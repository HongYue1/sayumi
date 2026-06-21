package storage

import (
	"context"
	"testing"
)

// insertCoverTestBook seeds a minimal book row. A freshly imported book has no
// resolved cover yet, mirroring what the scanner records before extraction.
func insertCoverTestBook(t *testing.T, db *DB, id, path, hash string) {
	t.Helper()
	if _, err := db.InsertBookContext(context.Background(), BookRecord{
		BookSummary: BookSummary{ID: id, Title: id, FilePath: path, FileHash: hash},
		SpineJSON:   "[]",
		TocJSON:     "[]",
	}); err != nil {
		t.Fatalf("seed book %s: %v", id, err)
	}
}

func listMissingCoverIDs(t *testing.T, db *DB) map[string]bool {
	t.Helper()
	rows, err := db.ListBooksMissingCoversContext(context.Background())
	if err != nil {
		t.Fatalf("ListBooksMissingCoversContext: %v", err)
	}
	ids := make(map[string]bool, len(rows))
	for _, bp := range rows {
		ids[bp.ID] = true
	}
	return ids
}

// TestCoverCheckedBackfillQuery verifies the cover backfill's driving query only
// returns books whose cover is still unresolved, and that both ways of resolving
// a cover -- recording one (UpdateBookCoverContext) and marking a definitive
// no-cover result (MarkCoverCheckedContext) -- remove a book from it. This is the
// invariant that stops the post-walk backfill from re-parsing a cover-less or
// skipped book on every scan.
func TestCoverCheckedBackfillQuery(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	insertCoverTestBook(t, db, "book_with", "/lib/with.epub", "hash-with")
	insertCoverTestBook(t, db, "book_skip", "/lib/skip.epub", "hash-skip")
	insertCoverTestBook(t, db, "book_pending", "/lib/pending.epub", "hash-pending")

	// Freshly imported books are unresolved and must all be listed.
	missing := listMissingCoverIDs(t, db)
	for _, id := range []string{"book_with", "book_skip", "book_pending"} {
		if !missing[id] {
			t.Fatalf("expected %s to be listed as missing a cover", id)
		}
	}

	// Recording a real cover resolves the book.
	if err := db.UpdateBookCoverContext(ctx, "book_with", ".sayumi/covers/book_with.jpg"); err != nil {
		t.Fatalf("UpdateBookCoverContext: %v", err)
	}
	// Marking a definitive no-cover result (oversized/no cover image) also resolves it.
	if err := db.MarkCoverCheckedContext(ctx, "book_skip"); err != nil {
		t.Fatalf("MarkCoverCheckedContext: %v", err)
	}

	missing = listMissingCoverIDs(t, db)
	if missing["book_with"] {
		t.Errorf("book_with has a recorded cover but is still listed as missing")
	}
	if missing["book_skip"] {
		t.Errorf("book_skip was marked cover-checked but is still listed as missing")
	}
	if !missing["book_pending"] {
		t.Errorf("book_pending is still unresolved and must remain listed for retry")
	}

	// UpdateBookCoverContext must also flip has_cover so the book renders a cover.
	summary, found, err := db.GetBookSummaryContext(ctx, "book_with")
	if err != nil {
		t.Fatalf("GetBookSummaryContext: %v", err)
	}
	if !found {
		t.Fatalf("book_with not found")
	}
	if !summary.HasCover || summary.CoverPath == "" {
		t.Errorf("book_with: HasCover=%v CoverPath=%q, want true and non-empty", summary.HasCover, summary.CoverPath)
	}
}
