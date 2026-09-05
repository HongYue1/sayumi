package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"sayumi/internal/epub"
)

// BookCache holds ordered book summaries and lazily parsed spines.
type BookCache struct {
	// loadBookContent backs lazy spine loads. The cache is built from book
	// summaries, so the heavy spine/toc JSON is fetched only when a book opens.
	// Keeping this as a function also makes concurrent-load behavior testable.
	loadBookContent func(context.Context, string) (string, string, error)

	mu             sync.RWMutex
	byID           map[string]BookRecord
	order          []string
	generations    map[string]uint64
	nextGeneration uint64
	spineLoads     singleflight.Group

	// spines is populated on first use, avoiding JSON parsing for books that
	// are only listed. Add and Remove invalidate entries so replacements never
	// reuse parsed content from an older generation.
	spines map[string][]epub.SpineEntry
}

func NewBookCache(ctx context.Context, db *DB) (*BookCache, error) {
	listStart := time.Now()
	summaries, err := db.ListBookSummariesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("populate book cache: %w", err)
	}
	listDur := time.Since(listStart)

	c := &BookCache{
		loadBookContent: db.GetBookContentContext,
		byID:            make(map[string]BookRecord, len(summaries)),
		order:           make([]string, 0, len(summaries)),
		spines:          make(map[string][]epub.SpineEntry),
		generations:     make(map[string]uint64),
	}
	for _, s := range summaries {
		// Summaries omit the large spine/toc JSON; book reads fetch it on demand.
		c.byID[s.ID] = BookRecord{BookSummary: s}
		c.order = append(c.order, s.ID)
	}

	// Separate SQL list time from in-memory cache construction in diagnostics.
	slog.Debug("book cache built", "books", len(summaries), "list_books", listDur)

	return c, nil
}

func (c *BookCache) Get(id string) (BookRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.byID[id]
	return b, ok
}

type spineLoadResult struct {
	spine []epub.SpineEntry
	found bool
	retry bool
}

// GetSpine returns shared, immutable entries. Callers must not modify the slice
// or its elements; a missing book returns found=false without an error.
func (c *BookCache) GetSpine(ctx context.Context, id string) ([]epub.SpineEntry, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}

		// Fast path: the book exists and its spine has already been parsed.
		c.mu.RLock()
		if spine, ok := c.spines[id]; ok {
			c.mu.RUnlock()
			return spine, true, nil
		}
		book, ok := c.byID[id]
		generation := c.generations[id]
		c.mu.RUnlock()
		if !ok {
			return nil, false, nil
		}

		// Only one cold load per book generation reaches SQLite and JSON parsing.
		// Each waiter may still cancel independently; the shared load is allowed to
		// finish and populate the cache for later requests.
		key := fmt.Sprintf("%s\x00%d", id, generation)
		loadCtx := context.WithoutCancel(ctx)
		resultCh := c.spineLoads.DoChan(key, func() (any, error) {
			return c.loadSpine(loadCtx, id, book, generation)
		})

		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case result := <-resultCh:
			if result.Err != nil {
				return nil, false, result.Err
			}
			loaded := result.Val.(spineLoadResult)
			if loaded.retry {
				continue
			}
			return loaded.spine, loaded.found, nil
		}
	}
}

func (c *BookCache) loadSpine(
	ctx context.Context,
	id string,
	book BookRecord,
	generation uint64,
) (spineLoadResult, error) {
	// A late caller can enter singleflight after another flight has finished.
	// Recheck the memo and generation before doing duplicate or obsolete work.
	c.mu.RLock()
	result, done := c.spineLoadStateLocked(id, book, generation)
	c.mu.RUnlock()
	if done {
		return result, nil
	}

	spineJSON := book.SpineJSON
	var err error
	if spineJSON == "" {
		spineJSON, _, err = c.loadBookContent(ctx, id)
		if err != nil {
			err = fmt.Errorf("load spine for book %s: %w", id, err)
		}
	}
	var parsed []epub.SpineEntry
	if err == nil {
		parsed, err = parseSpine(id, spineJSON)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Invalidation applies to failures too: a removed or replaced book must
	// not inherit a read/parse error from its obsolete generation.
	if result, done := c.spineLoadStateLocked(id, book, generation); done {
		return result, nil
	}
	if err != nil {
		return spineLoadResult{}, err
	}
	c.spines[id] = parsed
	return spineLoadResult{spine: parsed, found: true}, nil
}

// spineLoadStateLocked requires mu to be held for reading or writing. done is
// true when a memo, removal, or replacement has already resolved this load.
func (c *BookCache) spineLoadStateLocked(id string, book BookRecord, generation uint64) (result spineLoadResult, done bool) {
	if spine, ok := c.spines[id]; ok {
		return spineLoadResult{spine: spine, found: true}, true
	}
	current, exists := c.byID[id]
	if !exists {
		return spineLoadResult{}, true
	}
	if c.generations[id] != generation || current != book {
		return spineLoadResult{retry: true}, true
	}
	return spineLoadResult{}, false
}

func (c *BookCache) ListSummaries() []BookSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]BookSummary, 0, len(c.order))
	for _, id := range c.order {
		if b, ok := c.byID[id]; ok {
			out = append(out, b.BookSummary)
		}
	}
	return out
}

// compareASCIIFold matches SQLite's NOCASE collation without allocating folded
// strings. Folding is ASCII-only; bytes >= 0x80 are compared unchanged so live
// cache updates preserve the database order used at construction.
func compareASCIIFold(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 32
		}
		if y >= 'A' && y <= 'Z' {
			y += 32
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
		if x == 0 {
			// NOCASE stops at equal NUL bytes, then compares original lengths.
			break
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// insertPosition returns the index in order where a book with this title and ID
// belongs. Ties on title are broken by ID to match the list query's ORDER BY
// title COLLATE NOCASE ASC, id ASC: without that second key a live Add lands at
// the front of a run of equal titles while a cache rebuilt at startup puts it in
// ID order, so a book changes position across a restart with no edit behind it.
func (c *BookCache) insertPosition(title, id string) int {
	pos, _ := slices.BinarySearchFunc(c.order, title, func(existingID, target string) int {
		existing, ok := c.byID[existingID]
		if !ok {
			// A valid cache has no orphaned IDs. Keep insertion bounds safe if
			// an incomplete fixture violates that invariant.
			return 1
		}
		if cmp := compareASCIIFold(existing.Title, target); cmp != 0 {
			return cmp
		}
		return strings.Compare(existingID, id)
	})
	return pos
}

// removeFromOrder requires mu to be held and byID to still contain the old
// record, since its title participates in the binary search.
func (c *BookCache) removeFromOrder(title, id string) {
	pos := c.insertPosition(title, id)
	if pos < len(c.order) && c.order[pos] == id {
		c.order = slices.Delete(c.order, pos, pos+1)
	}
}

// Add inserts or replaces a book record. If the book already exists and its
// title has changed, the entry is re-sorted into the correct position so the
// order slice stays consistent with alphabetical ordering by title.
func (c *BookCache) Add(b BookRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextGeneration++
	c.generations[b.ID] = c.nextGeneration

	// Fast path: an existing book whose title is unchanged keeps its sort
	// position, so skip the O(N) order-slice delete + binary-search reinsert and
	// just refresh the record in place. This covers duplicate re-uploads and
	// cache re-warms where only non-title fields (e.g. cover state) change.
	if existing, exists := c.byID[b.ID]; exists && existing.Title == b.Title {
		c.byID[b.ID] = b
		delete(c.spines, b.ID)
		return
	}

	if existing, exists := c.byID[b.ID]; exists {
		c.removeFromOrder(existing.Title, b.ID)
	}

	pos := c.insertPosition(b.Title, b.ID)
	c.order = slices.Insert(c.order, pos, b.ID)
	c.byID[b.ID] = b
	// Invalidate any memoized spine; GetSpine re-parses lazily from the
	// (possibly changed) SpineJSON on next access.
	delete(c.spines, b.ID)
}

func (c *BookCache) Remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	book, exists := c.byID[id]
	if !exists {
		return
	}
	c.removeFromOrder(book.Title, id)
	c.nextGeneration++
	delete(c.generations, id)
	delete(c.byID, id)
	delete(c.spines, id)
}

func (c *BookCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

func parseSpine(bookID, spineJSON string) ([]epub.SpineEntry, error) {
	var entries []epub.SpineEntry
	if err := json.Unmarshal([]byte(spineJSON), &entries); err != nil {
		return nil, fmt.Errorf("parse spine JSON for book %s: %w", bookID, err)
	}
	if entries == nil {
		entries = []epub.SpineEntry{}
	}
	return entries, nil
}
