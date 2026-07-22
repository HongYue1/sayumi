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

type preparedBookFile struct {
	tmpPath string
	hash    string
	size    int64
}

func (p preparedBookFile) cleanup() {
	if p.tmpPath == "" {
		return
	}
	if err := os.Remove(p.tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove temp epub failed", "path", p.tmpPath, "err", err)
	}
}

// prepareBookFile builds and hashes the replacement beside the source EPUB.
// This expensive work intentionally runs before bookReplaceMu's write lock so
// chapter reads remain fully concurrent; bookEditMu serializes edit handlers,
// keeping the source generation stable while this preparation runs.
func prepareBookFile(ctx context.Context, filePath string, edit epub.MetadataEdit) (preparedBookFile, error) {
	tmpPath, err := epub.RewriteBook(filePath, edit)
	if err != nil {
		return preparedBookFile{}, fmt.Errorf("rewrite epub: %w", err)
	}
	prepared := preparedBookFile{tmpPath: tmpPath}
	prepared.hash, prepared.size, err = library.HashFile(ctx, tmpPath)
	if err != nil {
		prepared.cleanup()
		return preparedBookFile{}, fmt.Errorf("rehash epub: %w", err)
	}
	return prepared, nil
}

// replaceBookFile atomically installs a prepared sibling file. The caller must
// hold bookReplaceMu's write side through the following DB/cache refresh so no
// chapter can pair the new bytes with the previous file hash/resource token.
func replaceBookFile(pd *profileDeps, filePath, tmpPath string) error {
	// The cached reader must be fully released before the file is replaced. If a
	// request still holds it open, refuse rather than risk a torn/failed swap.
	if !pd.Store.TryCloseForReplace(filePath) {
		return errBookInUse
	}

	if renameErr := os.Rename(tmpPath, filePath); renameErr != nil {
		// A rename failure here is most likely the file still being held open by
		// another in-flight read (Windows); treat it as retryable in-use.
		slog.Warn("replace epub failed", "path", filePath, "err", renameErr)
		return errBookInUse
	}
	return nil
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

		// Serialize edit preparation without blocking chapter readers. Re-read the
		// book after taking the lock so a preceding cover/metadata edit cannot be
		// overwritten from the stale pre-decode snapshot above.
		pd.bookEditMu.Lock()
		defer pd.bookEditMu.Unlock()
		book, ok = pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
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

		prepared, err := prepareBookFile(r.Context(), book.FilePath, epub.MetadataEdit{Title: &title, Author: &author})
		if err != nil {
			slog.Error("write book metadata into epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
			return
		}
		defer prepared.cleanup()

		pd.bookReplaceMu.Lock()
		defer pd.bookReplaceMu.Unlock()
		if err := replaceBookFile(pd, book.FilePath, prepared.tmpPath); err != nil {
			if errors.Is(err, errBookInUse) {
				writeError(w, http.StatusConflict, "book_open", "close the book in the reader and try again")
				return
			}
			slog.Error("write book metadata into epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
			return
		}

		if err := pd.DB.UpdateBookMetadataAndFileContext(r.Context(), id, title, author, prepared.hash, prepared.size); err != nil {
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

		// Cover decoding is deliberately outside this lock: it can be expensive
		// and does not inspect or mutate the EPUB. Serialize only the edit/file
		// generation work, then refresh the book snapshot before preparing it.
		pd.bookEditMu.Lock()
		defer pd.bookEditMu.Unlock()
		book, ok = pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Embed into the EPUB first: a "book open" conflict then leaves both the
		// file and the displayed cover untouched (nothing has been written yet).
		var fileHash string
		var fileSize int64
		embedInFile := book.FilePath != ""
		if embedInFile {
			prepared, prepareErr := prepareBookFile(r.Context(), book.FilePath, epub.MetadataEdit{CoverJPEG: jpegData})
			if prepareErr != nil {
				slog.Error("embed cover into epub failed", "book", id, "err", prepareErr)
				writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
				return
			}
			defer prepared.cleanup()

			pd.bookReplaceMu.Lock()
			defer pd.bookReplaceMu.Unlock()
			err = replaceBookFile(pd, book.FilePath, prepared.tmpPath)
			if err != nil {
				if errors.Is(err, errBookInUse) {
					writeError(w, http.StatusConflict, "book_open", "close the book in the reader and try again")
					return
				}
				slog.Error("embed cover into epub failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
				return
			}
			fileHash = prepared.hash
			fileSize = prepared.size
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
