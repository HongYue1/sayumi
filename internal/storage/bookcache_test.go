package storage

import (
	"context"
	"encoding/json"
	"testing"

	"sayumi/internal/epub"
)

// bookWithSpine returns a sampleBook whose SpineJSON encodes the given spine,
// so cache tests can exercise real spine parsing (sampleBook defaults to "[]").
func bookWithSpine(t *testing.T, id, hash, path string, spine []epub.SpineEntry) BookRecord {
	t.Helper()
	raw, err := json.Marshal(spine)
	if err != nil {
		t.Fatalf("marshal spine: %v", err)
	}
	b := sampleBook(id, hash, path)
	b.SpineJSON = string(raw)
	b.ChapterCount = len(spine)
	return b
}

// TestBookCacheGetSpineLazy verifies that a spine is parsed on first access,
// returns the correct entries, is stable on the memoized second access, and
// that an unknown book reports ok == false.
func TestBookCacheGetSpineLazy(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	spine := []epub.SpineEntry{
		{Href: "ch1.xhtml", ID: "c1", MediaType: "application/xhtml+xml", Linear: true},
		{Href: "ch2.xhtml", ID: "c2", MediaType: "application/xhtml+xml", Linear: true},
	}
	mustInsertBook(t, db, bookWithSpine(t, "id1", "hash-a", "/lib/a.epub", spine))

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}

	if _, ok := cache.GetSpine(ctx, "missing"); ok {
		t.Error("GetSpine(missing) ok = true, want false")
	}

	got, ok := cache.GetSpine(ctx, "id1")
	if !ok {
		t.Fatal("GetSpine(id1) ok = false, want true")
	}
	if len(got) != len(spine) {
		t.Fatalf("spine len = %d, want %d", len(got), len(spine))
	}
	for i := range spine {
		if got[i] != spine[i] {
			t.Errorf("spine[%d] = %+v, want %+v", i, got[i], spine[i])
		}
	}

	again, ok := cache.GetSpine(ctx, "id1")
	if !ok {
		t.Fatal("second GetSpine(id1) ok = false, want true")
	}
	if len(again) != len(got) {
		t.Errorf("memoized spine len = %d, want %d", len(again), len(got))
	}
}

// TestBookCacheAddInvalidatesSpine verifies that re-adding a book replaces any
// previously memoized spine so the next GetSpine reflects the new SpineJSON.
func TestBookCacheAddInvalidatesSpine(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub")) // empty spine

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}

	// Memoize the initial (empty) spine.
	if s, ok := cache.GetSpine(ctx, "id1"); !ok || len(s) != 0 {
		t.Fatalf("initial spine = (%v, %d entries), want (true, 0)", ok, len(s))
	}

	newSpine := []epub.SpineEntry{
		{Href: "only.xhtml", ID: "c1", MediaType: "application/xhtml+xml", Linear: true},
	}
	cache.Add(bookWithSpine(t, "id1", "hash-a", "/lib/a.epub", newSpine))

	got, ok := cache.GetSpine(ctx, "id1")
	if !ok {
		t.Fatal("GetSpine after Add ok = false, want true")
	}
	if len(got) != 1 || got[0].Href != "only.xhtml" {
		t.Errorf("spine after Add = %+v, want one entry only.xhtml", got)
	}
}

// TestBookCacheRemoveDropsSpine verifies Remove clears both the record and any
// memoized spine.
func TestBookCacheRemoveDropsSpine(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}
	if _, ok := cache.GetSpine(ctx, "id1"); !ok {
		t.Fatal("precondition: GetSpine(id1) ok = false")
	}

	cache.Remove("id1")

	if _, ok := cache.Get("id1"); ok {
		t.Error("Get(id1) after Remove ok = true, want false")
	}
	if _, ok := cache.GetSpine(ctx, "id1"); ok {
		t.Error("GetSpine(id1) after Remove ok = true, want false")
	}
	if cache.Len() != 0 {
		t.Errorf("cache Len = %d, want 0", cache.Len())
	}
}
