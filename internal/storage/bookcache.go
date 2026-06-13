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

	"sayumi/internal/epub"
)

// BookCache mirrors the books table in memory for fast lookups.
type BookCache struct {
	// db backs the lazy spine load. The cache is built from book *summaries*
	// only (ListBookSummariesContext), so the heavy spine_json / toc_json are
	// not held in memory; GetSpine fetches a book's spine JSON from db on first
	// access. A book inserted via Add carries its SpineJSON inline, so that path
	// parses directly without touching db.
	db *DB

	mu    sync.RWMutex
	byID  map[string]BookRecord
	order []string

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
		db:     db,
		byID:   make(map[string]BookRecord, len(summaries)),
		order:  make([]string, 0, len(summaries)),
		spines: make(map[string][]epub.SpineEntry, len(summaries)),
	}
	for _, s := range summaries {
		// SpineJSON / TocJSON are intentionally left empty here: the list query
		// no longer reads them. GetSpine backfills the spine from db on demand;
		// the book-detail / toc handlers fetch the JSON via GetBookContentContext.
		c.byID[s.ID] = BookRecord{BookSummary: s}
		c.order = append(c.order, s.ID)
	}

	// Spines are parsed lazily and the heavy spine/toc columns are no longer
	// read at construction (see the db field doc). Logging the summary-list
	// duration makes cold-start attribution explicit: the profile-open
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

func (c *BookCache) GetSpine(ctx context.Context, id string) ([]epub.SpineEntry, bool) {
	// Fast path: the book exists and its spine has already been parsed.
	c.mu.RLock()
	if s, ok := c.spines[id]; ok {
		c.mu.RUnlock()
		return s, true
	}
	b, ok := c.byID[id]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}

	// Slow path. The spine JSON usually isn't in memory (the cache is built from
	// summaries), so load it from the database on demand; a book added at runtime
	// carries its SpineJSON inline and skips the query. Parse outside the lock
	// (JSON unmarshal of a large spine can be costly and must not block concurrent
	// readers), then memoize under the write lock.
	spineJSON := b.SpineJSON
	if spineJSON == "" {
		loaded, _, err := c.db.GetBookContentContext(ctx, id)
		if err != nil {
			slog.Warn("load spine failed", "book", id, "err", err)
			return nil, false
		}
		spineJSON = loaded
	}
	parsed := parseSpine(id, spineJSON)

	// A concurrent caller may have populated the entry meanwhile, so re-check
	// before storing to keep a single shared slice. Re-check byID too in case the
	// book was removed during the unlocked load+parse.
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.spines[id]; ok {
		return s, true
	}
	if _, stillExists := c.byID[id]; !stillExists {
		return nil, false
	}
	c.spines[id] = parsed
	return parsed, true
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

func parseSpine(bookID, spineJSON string) []epub.SpineEntry {
	var entries []epub.SpineEntry
	if err := json.Unmarshal([]byte(spineJSON), &entries); err != nil {
		slog.Warn("parse spine JSON failed", "book", bookID, "err", err)
		return []epub.SpineEntry{}
	}
	return entries
}
