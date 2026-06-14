package api

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"sayumi/internal/storage"
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

		importedIDs, scanErr := pd.Scanner.ScanNow(r.Context())

		// Warm the cache for everything imported, even if the scan was canceled
		// partway (scanErr != nil with a non-empty importedIDs). Those rows are
		// committed, and the next scan's dedup snapshot would treat them as known
		// and never re-report them — so without this they stay out of the cache
		// until a profile reopen. Use a cancel-free context for the reloads since
		// r.Context() may already be done on the cancellation path.
		reloadCtx := context.WithoutCancel(r.Context())
		for _, id := range importedIDs {
			// Only summary fields are needed to warm the cache: BookCache.Add stores
			// the summary and invalidates any spine, which GetSpine reloads lazily
			// when the book is first opened. Reading the heavy spine_json / toc_json
			// here would pay N overflow-page reads on a bulk import for spines that
			// are usually never opened right after a rescan.
			summary, found, err := pd.DB.GetBookSummaryContext(reloadCtx, id)
			if err != nil {
				// The book was imported into the DB but could not be reloaded for
				// the cache; skip it (a future restart will pick it up).
				slog.Warn("rescan: reload imported book failed", "book", id, "err", err)
				continue
			}
			if !found {
				continue
			}
			pd.Books.Add(storage.BookRecord{BookSummary: summary})
		}

		if scanErr != nil {
			slog.Error("library rescan failed", "err", scanErr)
			writeError(w, http.StatusInternalServerError, "scan_error", "failed to rescan library")
			return
		}

		writeJSON(w, http.StatusOK, map[string]int{"imported": len(importedIDs)})
	}
}

// filterAndSortBooks applies optional query (q), sort field, and order to a
// book list. Unknown/empty values fall back to no-filter and title-ascending,
// matching the client's default ordering.
//
// The books slice is sorted in place and may be reordered. The sole caller
// (listBooksHandler) passes a freshly built slice it owns, so cloning it just
// to sort would allocate and immediately discard a second N-element slice on
// the most-hit endpoint. When q matches, a new filtered slice is built and the
// input is left untouched.
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
