package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
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

		spine, ok, err := pd.Books.GetSpine(r.Context(), id)
		if err != nil {
			if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
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

		resp, err := epub.Search(r.Context(), pd.Store, book.FilePath, spine, query, cursor, limit)
		if err != nil {
			// The reader aborts the previous in-flight search on every keystroke
			// (debounced search box), which cancels this request's context. That's
			// expected client behavior, not a failure — stay quiet and don't try to
			// write a 500 to a connection the client already walked away from.
			if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
				return
			}
			slog.Error("search failed", "book", id, "query", query, "err", err)
			writeError(w, http.StatusInternalServerError, "search_error", "search failed")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
