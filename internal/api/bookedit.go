package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

const (
	maxBookTitleLen  = 512
	maxBookAuthorLen = 512
	// maxCoverUploadBytes bounds the cover upload body. It matches
	// library.maxCoverBytes (the decode-time guard) so the two limits agree.
	maxCoverUploadBytes = 20 << 20 // 20 MB
	// maxBookMetaBody bounds the PATCH JSON body. Title + author are short text
	// fields, so a generous-but-finite cap stops an unbounded read.
	maxBookMetaBody = 64 << 10 // 64 KB
)

// updateBookRequest is the PATCH /api/books/{id} body. Fields are pointers so a
// missing key is distinguishable from an empty string: only provided fields are
// changed (patch semantics), an omitted field keeps its current value.
type updateBookRequest struct {
	Title  *string `json:"title"`
	Author *string `json:"author"`
}

// errBookInUse signals that a book's file could not be replaced because a
// reader currently holds it open. Handlers map it to a 409 so the client can
// prompt the user to close the book and retry.
var errBookInUse = errors.New("book is open in the reader")

// applyBookMetaPatch merges a PATCH body onto the current title/author.
// Omitted pointer fields keep the current value. Returns a user-facing error
// message when validation fails (empty title, overlong fields).
func applyBookMetaPatch(curTitle, curAuthor string, req updateBookRequest) (title, author string, errMsg string) {
	title = curTitle
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	author = curAuthor
	if req.Author != nil {
		author = strings.TrimSpace(*req.Author)
	}
	if title == "" {
		return "", "", "title must not be empty"
	}
	if len(title) > maxBookTitleLen {
		return "", "", "title too long"
	}
	if len(author) > maxBookAuthorLen {
		return "", "", "author too long"
	}
	return title, author, ""
}

// refreshBookCache reloads a book's summary from the DB and updates the
// in-memory book cache so list/detail responses (and their ETags) reflect a
// just-applied change. It returns the refreshed record for the JSON response.
func refreshBookCache(ctx context.Context, pd *profileDeps, id string) (storage.BookRecord, error) {
	summary, found, err := pd.DB.GetBookSummaryContext(ctx, id)
	if err != nil {
		return storage.BookRecord{}, fmt.Errorf("reload book %s: %w", id, err)
	}
	if !found {
		return storage.BookRecord{}, fmt.Errorf("book %s missing after update", id)
	}
	book := storage.BookRecord{BookSummary: summary}
	pd.Books.Add(book)
	return book, nil
}

// replaceBookFile rewrites the book's EPUB at filePath with edit applied, then
// atomically swaps the rewritten copy into place and returns the recomputed
// content hash and size. The new file is built as a sibling temp first; the
// swap requires that no reader currently holds the book open, so a book open in
// the reader yields errBookInUse and leaves the original file untouched. The
// temp file is removed on every failure path.
func replaceBookFile(ctx context.Context, pd *profileDeps, filePath string, edit epub.MetadataEdit) (hash string, size int64, err error) {
	tmpPath, err := epub.RewriteBook(filePath, edit)
	if err != nil {
		return "", 0, fmt.Errorf("rewrite epub: %w", err)
	}
	removeTmp := func() {
		if rmErr := os.Remove(tmpPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Error("remove temp epub failed", "path", tmpPath, "err", rmErr)
		}
	}

	// The cached reader must be fully released before the file is replaced. If a
	// request still holds it open, refuse rather than risk a torn/failed swap.
	if !pd.Store.TryCloseForReplace(filePath) {
		removeTmp()
		return "", 0, errBookInUse
	}

	if renameErr := os.Rename(tmpPath, filePath); renameErr != nil {
		removeTmp()
		// A rename failure here is most likely the file still being held open by
		// another in-flight read (Windows); treat it as retryable in-use.
		slog.Warn("replace epub failed", "path", filePath, "err", renameErr)
		return "", 0, errBookInUse
	}

	hash, size, err = library.HashFile(ctx, filePath)
	if err != nil {
		return "", 0, fmt.Errorf("rehash epub: %w", err)
	}
	return hash, size, nil
}

// updateBookHandler handles PATCH /api/books/{id}: edits the user-facing title
// and author. The change is written back into the EPUB's package document (so
// the file itself reflects it) and the recomputed file hash/size are persisted.
// Cover edits go through uploadCoverHandler.
func updateBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		var req updateBookRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBookMetaBody)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "invalid request body")
			return
		}

		title, author, errMsg := applyBookMetaPatch(book.Title, book.Author, req)
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "invalid", errMsg)
			return
		}

		// No effective change: skip rewriting the (potentially large) file.
		if title == book.Title && author == book.Author {
			writeJSON(w, http.StatusOK, bookResponseFromRecord(book))
			return
		}

		if book.FilePath == "" {
			writeError(w, http.StatusUnprocessableEntity, "no_file", "book has no source file to edit")
			return
		}

		// Rewriting a large EPUB can outlast the server WriteTimeout (armed at
		// header-read time). Clear the write deadline; the request body is bounded.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear book edit write deadline unsupported", "err", err)
		}

		hash, size, err := replaceBookFile(r.Context(), pd, book.FilePath, epub.MetadataEdit{Title: &title, Author: &author})
		if err != nil {
			if errors.Is(err, errBookInUse) {
				writeError(w, http.StatusConflict, "book_open", "close the book in the reader and try again")
				return
			}
			slog.Error("write book metadata into epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
			return
		}

		if err := pd.DB.UpdateBookMetadataAndFileContext(r.Context(), id, title, author, hash, size); err != nil {
			if errors.Is(err, storage.ErrFileHashConflict) {
				writeError(w, http.StatusConflict, "duplicate", "another copy of this book already exists")
				return
			}
			slog.Error("update book metadata failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update book")
			return
		}

		updated, err := refreshBookCache(r.Context(), pd, id)
		if err != nil {
			slog.Error("reload book after metadata update failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "book updated but failed to reload")
			return
		}
		writeJSON(w, http.StatusOK, bookResponseFromRecord(updated))
	}
}

// uploadCoverHandler handles PUT /api/books/{id}/cover: replaces a book's cover
// with an uploaded image (multipart field "cover"). The image is normalized to
// the same resized JPEG the importer produces, so the served cover stays
// uniform regardless of the source format/size. The normalized JPEG is embedded
// into the EPUB file itself and also written to the sidecar cover store that
// serves the displayed cover; books without a source file fall back to a
// sidecar-only update.
func uploadCoverHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Re-encoding a large image and rewriting the EPUB can outlast the server
		// WriteTimeout (armed at header-read time). Clear the write deadline; the
		// body is bounded below.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear cover upload write deadline unsupported", "err", err)
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadBytes+1024)
		if err := r.ParseMultipartForm(maxCoverUploadBytes + 1024); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "image too large (max 20MB)")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid", "invalid multipart form")
			return
		}
		if r.MultipartForm != nil {
			defer func() {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					slog.Error("clean multipart temp files failed", "err", err)
				}
			}()
		}

		file, _, err := r.FormFile("cover")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "missing cover file field")
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				slog.Error("close uploaded cover failed", "err", err)
			}
		}()

		data, err := io.ReadAll(io.LimitReader(file, maxCoverUploadBytes+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "failed to read cover upload")
			return
		}
		if int64(len(data)) > maxCoverUploadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "image too large (max 20MB)")
			return
		}

		// Normalize the upload to the same resized JPEG the importer produces. The
		// bytes are both embedded into the EPUB and written to the sidecar store.
		jpegData, err := library.EncodeCoverJPEG(r.Context(), id, data)
		if err != nil {
			if errors.Is(err, library.ErrCoverSkipped) {
				writeError(w, http.StatusBadRequest, "invalid", "image dimensions too large")
				return
			}
			// A decode failure means the upload was not a supported image; that is
			// user error, so surface it as a 400 rather than a 500.
			slog.Warn("process uploaded cover failed", "book", id, "err", err)
			writeError(w, http.StatusBadRequest, "invalid", "could not process image (use JPEG, PNG, or WebP)")
			return
		}

		// Embed into the EPUB first: a "book open" conflict then leaves both the
		// file and the displayed cover untouched (nothing has been written yet).
		var fileHash string
		var fileSize int64
		embedInFile := book.FilePath != ""
		if embedInFile {
			fileHash, fileSize, err = replaceBookFile(r.Context(), pd, book.FilePath, epub.MetadataEdit{CoverJPEG: jpegData})
			if err != nil {
				if errors.Is(err, errBookInUse) {
					writeError(w, http.StatusConflict, "book_open", "close the book in the reader and try again")
					return
				}
				slog.Error("embed cover into epub failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
				return
			}
		}

		coverPath, err := library.WriteCoverImageJPEG(pd.LibPath, id, jpegData)
		if err != nil {
			slog.Error("save uploaded cover failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "io_error", "failed to save cover")
			return
		}

		if embedInFile {
			if err := pd.DB.UpdateBookCoverAndFileContext(r.Context(), id, coverPath, fileHash, fileSize); err != nil {
				if errors.Is(err, storage.ErrFileHashConflict) {
					writeError(w, http.StatusConflict, "duplicate", "another copy of this book already exists")
					return
				}
				slog.Error("update book cover failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to update cover")
				return
			}
		} else {
			if err := pd.DB.UpdateBookCoverContext(r.Context(), id, coverPath); err != nil {
				slog.Error("update book cover failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to update cover")
				return
			}
		}

		updated, err := refreshBookCache(r.Context(), pd, id)
		if err != nil {
			slog.Error("reload book after cover update failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "cover updated but failed to reload")
			return
		}
		writeJSON(w, http.StatusOK, bookResponseFromRecord(updated))
	}
}
