package storage

import (
	"context"
	"errors"
	"testing"
)

// TestDeleteBookCascadesChildRows verifies that deleting a book removes its
// dependent rows. DeleteBookContext only deletes the books row (and records an
// ignored_files entry); the progress, bookmarks, and book_flairs rows are
// expected to disappear via ON DELETE CASCADE. This regression test fails if
// foreign-key enforcement is not actually enabled on the connection, which is
// exactly the bug the DSN fix in Open addresses.
func TestDeleteBookCascadesChildRows(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.SaveProgressContext(ctx, ProgressRecord{
		BookID: "id1", UserID: "default", Chapter: 1, Percent: 0.25,
	}); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	bookmarkID := GenerateBookmarkID()
	if err := db.InsertBookmarkContext(ctx, BookmarkRecord{
		ID: bookmarkID, BookID: "id1", UserID: "default", Chapter: 1, Percent: 0.25,
		Label: "Start",
	}); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	if err := db.InsertFlairContext(ctx, FlairRecord{
		ID: "flair_1", UserID: "default", Label: "Favorite", Color: "#ffffff",
	}); err != nil {
		t.Fatalf("insert flair: %v", err)
	}
	if err := db.SetBookFlairContext(ctx, "id1", "default", "flair_1"); err != nil {
		t.Fatalf("set book flair: %v", err)
	}

	if err := db.DeleteBookContext(ctx, "id1"); err != nil {
		t.Fatalf("delete book: %v", err)
	}

	if _, err := db.GetProgressContext(ctx, "id1", "default"); !errors.Is(err, ErrNotFound) {
		t.Errorf("progress row survived book deletion (cascade off?): err = %v", err)
	}

	marks, err := db.ListBookmarksContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("list bookmarks: %v", err)
	}
	if len(marks) != 0 {
		t.Errorf("bookmark rows survived book deletion (cascade off?): got %d", len(marks))
	}

	assigned, err := db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs: %v", err)
	}
	if _, ok := assigned["id1"]; ok {
		t.Error("book_flairs row survived book deletion (cascade off?)")
	}
}
