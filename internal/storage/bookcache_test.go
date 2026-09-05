package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	ctx := t.Context()

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
	ctx := t.Context()
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
	ctx := t.Context()
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
		spine, found, err := cache.GetSpine(t.Context(), "id1")
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

	_, _, err := cache.GetSpine(t.Context(), "id1")
	if err == nil {
		t.Fatal("GetSpine error = nil, want parse error")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("GetSpine error = %v, want parse error", err)
	}
}

// TestAddPlacesDuplicateTitlesWhereARestartWould pins Add's insert slot to the
// list query's ORDER BY title COLLATE NOCASE ASC, id ASC. SQL breaks title ties
// by ID, and that query seeds order on every rebuild, so a live Add must land
// where a restart would put it. The API sorts titles with a stable sort, which
// means cache order is exactly what the user sees for identical titles.
func TestAddPlacesDuplicateTitlesWhereARestartWould(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()

	dup := func(id, hash, path string) BookRecord {
		b := sampleBook(id, hash, path)
		b.Title = "Dune"
		return b
	}

	mustInsertBook(t, db, dup("id2", "hash-2", "/lib/b.epub"))
	mustInsertBook(t, db, dup("id4", "hash-4", "/lib/d.epub"))

	cache, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("new book cache: %v", err)
	}

	// id9 sorts after both stored IDs, so it belongs at the end of the run of
	// equal titles, not at its front.
	added := dup("id9", "hash-9", "/lib/i.epub")
	mustInsertBook(t, db, added)
	cache.Add(added)

	rebuilt, err := NewBookCache(ctx, db)
	if err != nil {
		t.Fatalf("rebuild book cache: %v", err)
	}

	live := cache.ListSummaries()
	fresh := rebuilt.ListSummaries()
	if len(live) != len(fresh) {
		t.Fatalf("live cache holds %d books, rebuilt holds %d", len(live), len(fresh))
	}
	for i := range live {
		if live[i].ID != fresh[i].ID {
			t.Fatalf("order[%d] = %s after Add, but %s after a restart", i, live[i].ID, fresh[i].ID)
		}
	}
}

func newMemoryBookCache(books ...BookRecord) *BookCache {
	cache := &BookCache{
		byID:        make(map[string]BookRecord, len(books)),
		order:       make([]string, 0, len(books)),
		spines:      make(map[string][]epub.SpineEntry),
		generations: make(map[string]uint64),
	}
	for _, book := range books {
		cache.Add(book)
	}
	return cache
}

// SQLite itself is the ordering oracle, including its embedded-NUL behavior.
// Comparing against a second Go fold implementation would share its blind spots.
func TestCompareASCIIFoldMatchesSQLite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	words := []string{
		"", "a", "A", "ab", "aB", "Ab", "AB", "abc", "ABD",
		"Dune", "dune", "dunes", "Zoo", "[", "{", "@", "_",
		"Ångström", "ångström", "Æon", "æon", "naïve", "NAÏVE",
		"book 2", "Book 10", "book", "book\x00", "book\x00a", "BOOK\x00z",
		"book\x00long", "\x00", "\x00a", "\x00z", "\x00aa",
	}
	for _, a := range words {
		for _, b := range words {
			var want int
			err := db.QueryRowContext(t.Context(), `
				SELECT CASE WHEN ? COLLATE NOCASE < ? THEN -1
				            WHEN ? COLLATE NOCASE > ? THEN 1 ELSE 0 END
			`, a, b, a, b).Scan(&want)
			if err != nil {
				t.Fatal(err)
			}
			if got := compareASCIIFold(a, b); got != want {
				t.Errorf("compareASCIIFold(%q, %q) = %d, SQLite = %d", a, b, got, want)
			}
		}
	}
}

func TestBookCacheMutationsMatchSQLiteOrder(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cache, err := NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	checkOrder := func() {
		t.Helper()
		want, err := db.ListBookSummariesContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got := cache.ListSummaries(); !slices.Equal(idsOf(got), idsOf(want)) {
			t.Fatalf("live order = %v, database order = %v", idsOf(got), idsOf(want))
		}
	}

	for i, title := range []string{"Zulu", "book\x00z", "alpha", "BOOK\x00a", "ALPHA", "Ångström", "ångström"} {
		id := fmt.Sprintf("id%d", i)
		book := sampleBook(id, id, "/lib/"+id+".epub")
		book.Title = title
		mustInsertBook(t, db, book)
		cache.Add(book)
		checkOrder()
	}
	for _, title := range []string{"Aardvark", "zzzz", "alpha", "book\x00x"} {
		if err := db.UpdateBookMetadataAndFileContext(t.Context(), "id0", title, "Author", "id0", 1); err != nil {
			t.Fatal(err)
		}
		book, err := db.GetBookContext(t.Context(), "id0")
		if err != nil {
			t.Fatal(err)
		}
		cache.Add(book)
		checkOrder()
	}

	snapshot := cache.ListSummaries()
	original := snapshot[0]
	snapshot[0].Title = "caller mutation"
	if got, _ := cache.Get(original.ID); got.Title != original.Title {
		t.Fatal("ListSummaries returned mutable cache-owned state")
	}
	for _, id := range []string{"missing", "id0", "id6", "id2", "id1", "id3", "id4", "id5"} {
		if id != "missing" {
			if err := db.DeleteBookContext(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		}
		cache.Remove(id)
		cache.Remove(id)
		checkOrder()
	}
	if cache.Len() != 0 {
		t.Fatalf("cache length = %d, want 0", cache.Len())
	}
}
