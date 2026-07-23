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

		params := parseSearchRequestParams(r)
		id := r.PathValue("id")
		if params.query == "" {
			if _, ok := pd.Books.Get(id); !ok {
				writeError(w, http.StatusNotFound, "not_found", "book not found")
				return
			}
			writeJSON(w, http.StatusOK, epub.SearchResponse{Results: []epub.SearchResult{}})
			return
		}

		// Pair the BookCache/spine snapshots and extracted-text cache with one
		// complete on-disk EPUB generation. Searches retain the read side while
		// scanning, so concurrent searches stay concurrent while replacement and
		// deletion wait until every active ZIP reader has been released.
		pd.bookReplaceMu.RLock()
		defer pd.bookReplaceMu.RUnlock()

		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
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

		resp, err := epub.Search(
			r.Context(),
			pd.Store,
			book.FilePath,
			spine,
			params.query,
			params.cursor,
			params.limit,
		)
		if err != nil {
			// The reader aborts the previous in-flight search on every keystroke
			// (debounced search box), which cancels this request's context. That's
			// expected client behavior, not a failure — stay quiet and don't try to
			// write a 500 to a connection the client already walked away from.
			if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
				return
			}
			slog.Error("search failed", "book", id, "query", params.query, "err", err)
			writeError(w, http.StatusInternalServerError, "search_error", "search failed")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

type searchRequestParams struct {
	query  string
	cursor string
	limit  int
}

func parseSearchRequestParams(r *http.Request) searchRequestParams {
	values := r.URL.Query()
	params := searchRequestParams{
		query:  strings.TrimSpace(values.Get("q")),
		cursor: values.Get("cursor"),
		limit:  200,
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 && parsedLimit <= 200 {
			params.limit = parsedLimit
		}
	}
	return params
}
