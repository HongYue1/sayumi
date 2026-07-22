package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestListBookSummariesStableTitleTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	// Same title, different ids: order must be title then id, not insertion order.
	mustInsertBook(t, db, sampleBook("z-book", "hash-z", "/lib/z.epub"))
	mustInsertBook(t, db, sampleBook("a-book", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("m-book", "hash-m", "/lib/m.epub"))

	// Force identical titles after insert so the id tie-breaker is exercised.
	// Use distinct hashes so the partial unique index is not involved.
	for _, id := range []string{"z-book", "a-book", "m-book"} {
		hash := "hash-title-" + id
		if err := db.UpdateBookMetadataAndFileContext(ctx, id, "Same Title", "Author", hash, 10); err != nil {
			t.Fatalf("set title for %s: %v", id, err)
		}
	}

	got, err := db.ListBookSummariesContext(ctx)
	if err != nil {
		t.Fatalf("list book summaries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("book count = %d, want 3", len(got))
	}
	wantIDs := []string{"a-book", "m-book", "z-book"}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, got[i].ID, id, idsOf(got))
		}
		if got[i].Title != "Same Title" {
			t.Errorf("title[%d] = %q, want Same Title", i, got[i].Title)
		}
	}
}

func idsOf(books []BookSummary) []string {
	out := make([]string, len(books))
	for i, b := range books {
		out[i] = b.ID
	}
	return out
}

func TestBookUpdateMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.UpdateBookFilePathContext(ctx, "missing", "/lib/x.epub"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookFilePathContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookCoverContext(ctx, "missing", "covers/x.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookCoverContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookMetadataAndFileContext(ctx, "missing", "t", "a", "h", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookMetadataAndFileContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookCoverAndFileContext(ctx, "missing", "covers/x.jpg", "h", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookCoverAndFileContext err = %v, want ErrNotFound", err)
	}
	if err := db.MarkCoverCheckedContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkCoverCheckedContext err = %v, want ErrNotFound", err)
	}
}

func TestUpdateBookMetadataFileHashConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("id2", "hash-b", "/lib/b.epub"))

	// Preflight path: assertFileHashFree should reject adopting another book's hash.
	err := db.UpdateBookMetadataAndFileContext(ctx, "id2", "Title id2", "Author", "hash-a", 99)
	if !errors.Is(err, ErrFileHashConflict) {
		t.Fatalf("metadata hash conflict err = %v, want ErrFileHashConflict", err)
	}
	err = db.UpdateBookCoverAndFileContext(ctx, "id2", "covers/id2.jpg", "hash-a", 99)
	if !errors.Is(err, ErrFileHashConflict) {
		t.Fatalf("cover hash conflict err = %v, want ErrFileHashConflict", err)
	}

	// Unchanged row still has its original hash.
	got, err := db.GetBookContext(ctx, "id2")
	if err != nil {
		t.Fatalf("get book: %v", err)
	}
	if got.FileHash != "hash-b" {
		t.Errorf("file hash = %q, want hash-b", got.FileHash)
	}
}

func TestGetBookContentAndSummary(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	book := sampleBook("id1", "hash-a", "/lib/a.epub")
	book.SpineJSON = `[{"id":"c1"}]`
	book.TocJSON = `[{"label":"One"}]`
	mustInsertBook(t, db, book)

	spine, toc, err := db.GetBookContentContext(ctx, "id1")
	if err != nil {
		t.Fatalf("get book content: %v", err)
	}
	if spine != book.SpineJSON || toc != book.TocJSON {
		t.Errorf("content = (%q, %q), want (%q, %q)", spine, toc, book.SpineJSON, book.TocJSON)
	}
	if _, _, err := db.GetBookContentContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing content err = %v, want ErrNotFound", err)
	}

	summary, found, err := db.GetBookSummaryContext(ctx, "id1")
	if err != nil || !found {
		t.Fatalf("get summary: found=%v err=%v", found, err)
	}
	if summary.Title != "Title id1" || summary.FileHash != "hash-a" {
		t.Errorf("summary = %+v", summary)
	}
	_, found, err = db.GetBookSummaryContext(ctx, "missing")
	if err != nil || found {
		t.Errorf("missing summary found=%v err=%v, want false/nil", found, err)
	}
}

func TestInsertBookConcurrentSameHash(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	const workers = 8
	var (
		wg   sync.WaitGroup
		ids  = make([]string, workers)
		errs = make([]error, workers)
	)
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			book := sampleBook(fmt.Sprintf("id-%d", i), "hash-shared", fmt.Sprintf("/lib/%d.epub", i))
			ids[i], errs[i] = db.InsertBookContext(ctx, book)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	canonical := ids[0]
	for i, id := range ids {
		if id != canonical {
			t.Fatalf("worker %d canonical id = %q, want %q (all=%v)", i, id, canonical, ids)
		}
	}
	books, err := db.ListBookSummariesContext(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("book count = %d, want 1", len(books))
	}
}
