package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

const chapterCacheControl = "private, no-cache"

func chapterResponseETag(fileHash string, chapterIndex int) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + ":" + strconv.Itoa(chapterIndex) + ":" + epub.ChapterRenderVersion + `"`
}

func ifNoneMatchMatches(r *http.Request, etag string) bool {
	if etag == "" {
		return false
	}

	for _, fieldValue := range r.Header.Values("If-None-Match") {
		for part := range strings.SplitSeq(fieldValue, ",") {
			candidate := strings.TrimSpace(part)
			if candidate == "" {
				continue
			}
			if candidate == "*" || candidate == etag || candidate == "W/"+etag {
				return true
			}
		}
	}
	return false
}

func requestContextDone(r *http.Request, err error) bool {
	return errors.Is(err, context.Canceled) || r.Context().Err() != nil
}

func getChapterHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		chapterIndex, err := strconv.Atoi(r.PathValue("index"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "invalid chapter index")
			return
		}

		// Pair the BookCache snapshot and rendered response with one complete
		// on-disk EPUB generation. In-place edits take the write side only for
		// their short rename + DB/cache commit window, so normal chapter requests
		// remain concurrent and the expensive rewrite/hash phase does not block
		// readers.
		pd.bookReplaceMu.RLock()
		defer pd.bookReplaceMu.RUnlock()

		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// BookSummary already carries the authoritative chapter count. Validate
		// and revalidate before lazily loading/parsing spine_json so a cold-cache
		// 304 stays DB- and allocation-free.
		if chapterIndex < 0 || chapterIndex >= book.ChapterCount {
			writeError(w, http.StatusBadRequest, "invalid", "chapter index out of range")
			return
		}

		// Rendering a large chapter and streaming it over a slow link can outlast
		// the server WriteTimeout, which net/http arms at header-read time.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear chapter write deadline unsupported", "err", err)
		}

		w.Header().Set("Cache-Control", chapterCacheControl)

		// The validator is held back until the response is committed to success:
		// an ETag on a 500 describes a body this URL will later serve a 200 for,
		// so a cache holding the error can be given a 304 for it and keep
		// rendering the error in place of the chapter.
		etag := chapterResponseETag(book.FileHash, chapterIndex)
		if etag != "" && ifNoneMatchMatches(r, etag) {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}

		spine, ok, err := pd.Books.GetSpine(r.Context(), id)
		if err != nil {
			if requestContextDone(r, err) {
				return
			}
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
			slog.Error("load spine failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "parse_error", "failed to get spine")
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		if chapterIndex >= len(spine) {
			writeError(w, http.StatusBadRequest, "invalid", "chapter index out of range")
			return
		}

		resp, err := epub.ProcessChapter(
			r.Context(),
			pd.Store,
			book.FilePath,
			spine,
			chapterIndex,
			book.ID,
			book.Direction,
			resourceTokenForBook(book.FileHash),
		)
		if err != nil {
			if requestContextDone(r, err) {
				// Client disconnected mid-render; the response is moot and this
				// is not a server fault, so don't log it as an error.
				return
			}
			slog.Error("process chapter failed", "book", id, "chapter", chapterIndex, "err", err)
			writeError(w, http.StatusInternalServerError, "process_error", "failed to process chapter")
			return
		}

		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
