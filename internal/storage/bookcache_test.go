package storage

import (
	"context"
	"encoding/json"
	"errors"
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

	if _, ok, err := cache.GetSpine(ctx, "missing"); err != nil {
		t.Fatalf("GetSpine(missing): %v", err)
	} else if ok {
		t.Error("GetSpine(missing) ok = true, want false")
	}

	got, ok, err := cache.GetSpine(ctx, "id1")
	if err != nil {
		t.Fatalf("GetSpine(id1): %v", err)
	}
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

	again, ok, err := cache.GetSpine(ctx, "id1")
	if err != nil {
		t.Fatalf("second GetSpine(id1): %v", err)
	}
	if !ok {
		t.Fatal("second GetSpine(id1) ok = false, want true")
	}
	if len(again) != len(got) {
		t.Errorf("memoized spine len = %d, want %d", len(again), len(got))
	}
}

func TestBookCacheAddInvalidatesSpine(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}

	if spine, ok, err := cache.GetSpine(ctx, "id1"); err != nil {
		t.Fatalf("initial GetSpine: %v", err)
	} else if !ok || len(spine) != 0 {
		t.Fatalf("initial spine = (%v, %d entries), want (true, 0)", ok, len(spine))
	}

	newSpine := []epub.SpineEntry{
		{Href: "only.xhtml", ID: "c1", MediaType: "application/xhtml+xml", Linear: true},
	}
	cache.Add(bookWithSpine(t, "id1", "hash-a", "/lib/a.epub", newSpine))

	got, ok, err := cache.GetSpine(ctx, "id1")
	if err != nil {
		t.Fatalf("GetSpine after Add: %v", err)
	}
	if !ok {
		t.Fatal("GetSpine after Add ok = false, want true")
	}
	if len(got) != 1 || got[0].Href != "only.xhtml" {
		t.Errorf("spine after Add = %+v, want one entry only.xhtml", got)
	}
}

func TestBookCacheRemoveDropsSpine(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}
	if _, ok, err := cache.GetSpine(ctx, "id1"); err != nil {
		t.Fatalf("precondition GetSpine: %v", err)
	} else if !ok {
		t.Fatal("precondition: GetSpine(id1) ok = false")
	}

	cache.Remove("id1")

	if _, ok := cache.Get("id1"); ok {
		t.Error("Get(id1) after Remove ok = true, want false")
	}
	if _, ok, err := cache.GetSpine(ctx, "id1"); err != nil {
		t.Fatalf("GetSpine after Remove: %v", err)
	} else if ok {
		t.Error("GetSpine(id1) after Remove ok = true, want false")
	}
	if cache.Len() != 0 {
		t.Errorf("cache Len = %d, want 0", cache.Len())
	}
}

func TestBookCacheGetSpineRejectsStaleConcurrentLoad(t *testing.T) {
	oldBook := sampleBook("id1", "hash-old", "/lib/a.epub")
	oldBook.SpineJSON = ""
	oldRaw, err := json.Marshal([]epub.SpineEntry{{Href: "old.xhtml"}})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	cache := &BookCache{
		loadBookContent: func(context.Context, string) (string, string, error) {
			close(started)
			<-release
			return string(oldRaw), "[]", nil
		},
		byID:        map[string]BookRecord{"id1": oldBook},
		order:       []string{"id1"},
		spines:      make(map[string][]epub.SpineEntry),
		generations: make(map[string]uint64),
	}

	type result struct {
		spine []epub.SpineEntry
		found bool
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		spine, found, err := cache.GetSpine(context.Background(), "id1")
		resultCh <- result{spine: spine, found: found, err: err}
	}()

	<-started
	cache.Add(bookWithSpine(t, "id1", "hash-new", "/lib/a.epub", []epub.SpineEntry{{Href: "new.xhtml"}}))
	close(release)

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("GetSpine: %v", got.err)
	}
	if !got.found || len(got.spine) != 1 || got.spine[0].Href != "new.xhtml" {
		t.Fatalf("GetSpine = (%+v, %v), want new.xhtml", got.spine, got.found)
	}
}

func TestBookCacheGetSpineReturnsParseError(t *testing.T) {
	book := sampleBook("id1", "hash-a", "/lib/a.epub")
	book.SpineJSON = "not-json"
	cache := &BookCache{
		byID:        map[string]BookRecord{"id1": book},
		spines:      make(map[string][]epub.SpineEntry),
		generations: make(map[string]uint64),
	}

	_, _, err := cache.GetSpine(context.Background(), "id1")
	if err == nil {
		t.Fatal("GetSpine error = nil, want parse error")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("GetSpine error = %v, want parse error", err)
	}
}
