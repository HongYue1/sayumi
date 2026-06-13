package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"sayumi/internal/epub"
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

	for part := range strings.SplitSeq(r.Header.Get("If-None-Match"), ",") {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if candidate == "*" || candidate == etag || candidate == "W/"+etag {
			return true
		}
	}
	return false
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

		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		spine, ok := pd.Books.GetSpine(r.Context(), id)
		if !ok {
			writeError(w, http.StatusInternalServerError, "parse_error", "failed to get spine")
			return
		}

		if chapterIndex < 0 || chapterIndex >= len(spine) {
			writeError(w, http.StatusBadRequest, "invalid", "chapter index out of range")
			return
		}

		w.Header().Set("Cache-Control", chapterCacheControl)

		if etag := chapterResponseETag(book.FileHash, chapterIndex); etag != "" {
			w.Header().Set("ETag", etag)
			if ifNoneMatchMatches(r, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		resp, err := epub.ProcessChapter(
			pd.Store,
			book.FilePath,
			spine,
			chapterIndex,
			book.ID,
			book.Direction,
			resourceTokenForBook(book.FileHash),
		)
		if err != nil {
			slog.Error("process chapter failed", "book", id, "chapter", chapterIndex, "err", err)
			writeError(w, http.StatusInternalServerError, "process_error", "failed to process chapter")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
