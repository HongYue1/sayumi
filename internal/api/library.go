package api

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"sayumi/internal/library"
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

		scanResult, scanErr := pd.Scanner.ScanNowWithChanges(r.Context())

		// Warm the cache for everything imported, even if the scan was canceled
		// partway (scanErr != nil with non-empty ImportedIDs). Those rows are
		// committed, and the next scan's dedup snapshot would treat them as known
		// and never re-report them — so without this they stay out of the cache
		// until a profile reopen. Also refresh existing books whose cover backfill
		// changed their summary. Use a cancel-free context for the reloads since
		// r.Context() may already be done on the cancellation path.
		reloadCtx := context.WithoutCancel(r.Context())
		warmBook := func(id string) {
			// Keep the DB snapshot and cache publication atomic against edit and
			// delete writers. Otherwise a delete can be followed by a stale Add,
			// resurrecting a cache-only ghost, or an edit can be overwritten by the
			// older summary loaded here.
			pd.bookReplaceMu.RLock()
			defer pd.bookReplaceMu.RUnlock()

			// Only summary fields are needed to warm the cache: BookCache.Add stores
			// the summary and invalidates any spine, which GetSpine reloads lazily
			// when the book is first opened. Reading the heavy spine_json / toc_json
			// here would pay N overflow-page reads on a bulk import for spines that
			// are usually never opened right after a rescan.
			summary, found, err := pd.DB.GetBookSummaryContext(reloadCtx, id)
			if err != nil {
				// The book was imported into the DB but could not be reloaded for
				// the cache; skip it (a future restart will pick it up).
				slog.Warn("rescan: reload changed book failed", "book", id, "err", err)
				return
			}
			if !found {
				return
			}
			pd.Books.Add(storage.BookRecord{BookSummary: summary})
		}
		for _, id := range scanResult.ImportedIDs {
			warmBook(id)
		}
		for _, id := range scanResult.RefreshedIDs {
			warmBook(id)
		}

		resp, ok := rescanResponse(scanResult, scanErr)
		if !ok {
			slog.Error("library rescan failed", "err", scanErr)
			writeError(w, http.StatusInternalServerError, "scan_error", "failed to rescan library")
			return
		}
		if scanErr != nil {
			slog.Warn("library rescan stopped after partial work",
				"imported", resp["imported"], "refreshed", resp["refreshed"], "err", scanErr)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// rescanResponse builds the rescan payload and reports whether it should be
// sent at all.
//
// refreshed is carried alongside imported because a scan that only backfilled
// covers changes what the client must display while importing nothing; with an
// import count alone the client cannot tell that apart from a no-op.
//
// A scan that failed *after* committing work is reported as a 200 with
// partial: true rather than a bare 500. Those rows are durable, and the next
// scan's dedup snapshot treats them as known and never re-reports them, so a
// flat error would permanently hide books that are now in the library. ok is
// false only when nothing was committed, leaving the caller to write the 500.
func rescanResponse(result library.ScanResult, scanErr error) (map[string]any, bool) {
	imported := len(result.ImportedIDs)
	refreshed := len(result.RefreshedIDs)
	if scanErr != nil && imported == 0 && refreshed == 0 {
		return nil, false
	}

	resp := map[string]any{"imported": imported, "refreshed": refreshed}
	if scanErr != nil {
		resp["partial"] = true
	}
	return resp, true
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
//
// This is the third place library titles get ordered, after the list query's
// ORDER BY (COLLATE NOCASE) and BookCache's ASCII fold. The sort here is
// stable, so equal titles keep the order the cache produced -- which is why
// that layer breaks ties on ID. Folding here is Unicode where the other two
// are ASCII; the difference is deliberate, not an oversight.
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
	// Lowercased title keys back the default/title sort and the author/progress
	// tie-break, so they are precomputed once (O(N)) instead of lowercasing
	// inside the comparator, which the stable sort would call O(N log N) times —
	// each strings.ToLower allocating a fresh string. They are built lazily on
	// first use so the "added" and "read" sorts (which never call byTitle) skip
	// the map and per-title allocations entirely. Keys are keyed by the stable
	// book ID, not slice position, so they stay correct as the sort swaps
	// elements; this keeps the in-place, no-clone sort below (no second
	// []BookResponse is allocated).
	var lowerTitle map[string]string
	byTitle := func(a, b BookResponse) int {
		if lowerTitle == nil {
			lowerTitle = make(map[string]string, len(books))
			for i := range books {
				lowerTitle[books[i].ID] = strings.ToLower(books[i].Title)
			}
		}
		return cmp.Compare(lowerTitle[a.ID], lowerTitle[b.ID])
	}

	var less func(a, b BookResponse) int
	switch strings.ToLower(strings.TrimSpace(sortField)) {
	case "author":
		// Author sort also needs lowercased author keys; build them once here
		// so this non-default path pays no per-comparison ToLower either.
		lowerAuthor := make(map[string]string, len(books))
		for i := range books {
			lowerAuthor[books[i].ID] = strings.ToLower(books[i].Author)
		}
		less = func(a, b BookResponse) int {
			return cmp.Or(cmp.Compare(lowerAuthor[a.ID], lowerAuthor[b.ID]), byTitle(a, b))
		}
	case "added":
		less = func(a, b BookResponse) int { return cmp.Compare(a.AddedAt, b.AddedAt) }
	case "read":
		less = func(a, b BookResponse) int { return cmp.Compare(a.LastReadAt, b.LastReadAt) }
	case "progress":
		less = func(a, b BookResponse) int {
			return cmp.Or(cmp.Compare(a.Progress, b.Progress), byTitle(a, b))
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
