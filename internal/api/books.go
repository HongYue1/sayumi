package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"sayumi/internal/library"
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
	// UpdatedAt is the books row's updated_at. The client appends it to the cover
	// URL as ?v=<updatedAt> so an edited cover (which keeps the same path) busts
	// the immutable browser cache; it also folds into the cover/detail ETags.
	UpdatedAt string `json:"updatedAt,omitempty"`
	// Duplicate marks a response whose book was already in the library: the
	// upload was deduped by content hash, or it lost an import race against a
	// concurrent scan. It describes the outcome of the request, not the book, so
	// it is response-only and omitted when false.
	Duplicate bool `json:"duplicate,omitempty"`
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

// coverResponseETag identifies a cover response. The cover bytes for a given
// file_hash are immutable on import, but an in-place cover edit rewrites the
// same file, so bookUpdatedAt (the books row's updated_at, bumped on edit) is
// folded in to invalidate a non-versioned conditional request.
func coverResponseETag(fileHash, bookUpdatedAt string) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + ":" + bookUpdatedAt + ":cover" + `"`
}

const bookDetailCacheControl = "private, no-cache"

// bookDetailVersion is bumped if the shape of GET /api/books/{id} changes so
// stale cached detail responses revalidate after a deploy.
const bookDetailVersion = "1"

// bookDetailETag identifies a book-detail response. The spine + toc are
// immutable for a given file_hash, but the book's own metadata (e.g. title,
// cover) can change in place without a re-import, so bookUpdatedAt (the books
// row's updated_at) is folded in alongside progressVersion, which combines
// lastReadAt with the displayed progress value. Progress timestamps have
// second resolution, so the timestamp alone can miss a position change.
func bookDetailETag(fileHash, bookUpdatedAt, progressVersion string) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + ":" + bookUpdatedAt + ":" + progressVersion + ":" + bookDetailVersion + `"`
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
		UpdatedAt:    b.UpdatedAt,
	}
}

// enrichBookResponse fills in the fields a books-row summary cannot carry: the
// reader's progress, its timestamp, and the assigned flair. listBooksHandler
// loads both in one query per profile; a single-book response has to look them
// up for just this book, which is why this is separate from
// bookResponseFromSummary.
//
// Staged progress from the coalescer wins over the persisted row, so the
// response stays read-after-write consistent during the coalescer's durability
// window -- same precedence as getBookHandler.
//
// Best-effort by design: it decorates a response whose primary work (upload,
// metadata edit, cover replace) has already committed, so a failed lookup is
// logged and the field left at its zero value rather than turned into a 500
// that would tell the client the write itself failed.
func enrichBookResponse(r *http.Request, pd *profileDeps, br *BookResponse) {
	userID := getUserID(r)

	if prog, ok := pd.Progress.get(br.ID, userID); ok {
		br.Progress = calcProgress(prog.Chapter, prog.Percent, br.ChapterCount)
		br.LastReadAt = prog.UpdatedAt
	} else {
		prog, err := pd.DB.GetProgressContext(r.Context(), br.ID, userID)
		switch {
		case err == nil:
			br.Progress = calcProgress(prog.Chapter, prog.Percent, br.ChapterCount)
			br.LastReadAt = prog.UpdatedAt
		case errors.Is(err, storage.ErrNotFound):
		default:
			slog.Warn("enrich book response: load progress failed", "book", br.ID, "user", userID, "err", err)
		}
	}

	flairID, err := pd.DB.GetBookFlairContext(r.Context(), br.ID, userID)
	if err != nil {
		slog.Warn("enrich book response: load flair failed", "book", br.ID, "user", userID, "err", err)
		return
	}
	br.FlairID = flairID
}

func listBooksHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		summaries := pd.Books.ListSummaries()
		userID := getUserID(r)
		// Snapshot pending positions before reading the DB. A flush can finish
		// and remove them after the SQL read takes its snapshot; reading the
		// coalescer only afterward would then lose an acknowledged position.
		staged := pd.Progress.getAll(userID)
		allProgress, err := pd.DB.GetAllProgressContext(r.Context(), userID)
		if err != nil {
			slog.Error("load progress failed", "user", userID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load progress")
			return
		}
		// Overlay staged positions so the library API is read-after-write
		// consistent during the coalescer's short durability window.
		maps.Copy(allProgress, staged)

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

		if prog, ok := pd.Progress.get(book.ID, userID); ok {
			progress = calcProgress(prog.Chapter, prog.Percent, book.ChapterCount)
			lastReadAt = prog.UpdatedAt
		} else {
			prog, err := pd.DB.GetProgressContext(r.Context(), book.ID, userID)
			switch {
			case err == nil:
				progress = calcProgress(prog.Chapter, prog.Percent, book.ChapterCount)
				lastReadAt = prog.UpdatedAt
			case errors.Is(err, storage.ErrNotFound):
			default:
				slog.Error("load book progress failed", "book", book.ID, "user", userID, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load progress")
				return
			}
		}

		// Revalidate before fetching the large, immutable spine + toc JSON.
		// Include the displayed position as well as its second-resolution
		// timestamp so two scroll updates in one second cannot share a tag.
		progressVersion := lastReadAt + ":" + strconv.FormatFloat(progress, 'g', -1, 64)
		etag := bookDetailETag(book.FileHash, book.UpdatedAt, progressVersion)
		w.Header().Set("Cache-Control", bookDetailCacheControl)
		if etag != "" && ifNoneMatchMatches(r, etag) {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		spineJSON, tocJSON, err := pd.DB.GetBookContentContext(r.Context(), book.ID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
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

		// A failed content lookup must not attach the success validator to an
		// error body that a later conditional request could keep reusing.
		if etag != "" {
			w.Header().Set("ETag", etag)
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
		// Deletion removes the EPUB itself, so it takes the write side of the
		// generation gate used by chapter, download, and gofile readers. In
		// particular, Windows cannot remove a file while one of those readers
		// still has it open.
		pd.bookReplaceMu.Lock()
		defer pd.bookReplaceMu.Unlock()

		// Use the in-memory cache for the file paths needed during cleanup.
		// Fall back to the database only on a cache miss (e.g. after a restart
		// where the book was never loaded into cache for this profile session).
		book, ok := pd.Books.Get(id)
		if !ok {
			dbBook, err := pd.DB.GetBookContext(r.Context(), id)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					pd.Progress.dropBook(id)
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
			if errors.Is(err, storage.ErrNotFound) {
				pd.Books.Remove(id)
				pd.Progress.dropBook(id)
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
			slog.Error("delete book failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete book")
			return
		}

		pd.Books.Remove(id)
		// Drop any pending coalesced progress for this book; its progress row is
		// CASCADE-deleted above, so a staged position would otherwise retry
		// forever against a missing book_id FK.
		pd.Progress.dropBook(id)
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
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
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

		// Both waiting for an edit and streaming a cover can outlast the
		// header-armed WriteTimeout, so clear it before taking the gate.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear cover write deadline unsupported", "err", err)
		}
		// Pair the validator with the file generation and keep deletion from
		// removing its sidecar while Windows still has this reader open. The
		// file-close defer below runs before the gate is released.
		pd.bookReplaceMu.RLock()
		defer pd.bookReplaceMu.RUnlock()

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

		// The sidecar changes at the same /cover path. Edits advance updated_at
		// to trigger a frontend image reload, but unversioned or older URLs
		// must also revalidate rather than pinning stale bytes immutably.
		// 'no-cache' retains the bytes while checking the hash/version ETag;
		// unchanged covers return a stat-light 304 before Open + Stat. Matches
		// the book-detail revalidation policy (bookDetailCacheControl).
		w.Header().Set("Cache-Control", "private, no-cache")
		// Held back until success: an ETag left on the 404/500 paths below
		// describes a body this URL will later serve a 200 for, so a cache that
		// stored the error could be answered 304 for it.
		etag := coverResponseETag(book.FileHash, book.UpdatedAt)
		if etag != "" && ifNoneMatchMatches(r, etag) {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if pd.coverRoot == nil {
			slog.Error("cover root not available", "book", id)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to read cover")
			return
		}

		file, err := pd.coverRoot.Open(library.NormalizeCoverPath(book.CoverPath))
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

		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")

		http.ServeContent(w, r, "", fileInfo.ModTime(), file)
	}
}

// removeManagedLibraryFile deletes a file that was placed inside the library by
// Sayumi. Both absolute EPUB paths and relative cover paths are removed through
// os.Root: a lexical containment check followed by os.Remove would still follow
// an intermediate directory symlink that now points outside this library.
func removeManagedLibraryFile(libPath, targetPath, kind string) {
	if targetPath == "" {
		return
	}

	if filepath.IsAbs(targetPath) {
		rel, err := filepath.Rel(libPath, targetPath)
		if err != nil {
			slog.Error("managed file path escapes library root", "kind", kind, "path", targetPath)
			return
		}
		targetPath = rel
	} else {
		// Only stored cover paths need legacy Windows-separator normalization.
		// A backslash in a native Unix EPUB filename is a literal character.
		targetPath = library.NormalizeCoverPath(targetPath)
	}
	if !filepath.IsLocal(targetPath) || targetPath == "." {
		slog.Error("managed file path escapes library root", "kind", kind, "path", targetPath)
		return
	}

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
