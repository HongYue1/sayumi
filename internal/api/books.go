package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sayumi/internal/storage"
)

type BookResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	Language     string  `json:"language"`
	Publisher    string  `json:"publisher"`
	Description  string  `json:"description"`
	PubDate      string  `json:"pubDate"`
	HasCover     bool    `json:"hasCover"`
	Direction    string  `json:"direction"`
	ChapterCount int     `json:"chapterCount"`
	Progress     float64 `json:"progress"`
	FlairID      string  `json:"flairId,omitempty"`
	AddedAt      string  `json:"addedAt,omitempty"`
	LastReadAt   string  `json:"lastReadAt,omitempty"`
}

type BookDetailResponse struct {
	BookResponse
	Spine json.RawMessage `json:"spine"`
	TOC   json.RawMessage `json:"toc"`
}

func calcProgress(chapter int, percent float64, chapterCount int) float64 {
	if chapterCount <= 0 {
		return 0
	}
	progress := (float64(chapter) + percent) / float64(chapterCount)
	return max(0, min(progress, 1))
}

func coverResponseETag(fileHash string) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + ":cover" + `"`
}

const bookDetailCacheControl = "private, no-cache"

// bookDetailVersion is bumped if the shape of GET /api/books/{id} changes so
// stale cached detail responses revalidate after a deploy.
const bookDetailVersion = "1"

// bookDetailETag identifies a book-detail response. The spine + toc are
// immutable for a given file_hash, but the book's own metadata (e.g. title,
// cover) can change in place without a re-import, so bookUpdatedAt (the books
// row's updated_at) is folded in alongside the reader's progress; lastReadAt
// (the progress row's updated_at, empty when there is no progress) captures the
// latter. A re-open with unchanged metadata and progress revalidates to 304.
func bookDetailETag(fileHash, bookUpdatedAt, lastReadAt string) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + ":" + bookUpdatedAt + ":" + lastReadAt + ":" + bookDetailVersion + `"`
}

// bookResponseFromSummary constructs a BookResponse from a BookSummary.
// Progress and LastReadAt must be set separately if known.
func bookResponseFromSummary(b storage.BookSummary) BookResponse {
	return BookResponse{
		ID:           b.ID,
		Title:        b.Title,
		Author:       b.Author,
		Language:     b.Language,
		Publisher:    b.Publisher,
		Description:  b.Description,
		PubDate:      b.PubDate,
		HasCover:     b.HasCover,
		Direction:    b.Direction,
		ChapterCount: b.ChapterCount,
		AddedAt:      b.CreatedAt,
	}
}

func listBooksHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		summaries := pd.Books.ListSummaries()
		userID := getUserID(r)
		allProgress, err := pd.DB.GetAllProgressContext(r.Context(), userID)
		if err != nil {
			slog.Error("load progress failed", "user", userID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load progress")
			return
		}

		bookFlairs, err := pd.DB.GetAllBookFlairsContext(r.Context(), userID)
		if err != nil {
			slog.Error("load book flairs failed", "user", userID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load flairs")
			return
		}

		resp := make([]BookResponse, 0, len(summaries))
		for _, book := range summaries {
			br := bookResponseFromSummary(book)
			if progress, ok := allProgress[book.ID]; ok {
				br.Progress = calcProgress(progress.Chapter, progress.Percent, book.ChapterCount)
				br.LastReadAt = progress.UpdatedAt
			}
			if flairID, ok := bookFlairs[book.ID]; ok {
				br.FlairID = flairID
			}
			resp = append(resp, br)
		}

		// Optional server-side search/sort. The client filters in-memory for
		// instant feedback, but these params keep GET /api/books a complete,
		// directly-usable API (and support future pagination).
		q := r.URL.Query()
		resp = filterAndSortBooks(resp, q.Get("q"), q.Get("sort"), q.Get("order"))

		writeJSON(w, http.StatusOK, resp)
	}
}

func getBookHandler(_ *Dependencies) http.HandlerFunc {
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

		userID := getUserID(r)
		var progress float64
		var lastReadAt string

		prog, err := pd.DB.GetProgressContext(r.Context(), book.ID, userID)
		switch {
		case err == nil:
			progress = calcProgress(prog.Chapter, prog.Percent, book.ChapterCount)
			lastReadAt = prog.UpdatedAt
		case errors.Is(err, sql.ErrNoRows):
		default:
			slog.Error("load book progress failed", "book", book.ID, "user", userID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load progress")
			return
		}

		// The detail payload is dominated by the immutable spine + toc JSON (tens
		// of KB); its only mutable part is the reader's own progress. An ETag over
		// file_hash + last-progress-update lets an unchanged re-open return a
		// 0-byte 304 instead of re-fetching, re-marshaling and re-gzipping the
		// whole body. The content fetch + marshal happen only past the 304 check,
		// so a hit costs just the indexed progress lookup already done above.
		w.Header().Set("Cache-Control", bookDetailCacheControl)
		if etag := bookDetailETag(book.FileHash, book.UpdatedAt, lastReadAt); etag != "" {
			w.Header().Set("ETag", etag)
			if ifNoneMatchMatches(r, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		spineJSON, tocJSON, err := pd.DB.GetBookContentContext(r.Context(), book.ID)
		if err != nil {
			slog.Error("load book content failed", "book", book.ID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load book")
			return
		}

		br := bookResponseFromSummary(book.BookSummary)
		br.Progress = progress
		br.LastReadAt = lastReadAt

		resp := BookDetailResponse{
			BookResponse: br,
			Spine:        json.RawMessage(spineJSON),
			TOC:          json.RawMessage(tocJSON),
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

func deleteBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")

		// Use the in-memory cache for the file paths needed during cleanup.
		// Fall back to the database only on a cache miss (e.g. after a restart
		// where the book was never loaded into cache for this profile session).
		book, ok := pd.Books.Get(id)
		if !ok {
			dbBook, err := pd.DB.GetBookContext(r.Context(), id)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "not_found", "book not found")
					return
				}
				slog.Error("load book for deletion failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load book")
				return
			}
			book = dbBook
		}

		if err := pd.DB.DeleteBookContext(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				pd.Books.Remove(id)
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
			slog.Error("delete book failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete book")
			return
		}

		pd.Books.Remove(id)
		pd.Store.CloseBook(book.FilePath)
		pd.Store.EvictBook(book.FilePath)
		removeManagedLibraryFile(pd.LibPath, book.FilePath, "book")
		removeManagedLibraryFile(pd.LibPath, book.CoverPath, "cover")

		w.WriteHeader(http.StatusNoContent)
	}
}

func getTocHandler(_ *Dependencies) http.HandlerFunc {
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

		_, tocJSON, err := pd.DB.GetBookContentContext(r.Context(), id)
		if err != nil {
			slog.Error("load book toc failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load toc")
			return
		}

		writeJSON(w, http.StatusOK, json.RawMessage(tocJSON))
	}
}

func getCoverHandler(_ *Dependencies) http.HandlerFunc {
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
		if !book.HasCover || book.CoverPath == "" {
			writeError(w, http.StatusNotFound, "no_cover", "book has no cover")
			return
		}

		libRoot, err := os.OpenRoot(pd.LibPath)
		if err != nil {
			slog.Error("open library root for cover failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to read cover")
			return
		}
		defer func() {
			if closeErr := libRoot.Close(); closeErr != nil {
				slog.Error("close library root failed", "book", id, "err", closeErr)
			}
		}()

		file, err := libRoot.Open(book.CoverPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "no_cover", "cover file not found")
			return
		}
		defer func() { _ = file.Close() }()

		fileInfo, err := file.Stat()
		if err != nil {
			writeError(w, http.StatusNotFound, "no_cover", "cover file not found")
			return
		}

		contentType := mime.TypeByExtension(filepath.Ext(book.CoverPath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if etag := coverResponseETag(book.FileHash); etag != "" {
			w.Header().Set("ETag", etag)
			if ifNoneMatchMatches(r, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		http.ServeContent(w, r, "", fileInfo.ModTime(), file)
	}
}

// removeManagedLibraryFile deletes a file that was placed inside the library by
// Sayumi. FilePath is stored as an absolute path (via filepath.Abs in the
// scanner), so it is validated against libPath and removed directly. CoverPath
// is stored as a relative path and removed via os.Root for sandboxed access.
func removeManagedLibraryFile(libPath, targetPath, kind string) {
	if targetPath == "" {
		return
	}

	if filepath.IsAbs(targetPath) {
		// Absolute path (e.g. book.FilePath): verify it is still inside the
		// library before removing, then call os.Remove directly.
		rel, err := filepath.Rel(libPath, targetPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			slog.Error("managed file path escapes library root", "kind", kind, "path", targetPath)
			return
		}
		if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("remove managed file failed", "kind", kind, "path", targetPath, "err", err)
		}
		return
	}

	// Relative path (e.g. book.CoverPath): use os.Root for sandboxed removal.
	libRoot, err := os.OpenRoot(libPath)
	if err != nil {
		slog.Error("open library root for file removal failed", "kind", kind, "err", err)
		return
	}
	defer func() {
		if closeErr := libRoot.Close(); closeErr != nil {
			slog.Error("close library root failed", "kind", kind, "err", closeErr)
		}
	}()

	if err := libRoot.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove managed file failed", "kind", kind, "path", targetPath, "err", err)
	}
}
