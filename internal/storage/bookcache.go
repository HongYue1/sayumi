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

// BookCache mirrors the books table in memory for fast lookups.
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

	// spines memoizes parsed spine entries per book ID. It is populated lazily
	// on the first GetSpine for a book rather than eagerly at construction:
	// unmarshalling every book's spine JSON up front dominated cold-start
	// profile-open time, yet a spine is only needed when a book is actually
	// opened for reading -- never for the library list view. Entries are
	// invalidated in Add and Remove so a re-imported book re-parses from its new
	// SpineJSON.
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
		// SpineJSON / TocJSON are intentionally left empty here: the list query
		// no longer reads them. GetSpine backfills the spine from db on demand;
		// the book-detail / toc handlers fetch the JSON via GetBookContentContext.
		c.byID[s.ID] = BookRecord{BookSummary: s}
		c.order = append(c.order, s.ID)
	}

	// Spines are parsed lazily and the heavy spine/toc columns are no longer
	// read at construction (see the loadBookContent field doc). Logging the
	// summary-list duration makes cold-start attribution explicit: the profile-open
	// "book_cache" timing should collapse to roughly this value.
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
	spineJSON := book.SpineJSON
	if spineJSON == "" {
		loaded, _, err := c.loadBookContent(ctx, id)
		if err != nil {
			return spineLoadResult{}, fmt.Errorf("load spine for book %s: %w", id, err)
		}
		spineJSON = loaded
	}

	parsed, err := parseSpine(id, spineJSON)
	if err != nil {
		return spineLoadResult{}, err
	}

	// Add or Remove may have changed this book while the load ran unlocked. A
	// generation mismatch discards the stale result and makes GetSpine retry
	// against the newest record instead of repopulating an invalidated entry.
	c.mu.Lock()
	defer c.mu.Unlock()
	if spine, ok := c.spines[id]; ok {
		return spineLoadResult{spine: spine, found: true}, nil
	}
	current, stillExists := c.byID[id]
	if !stillExists {
		return spineLoadResult{}, nil
	}
	if c.generations[id] != generation || current != book {
		return spineLoadResult{retry: true}, nil
	}
	c.spines[id] = parsed
	return spineLoadResult{spine: parsed, found: true}, nil
}

func (c *BookCache) List() []BookRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]BookRecord, 0, len(c.order))
	for _, id := range c.order {
		if b, ok := c.byID[id]; ok {
			out = append(out, b)
		}
	}
	return out
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

// asciiToLower folds only ASCII A–Z to lower case, matching SQLite's NOCASE
// collation. Unlike strings.ToLower it does not Unicode-fold characters outside
// the ASCII range, keeping insertion order consistent with the ORDER BY title
// COLLATE NOCASE results from the database.
//
// The implementation avoids allocating a []byte unless the input actually
// contains an uppercase ASCII letter, which is the common case for book titles
// that are already stored in their display form.
func asciiToLower(s string) string {
	// Fast path: scan for the first uppercase ASCII letter.
	upper := -1
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			upper = i
			break
		}
	}
	if upper == -1 {
		return s // no allocation needed
	}
	b := []byte(s)
	for i := upper; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

func (c *BookCache) insertPosition(titleLower string) int {
	pos, _ := slices.BinarySearchFunc(c.order, titleLower, func(id, target string) int {
		existing, ok := c.byID[id]
		if !ok {
			// Orphaned ID (invariant violation): push to end so it never
			// blocks a correct insertion position.
			return 1
		}
		return strings.Compare(asciiToLower(existing.Title), target)
	})
	return pos
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
	// position, so skip the O(N) order-slice delete + binary-search reinsert
	// (each comparison re-lowercases a title) and just refresh the record in
	// place. This covers duplicate re-uploads and cache re-warms where only
	// non-title fields (e.g. cover state) change.
	if existing, exists := c.byID[b.ID]; exists && existing.Title == b.Title {
		c.byID[b.ID] = b
		delete(c.spines, b.ID)
		return
	}

	titleLower := asciiToLower(b.Title)

	if _, exists := c.byID[b.ID]; exists {
		// Remove the old position before re-inserting so the sort position
		// reflects any title change.
		c.order = slices.DeleteFunc(c.order, func(s string) bool { return s == b.ID })
	}

	pos := c.insertPosition(titleLower)
	c.order = slices.Insert(c.order, pos, b.ID)
	c.byID[b.ID] = b
	// Invalidate any memoized spine; GetSpine re-parses lazily from the
	// (possibly changed) SpineJSON on next access.
	delete(c.spines, b.ID)
}

func (c *BookCache) Remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextGeneration++
	delete(c.generations, id)
	delete(c.byID, id)
	delete(c.spines, id)
	c.order = slices.DeleteFunc(c.order, func(s string) bool {
		return s == id
	})
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
