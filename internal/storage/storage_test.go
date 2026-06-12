package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// newTestDB opens a fresh, isolated profile database in a temp directory and
// registers cleanup. Each test gets its own database so tests never share
// state.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test db: %v", err)
		}
	})
	return db
}

// sampleBook returns a minimal valid BookRecord. SpineJSON/TocJSON default to
// "[]" so the row satisfies the NOT NULL columns and parses cleanly.
func sampleBook(id, hash, path string) BookRecord {
	return BookRecord{
		BookSummary: BookSummary{
			ID:           id,
			Title:        "Title " + id,
			Author:       "Author",
			FilePath:     path,
			FileHash:     hash,
			Direction:    "ltr",
			ChapterCount: 3,
		},
		SpineJSON: "[]",
		TocJSON:   "[]",
	}
}

func mustInsertBook(t *testing.T, db *DB, book BookRecord) string {
	t.Helper()
	id, err := db.InsertBookContext(context.Background(), book)
	if err != nil {
		t.Fatalf("insert book %q: %v", book.ID, err)
	}
	return id
}

func TestInsertBookIsIdempotentByHash(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first, err := db.InsertBookContext(ctx, sampleBook("id1", "hash-a", "/lib/a.epub"))
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if first != "id1" {
		t.Fatalf("first insert canonical id = %q, want id1", first)
	}

	// Same content hash, different proposed id and path: the OR IGNORE path
	// must suppress the insert and report the already-stored canonical id.
	second, err := db.InsertBookContext(ctx, sampleBook("id2", "hash-a", "/lib/b.epub"))
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if second != "id1" {
		t.Errorf("duplicate-hash insert canonical id = %q, want id1", second)
	}

	books, err := db.ListBooksContext(ctx)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 1 {
		t.Errorf("book count = %d, want 1", len(books))
	}
}

func TestInsertBookEmptyHashDoesNotCollide(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// The partial unique index excludes the empty-string hash, so two
	// not-yet-hashed books must both be inserted.
	mustInsertBook(t, db, sampleBook("a", "", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("b", "", "/lib/b.epub"))

	books, err := db.ListBooksContext(ctx)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	if len(books) != 2 {
		t.Errorf("book count = %d, want 2", len(books))
	}
}

func TestGetBookAndLookups(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	got, err := db.GetBookContext(ctx, "id1")
	if err != nil {
		t.Fatalf("get book: %v", err)
	}
	if got.Title != "Title id1" {
		t.Errorf("title = %q, want %q", got.Title, "Title id1")
	}

	byHash, err := db.GetBookByHashContext(ctx, "hash-a")
	if err != nil {
		t.Fatalf("get book by hash: %v", err)
	}
	if byHash.ID != "id1" {
		t.Errorf("by-hash id = %q, want id1", byHash.ID)
	}

	id, found, err := db.BookExistsByPathContext(ctx, "/lib/a.epub")
	if err != nil {
		t.Fatalf("exists by path: %v", err)
	}
	if !found || id != "id1" {
		t.Errorf("exists by path = (%q, %v), want (id1, true)", id, found)
	}

	_, found, err = db.BookExistsByPathContext(ctx, "/lib/missing.epub")
	if err != nil {
		t.Fatalf("exists by missing path: %v", err)
	}
	if found {
		t.Error("missing path reported as found")
	}

	if _, err := db.GetBookContext(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("get missing book err = %v, want sql.ErrNoRows", err)
	}
}

func TestUpdateBookFilePathAndCover(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.UpdateBookFilePathContext(ctx, "id1", "/lib/renamed.epub"); err != nil {
		t.Fatalf("update file path: %v", err)
	}
	if err := db.UpdateBookCoverContext(ctx, "id1", "covers/id1.jpg"); err != nil {
		t.Fatalf("update cover: %v", err)
	}

	got, err := db.GetBookContext(ctx, "id1")
	if err != nil {
		t.Fatalf("get book: %v", err)
	}
	if got.FilePath != "/lib/renamed.epub" {
		t.Errorf("file path = %q, want /lib/renamed.epub", got.FilePath)
	}
	if !got.HasCover || got.CoverPath != "covers/id1.jpg" {
		t.Errorf("cover = (%v, %q), want (true, covers/id1.jpg)", got.HasCover, got.CoverPath)
	}
}

func TestProgressUpsert(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if _, err := db.GetProgressContext(ctx, "id1", "default"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("progress before save err = %v, want sql.ErrNoRows", err)
	}

	if err := db.SaveProgressContext(ctx, ProgressRecord{
		BookID: "id1", UserID: "default", Chapter: 1, Percent: 0.25,
	}); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	// Saving again for the same (book, user) primary key must update in place.
	if err := db.SaveProgressContext(ctx, ProgressRecord{
		BookID: "id1", UserID: "default", Chapter: 4, Percent: 0.5,
	}); err != nil {
		t.Fatalf("resave progress: %v", err)
	}

	got, err := db.GetProgressContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("get progress: %v", err)
	}
	if got.Chapter != 4 || got.Percent != 0.5 {
		t.Errorf("progress = (ch %d, %.2f), want (ch 4, 0.50)", got.Chapter, got.Percent)
	}

	all, err := db.GetAllProgressContext(ctx, "default")
	if err != nil {
		t.Fatalf("get all progress: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("all progress count = %d, want 1", len(all))
	}
}

func TestSettingsUpsert(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.GetSettingsContext(ctx, "default"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("settings before save err = %v, want sql.ErrNoRows", err)
	}

	// font_roles is NOT NULL, so it must be a valid (possibly empty) string.
	rec := SettingsRecord{
		UserID:    "default",
		FontSize:  sql.NullInt64{Int64: 30, Valid: true},
		Theme:     sql.NullString{String: "rose-pine", Valid: true},
		FontRoles: sql.NullString{String: "", Valid: true},
	}
	if err := db.SaveSettingsContext(ctx, rec); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	got, err := db.GetSettingsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if !got.FontSize.Valid || got.FontSize.Int64 != 30 {
		t.Errorf("font size = %+v, want 30", got.FontSize)
	}

	rec.FontSize = sql.NullInt64{Int64: 18, Valid: true}
	if err := db.SaveSettingsContext(ctx, rec); err != nil {
		t.Fatalf("resave settings: %v", err)
	}
	got, err = db.GetSettingsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get settings after upsert: %v", err)
	}
	if got.FontSize.Int64 != 18 {
		t.Errorf("font size after upsert = %d, want 18", got.FontSize.Int64)
	}
}

func TestFlairDeleteClearsAssignments(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.InsertFlairContext(ctx, FlairRecord{
		ID: "flair_1", UserID: "default", Label: "Favorite", Color: "#ffffff",
	}); err != nil {
		t.Fatalf("insert flair: %v", err)
	}

	flairs, err := db.ListFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("list flairs: %v", err)
	}
	if len(flairs) != 1 {
		t.Fatalf("flair count = %d, want 1", len(flairs))
	}

	if err := db.SetBookFlairContext(ctx, "id1", "default", "flair_1"); err != nil {
		t.Fatalf("set book flair: %v", err)
	}
	assigned, err := db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs: %v", err)
	}
	if assigned["id1"] != "flair_1" {
		t.Errorf("assignment = %q, want flair_1", assigned["id1"])
	}

	if err := db.DeleteFlairContext(ctx, "flair_1", "default"); err != nil {
		t.Fatalf("delete flair: %v", err)
	}
	assigned, err = db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs after delete: %v", err)
	}
	if _, ok := assigned["id1"]; ok {
		t.Error("assignment to deleted flair was not cleared")
	}

	if err := db.DeleteFlairContext(ctx, "missing", "default"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("delete missing flair err = %v, want sql.ErrNoRows", err)
	}
}

func TestSetBookFlairClearsWhenEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.SetBookFlairContext(ctx, "id1", "default", "builtin:fav"); err != nil {
		t.Fatalf("set book flair: %v", err)
	}
	if err := db.SetBookFlairContext(ctx, "id1", "default", ""); err != nil {
		t.Fatalf("clear book flair: %v", err)
	}

	assigned, err := db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs: %v", err)
	}
	if _, ok := assigned["id1"]; ok {
		t.Error("empty flair id should have cleared the assignment")
	}
}

func TestBookmarksCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	id := GenerateBookmarkID()
	if err := db.InsertBookmarkContext(ctx, BookmarkRecord{
		ID: id, BookID: "id1", UserID: "default", Chapter: 1, Percent: 0.25,
		Label: "Start", Comment: "first note",
	}); err != nil {
		t.Fatalf("insert bookmark: %v", err)
	}

	list, err := db.ListBookmarksContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("list bookmarks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("bookmark count = %d, want 1", len(list))
	}

	if err := db.UpdateBookmarkContext(ctx, id, "default", "Updated", "second note"); err != nil {
		t.Fatalf("update bookmark: %v", err)
	}
	got, err := db.GetBookmarkContext(ctx, id, "default")
	if err != nil {
		t.Fatalf("get bookmark: %v", err)
	}
	if got.Label != "Updated" || got.Comment != "second note" {
		t.Errorf("bookmark = (%q, %q), want (Updated, second note)", got.Label, got.Comment)
	}

	if err := db.DeleteBookmarkContext(ctx, id, "default"); err != nil {
		t.Fatalf("delete bookmark: %v", err)
	}
	list, err = db.ListBookmarksContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("list bookmarks after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("bookmark count after delete = %d, want 0", len(list))
	}

	if err := db.UpdateBookmarkContext(ctx, "missing", "default", "x", "y"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("update missing bookmark err = %v, want sql.ErrNoRows", err)
	}
	if err := db.DeleteBookmarkContext(ctx, "missing", "default"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("delete missing bookmark err = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteBookRecordsIgnoredFile(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.DeleteBookContext(ctx, "id1"); err != nil {
		t.Fatalf("delete book: %v", err)
	}

	if _, err := db.GetBookContext(ctx, "id1"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("get deleted book err = %v, want sql.ErrNoRows", err)
	}

	ignored, err := db.IsFileIgnoredContext(ctx, "/lib/a.epub")
	if err != nil {
		t.Fatalf("is file ignored: %v", err)
	}
	if !ignored {
		t.Error("deleted book's file path was not recorded as ignored")
	}

	if err := db.RemoveIgnoredFileContext(ctx, "/lib/a.epub"); err != nil {
		t.Fatalf("remove ignored file: %v", err)
	}
	ignored, err = db.IsFileIgnoredContext(ctx, "/lib/a.epub")
	if err != nil {
		t.Fatalf("is file ignored after remove: %v", err)
	}
	if ignored {
		t.Error("ignored file entry was not removed")
	}
}
