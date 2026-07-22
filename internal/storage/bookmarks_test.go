package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestBookmarkScopeAndDefaults(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("book-a", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("book-b", "hash-b", "/lib/b.epub"))

	const bookmarkID = "bookmark-a"
	if err := db.InsertBookmarkContext(ctx, BookmarkRecord{
		ID:      bookmarkID,
		BookID:  "book-a",
		UserID:  "user-a",
		Chapter: 2,
		Percent: 0.5,
		CFI:     sql.NullString{String: "epubcfi(/6/4)", Valid: true},
		Label:   "Original",
		Comment: "Original note",
	}); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	got, err := db.GetBookmarkContext(ctx, bookmarkID, "user-a")
	if err != nil {
		t.Fatalf("get bookmark: %v", err)
	}
	if !got.CFI.Valid || got.CFI.String != "epubcfi(/6/4)" {
		t.Errorf("CFI = %+v, want valid epubcfi(/6/4)", got.CFI)
	}
	if got.CreatedAt == "" {
		t.Fatal("CreatedAt is empty, want generated timestamp")
	}
	if _, err := time.Parse(time.DateTime, got.CreatedAt); err != nil {
		t.Errorf("CreatedAt = %q: %v", got.CreatedAt, err)
	}

	if _, err := db.GetBookmarkContext(ctx, bookmarkID, "user-b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get as wrong user err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetBookmarkContext(ctx, "missing", "user-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing bookmark err = %v, want ErrNotFound", err)
	}

	if err := db.UpdateBookmarkContext(
		ctx, bookmarkID, "book-b", "user-a", "Wrong book", "Wrong book note",
	); !errors.Is(err, ErrNotFound) {
		t.Errorf("update through wrong book err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookmarkContext(
		ctx, bookmarkID, "book-a", "user-b", "Wrong user", "Wrong user note",
	); !errors.Is(err, ErrNotFound) {
		t.Errorf("update as wrong user err = %v, want ErrNotFound", err)
	}

	if err := db.UpdateBookmarkContext(
		ctx, bookmarkID, "book-a", "user-a", "Updated", "Updated note",
	); err != nil {
		t.Fatalf("update bookmark: %v", err)
	}
	updated, err := db.GetBookmarkContext(ctx, bookmarkID, "user-a")
	if err != nil {
		t.Fatalf("get updated bookmark: %v", err)
	}
	if updated.Label != "Updated" || updated.Comment != "Updated note" {
		t.Errorf("updated bookmark = (%q, %q), want (Updated, Updated note)", updated.Label, updated.Comment)
	}
	if updated.BookID != "book-a" || updated.UserID != "user-a" || updated.CreatedAt != got.CreatedAt {
		t.Errorf("update changed immutable fields: before=%+v after=%+v", got, updated)
	}

	if err := db.DeleteBookmarkContext(ctx, bookmarkID, "book-b", "user-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete through wrong book err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteBookmarkContext(ctx, bookmarkID, "book-a", "user-b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete as wrong user err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetBookmarkContext(ctx, bookmarkID, "user-a"); err != nil {
		t.Errorf("bookmark missing after rejected mutations: %v", err)
	}
}

func TestListBookmarksStableTies(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("book-a", "hash-a", "/lib/a.epub"))

	bookmarks := []BookmarkRecord{
		{ID: "bookmark-b", BookID: "book-a", UserID: "default", Chapter: 1, Percent: 0.5, CreatedAt: "2026-01-02 00:00:00"},
		{ID: "bookmark-c", BookID: "book-a", UserID: "default", Chapter: 1, Percent: 0.5, CreatedAt: "2026-01-01 00:00:00"},
		{ID: "bookmark-a", BookID: "book-a", UserID: "default", Chapter: 1, Percent: 0.5, CreatedAt: "2026-01-02 00:00:00"},
	}
	for _, bookmark := range bookmarks {
		if err := db.InsertBookmarkContext(ctx, bookmark); err != nil {
			t.Fatalf("insert %s: %v", bookmark.ID, err)
		}
	}

	got, err := db.ListBookmarksContext(ctx, "book-a", "default")
	if err != nil {
		t.Fatalf("list bookmarks: %v", err)
	}
	want := []string{"bookmark-c", "bookmark-a", "bookmark-b"}
	if len(got) != len(want) {
		t.Fatalf("bookmark count = %d, want %d", len(got), len(want))
	}
	for i, bookmark := range got {
		if bookmark.ID != want[i] {
			t.Errorf("bookmark[%d] = %q, want %q", i, bookmark.ID, want[i])
		}
	}
}
