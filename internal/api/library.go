package api

import (
	"cmp"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

// rescanLibraryHandler re-scans the on-disk library folder for newly added
// EPUBs (e.g. files dropped in while the server was running) and imports any
// that are new, updating the in-memory book cache so they appear immediately.
func rescanLibraryHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		importedIDs, err := pd.Scanner.ScanNow(r.Context())
		if err != nil {
			slog.Error("library rescan failed", "err", err)
			writeError(w, http.StatusInternalServerError, "scan_error", "failed to rescan library")
			return
		}

		for _, id := range importedIDs {
			book, err := pd.DB.GetBookContext(r.Context(), id)
			if err != nil {
				// The book was imported into the DB but could not be reloaded for
				// the cache; skip it (a future restart will pick it up).
				if !errors.Is(err, sql.ErrNoRows) {
					slog.Warn("rescan: reload imported book failed", "book", id, "err", err)
				}
				continue
			}
			pd.Books.Add(book)
		}

		writeJSON(w, http.StatusOK, map[string]int{"imported": len(importedIDs)})
	}
}

// filterAndSortBooks applies optional query (q), sort field, and order to a
// book list. Unknown/empty values fall back to no-filter and title-ascending,
// matching the client's default ordering. The input slice is never mutated.
func filterAndSortBooks(books []BookResponse, q, sortField, order string) []BookResponse {
	if query := strings.ToLower(strings.TrimSpace(q)); query != "" {
		filtered := make([]BookResponse, 0, len(books))
		for _, b := range books {
			if strings.Contains(strings.ToLower(b.Title), query) ||
				strings.Contains(strings.ToLower(b.Author), query) {
				filtered = append(filtered, b)
			}
		}
		books = filtered
	} else {
		// Copy so the stable sort below doesn't reorder the caller's slice.
		books = slices.Clone(books)
	}

	desc := strings.EqualFold(strings.TrimSpace(order), "desc")
	byTitle := func(a, b BookResponse) int {
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	}

	var less func(a, b BookResponse) int
	switch strings.ToLower(strings.TrimSpace(sortField)) {
	case "author":
		less = func(a, b BookResponse) int {
			if c := cmp.Compare(strings.ToLower(a.Author), strings.ToLower(b.Author)); c != 0 {
				return c
			}
			return byTitle(a, b)
		}
	case "added":
		less = func(a, b BookResponse) int { return cmp.Compare(a.AddedAt, b.AddedAt) }
	case "read":
		less = func(a, b BookResponse) int { return cmp.Compare(a.LastReadAt, b.LastReadAt) }
	case "progress":
		less = func(a, b BookResponse) int {
			if c := cmp.Compare(a.Progress, b.Progress); c != 0 {
				return c
			}
			return byTitle(a, b)
		}
	case "", "title":
		less = byTitle
	default:
		less = byTitle
	}

	slices.SortStableFunc(books, func(a, b BookResponse) int {
		c := less(a, b)
		if desc {
			return -c
		}
		return c
	})
	return books
}
