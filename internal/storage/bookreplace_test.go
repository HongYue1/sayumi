package storage

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestChapterRemapApply(t *testing.T) {
	t.Parallel()
	anchor := sql.NullString{String: "cfi:2/1:5", Valid: true}
	remap := ChapterRemap{
		Moves: []ChapterMove{
			{Index: 1, KeepAnchor: true}, // moved
			{Index: -1},                  // removed
			{Index: 7, KeepAnchor: true}, // corrupt: past NewCount
		},
		NewCount: 3,
	}

	tests := []struct {
		name string
		in   Position
		want Position
	}{
		{"matched keeps percent and anchor", Position{0, 0.4, anchor}, Position{1, 0.4, anchor}},
		{"unmatched keeps index, drops anchor", Position{1, 0.6, anchor}, Position{1, 0.6, sql.NullString{}}},
		{"out-of-range move falls back", Position{2, 0.3, anchor}, Position{2, 0.3, sql.NullString{}}},
		{"beyond remap past new end parks at end", Position{9, 0.2, anchor}, Position{2, 1, sql.NullString{}}},
		{"negative chapter resets", Position{-1, 0.5, anchor}, Position{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := remap.Apply(tc.in); got != tc.want {
				t.Fatalf("Apply(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}

	if got := (ChapterRemap{}).Apply(Position{Chapter: 3, Percent: 0.5, CFI: anchor}); got != (Position{}) {
		t.Fatalf("empty replacement = %+v, want zero position", got)
	}
}

func TestStampAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 3, 4, 5, 600, time.UTC)
	tests := []struct{ prev, want string }{
		{"2026-01-02 03:04:04", "2026-01-02 03:04:05"}, // clock already past
		{"2026-01-02 03:04:05", "2026-01-02 03:04:06"}, // same second: strictly later
		{"2999-01-01 00:00:00", "2999-01-01 00:00:01"}, // prev ahead of the clock
		{"", "2026-01-02 03:04:05"},                    // no baseline
	}
	for _, tc := range tests {
		if got := StampAfter(tc.prev, now); got != tc.want {
			t.Errorf("StampAfter(%q) = %q, want %q", tc.prev, got, tc.want)
		}
	}
}

func TestReplaceBookFileRemapsPositions(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("a", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("b", "hash-b", "/lib/b.epub"))

	anchor := sql.NullString{String: "epubcfi(/6/4)", Valid: true}
	for _, p := range []ProgressRecord{
		{BookID: "a", UserID: "mover", Chapter: 1, Percent: 0.5, CFI: anchor},
		{BookID: "a", UserID: "stayer", Chapter: 0, Percent: 0.2, CFI: anchor},
	} {
		if err := db.SaveProgressContext(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// A baseline ahead of the clock pins StampAfter's strictly-later branch.
	const future = "2999-01-01 00:00:00"
	if _, err := db.ExecContext(ctx, "UPDATE progress SET updated_at = ? WHERE book_id = 'a'", future); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertBookmarkContext(ctx, BookmarkRecord{
		ID: "bm", BookID: "a", UserID: "mover", Chapter: 2, Percent: 0.7, CFI: anchor, Label: "x",
	}); err != nil {
		t.Fatal(err)
	}

	rep := BookFileReplacement{
		Direction: "ltr",
		FileHash:  "hash-b", // taken by book b
		FileSize:  42,
		SpineJSON: "[]",
		TocJSON:   "[]",
		Remap: ChapterRemap{
			Moves:    []ChapterMove{{Index: 0, KeepAnchor: true}, {Index: 2, KeepAnchor: true}, {Index: -1}},
			NewCount: 5,
		},
	}
	if err := db.ReplaceBookFileContext(ctx, "a", rep); !errors.Is(err, ErrFileHashConflict) {
		t.Fatalf("conflicting hash err = %v, want ErrFileHashConflict", err)
	}
	if got, err := db.GetProgressContext(ctx, "a", "mover"); err != nil || got.Chapter != 1 {
		t.Fatalf("conflict moved progress: %+v, %v", got, err)
	}

	rep.FileHash = "hash-a2"
	if err := db.ReplaceBookFileContext(ctx, "a", rep); err != nil {
		t.Fatal(err)
	}
	book, err := db.GetBookContext(ctx, "a")
	if err != nil || book.FileHash != "hash-a2" || book.ChapterCount != 5 || book.Title != "Title a" {
		t.Fatalf("book after replace = %+v, %v", book, err)
	}

	mover, err := db.GetProgressContext(ctx, "a", "mover")
	if err != nil || mover.Chapter != 2 || mover.Percent != 0.5 || mover.CFI != anchor {
		t.Fatalf("moved progress = %+v, %v", mover, err)
	}
	if mover.UpdatedAt != "2999-01-01 00:00:01" {
		t.Errorf("moved progress updated_at = %q, want strictly after %q", mover.UpdatedAt, future)
	}
	stayer, err := db.GetProgressContext(ctx, "a", "stayer")
	if err != nil || stayer.Chapter != 0 || stayer.CFI != anchor || stayer.UpdatedAt != future {
		t.Fatalf("unmoved progress = %+v, %v; want untouched", stayer, err)
	}

	marks, err := db.ListBookmarksContext(ctx, "a", "mover")
	if err != nil || len(marks) != 1 {
		t.Fatalf("bookmarks = %+v, %v", marks, err)
	}
	if m := marks[0]; m.Chapter != 2 || m.Percent != 0.7 || m.CFI.Valid || m.Label != "x" {
		t.Errorf("unmatched bookmark = %+v, want chapter 2 kept, anchor dropped", m)
	}
}
