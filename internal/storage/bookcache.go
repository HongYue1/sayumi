package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"sayumi/internal/epub"
)

// BookCache mirrors the books table in memory for fast lookups.
type BookCache struct {
	mu    sync.RWMutex
	byID  map[string]BookRecord
	order []string

	spines map[string][]epub.SpineEntry
}

func NewBookCache(ctx context.Context, db *DB) (*BookCache, error) {
	books, err := db.ListBooksContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("populate book cache: %w", err)
	}

	c := &BookCache{
		byID:   make(map[string]BookRecord, len(books)),
		order:  make([]string, 0, len(books)),
		spines: make(map[string][]epub.SpineEntry, len(books)),
	}
	for _, b := range books {
		c.byID[b.ID] = b
		c.order = append(c.order, b.ID)
		c.spines[b.ID] = parseSpine(b.ID, b.SpineJSON)
	}
	return c, nil
}

func (c *BookCache) Get(id string) (BookRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.byID[id]
	return b, ok
}

func (c *BookCache) GetSpine(id string) ([]epub.SpineEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.spines[id]
	return s, ok
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
	for i := 0; i < len(s); i++ {
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
	c.spines[b.ID] = parseSpine(b.ID, b.SpineJSON)
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
