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

func listMissingCovers(t *testing.T, db *DB) []BookPath {
	t.Helper()
	rows, err := db.ListBooksMissingCoversContext(context.Background())
	if err != nil {
		t.Fatalf("ListBooksMissingCoversContext: %v", err)
	}
	return rows
}

func missingCoverIDs(rows []BookPath) map[string]bool {
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

	// Freshly imported books are unresolved and must all be listed with paths
	// the scanner backfill needs to reopen each EPUB.
	missingRows := listMissingCovers(t, db)
	wantPaths := map[string]string{
		"book_with":    "/lib/with.epub",
		"book_skip":    "/lib/skip.epub",
		"book_pending": "/lib/pending.epub",
	}
	if len(missingRows) != len(wantPaths) {
		t.Fatalf("missing cover count = %d, want %d", len(missingRows), len(wantPaths))
	}
	for _, bp := range missingRows {
		wantPath, ok := wantPaths[bp.ID]
		if !ok {
			t.Fatalf("unexpected missing-cover id %q", bp.ID)
		}
		if bp.FilePath != wantPath {
			t.Errorf("missing-cover path for %s = %q, want %q", bp.ID, bp.FilePath, wantPath)
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

	missingRows = listMissingCovers(t, db)
	missing := missingCoverIDs(missingRows)
	if len(missingRows) != 1 {
		t.Fatalf("missing cover count after resolve = %d, want 1 (pending only); ids=%v", len(missingRows), missing)
	}
	if missing["book_with"] {
		t.Errorf("book_with has a recorded cover but is still listed as missing")
	}
	if missing["book_skip"] {
		t.Errorf("book_skip was marked cover-checked but is still listed as missing")
	}
	if !missing["book_pending"] {
		t.Errorf("book_pending is still unresolved and must remain listed for retry")
	}
	if missingRows[0].FilePath != "/lib/pending.epub" {
		t.Errorf("pending path = %q, want /lib/pending.epub", missingRows[0].FilePath)
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

	// MarkCoverChecked means "do not retry", not "has a cover". The UI must not
	// treat a checked-but-coverless book as having artwork.
	skip, found, err := db.GetBookSummaryContext(ctx, "book_skip")
	if err != nil {
		t.Fatalf("GetBookSummaryContext book_skip: %v", err)
	}
	if !found {
		t.Fatalf("book_skip not found")
	}
	if skip.HasCover || skip.CoverPath != "" {
		t.Errorf("book_skip: HasCover=%v CoverPath=%q, want false and empty after mark-checked", skip.HasCover, skip.CoverPath)
	}
}
