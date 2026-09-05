package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestBookmarkScopeAndDefaults(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
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
	ctx := t.Context()
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

func TestBookmarkListScopeAndNullableRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	for _, id := range []string{"a", "b"} {
		mustInsertBook(t, db, sampleBook(id, "hash-"+id, "/lib/"+id+".epub"))
	}
	const createdAt = "2026-01-01 00:00:00"
	want := []BookmarkRecord{
		{ID: "first", BookID: "a", UserID: "reader", Chapter: 0, Percent: 0.9, CFI: sql.NullString{String: "epubcfi(/6/2)", Valid: true}, Label: "First", Comment: "First note", CreatedAt: createdAt},
		{ID: "second", BookID: "a", UserID: "reader", Chapter: 1, Percent: 0.1, Label: "Second", Comment: "Second note", CreatedAt: createdAt},
		{ID: "third", BookID: "a", UserID: "reader", Chapter: 1, Percent: 0.5, CFI: sql.NullString{Valid: true}, Label: "Third", Comment: "Third note", CreatedAt: createdAt},
	}
	for _, bookmark := range slices.Backward(want) {
		if err := db.InsertBookmarkContext(ctx, bookmark); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []BookmarkRecord{
		{ID: "other-user", BookID: "a", UserID: "other"},
		{ID: "other-book", BookID: "b", UserID: "reader"},
	} {
		if err := db.InsertBookmarkContext(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ListBookmarksContext(ctx, "a", "reader")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("bookmarks = (%+v, %v), want %+v", got, err, want)
	}
	got[0].Label = "caller mutation"
	again, err := db.ListBookmarksContext(ctx, "a", "reader")
	if err != nil || !slices.Equal(again, want) {
		t.Errorf("mutating returned rows changed stored bookmarks: %+v, %v", again, err)
	}
	empty, err := db.ListBookmarksContext(ctx, "a", "missing")
	if err != nil || len(empty) != 0 {
		t.Errorf("missing user's bookmarks = (%+v, %v), want empty", empty, err)
	}
}

func TestBookmarkCancellationDoesNotWrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("book", "hash", "/lib/book.epub"))
	original := BookmarkRecord{ID: "mark", BookID: "book", UserID: "reader", Label: "Original"}
	if err := db.InsertBookmarkContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetBookmarkContext(ctx, original.ID, original.UserID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "get", run: func() error {
			_, err := db.GetBookmarkContext(canceled, "mark", "reader")
			return err
		}},
		{name: "list", run: func() error {
			_, err := db.ListBookmarksContext(canceled, "book", "reader")
			return err
		}},
		{name: "insert", run: func() error {
			return db.InsertBookmarkContext(canceled, BookmarkRecord{ID: "new", BookID: "book", UserID: "reader"})
		}},
		{name: "update", run: func() error {
			return db.UpdateBookmarkContext(canceled, "mark", "book", "reader", "Changed", "Changed")
		}},
		{name: "delete", run: func() error {
			return db.DeleteBookmarkContext(canceled, "mark", "book", "reader")
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled operation returned %v", err)
			}
		})
	}
	after, err := db.GetBookmarkContext(ctx, "mark", "reader")
	if err != nil || after != before {
		t.Errorf("canceled writes changed bookmark: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := db.GetBookmarkContext(ctx, "new", "reader"); !errors.Is(err, ErrNotFound) {
		t.Errorf("canceled insert created bookmark: %v", err)
	}
}
