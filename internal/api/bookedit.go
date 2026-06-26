package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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

// updateBookHandler handles PATCH /api/books/{id}: edits the user-facing title
// and author. Cover edits go through uploadCoverHandler.
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

		// Patch semantics: start from the current values, override only what was
		// sent. A trimmed empty title is rejected; author may be empty.
		title := book.Title
		if req.Title != nil {
			title = strings.TrimSpace(*req.Title)
		}
		author := book.Author
		if req.Author != nil {
			author = strings.TrimSpace(*req.Author)
		}

		if title == "" {
			writeError(w, http.StatusBadRequest, "invalid", "title must not be empty")
			return
		}
		if len(title) > maxBookTitleLen {
			writeError(w, http.StatusBadRequest, "invalid", "title too long")
			return
		}
		if len(author) > maxBookAuthorLen {
			writeError(w, http.StatusBadRequest, "invalid", "author too long")
			return
		}

		if err := pd.DB.UpdateBookMetadataContext(r.Context(), id, title, author); err != nil {
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
// uniform regardless of the source format/size.
func uploadCoverHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		if _, ok := pd.Books.Get(id); !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Re-encoding a large image can outlast the server WriteTimeout (armed at
		// header-read time). Clear the write deadline; the body is bounded below.
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

		coverPath, err := library.SaveCoverImage(r.Context(), pd.LibPath, id, data)
		if err != nil {
			if errors.Is(err, library.ErrCoverSkipped) {
				writeError(w, http.StatusBadRequest, "invalid", "image dimensions too large")
				return
			}
			// A decode failure means the upload was not a supported image; that is
			// user error, so surface it as a 400 rather than a 500.
			slog.Warn("save uploaded cover failed", "book", id, "err", err)
			writeError(w, http.StatusBadRequest, "invalid", "could not process image (use JPEG, PNG, or WebP)")
			return
		}

		if err := pd.DB.UpdateBookCoverContext(r.Context(), id, coverPath); err != nil {
			slog.Error("update book cover failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update cover")
			return
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
