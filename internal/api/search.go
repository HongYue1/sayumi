package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"sayumi/internal/epub"
)

func searchHandler(_ *Dependencies) http.HandlerFunc {
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

		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			writeJSON(w, http.StatusOK, epub.SearchResponse{Results: []epub.SearchResult{}})
			return
		}

		cursor := r.URL.Query().Get("cursor")
		limit := 200
		if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
			if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 && parsedLimit <= 200 {
				limit = parsedLimit
			}
		}

		spine, ok := pd.Books.GetSpine(id)
		if !ok {
			writeError(w, http.StatusInternalServerError, "parse_error", "failed to get spine")
			return
		}

		resp, err := epub.Search(r.Context(), pd.Store, book.FilePath, spine, query, cursor, limit)
		if err != nil {
			slog.Error("search failed", "book", id, "query", query, "err", err)
			writeError(w, http.StatusInternalServerError, "search_error", "search failed")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
