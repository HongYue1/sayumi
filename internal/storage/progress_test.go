package storage

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"testing"
	"time"
)

func TestAllProgressScopeAndRowOwnership(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	for _, id := range []string{"a", "b", "c"} {
		mustInsertBook(t, db, sampleBook(id, "hash-"+id, "/lib/"+id+".epub"))
	}
	records := []ProgressRecord{
		{BookID: "a", UserID: "reader", Chapter: 1, Percent: 0.25, CFI: sql.NullString{String: "epubcfi(/6/2)", Valid: true}},
		{BookID: "b", UserID: "reader", Chapter: 2, Percent: 0.5},
		{BookID: "c", UserID: "reader", Chapter: 3, Percent: 0.75, CFI: sql.NullString{Valid: true}},
	}
	want := make(map[string]ProgressRecord, len(records))
	for _, record := range records {
		record.UpdatedAt = "client timestamp must be ignored"
		if err := db.SaveProgressContext(ctx, record); err != nil {
			t.Fatalf("save %s: %v", record.BookID, err)
		}
		got, err := db.GetProgressContext(ctx, record.BookID, record.UserID)
		if err != nil {
			t.Fatalf("get %s: %v", record.BookID, err)
		}
		if _, err := time.Parse(time.DateTime, got.UpdatedAt); err != nil {
			t.Fatalf("server timestamp = %q: %v", got.UpdatedAt, err)
		}
		record.UpdatedAt = got.UpdatedAt
		if got != record {
			t.Errorf("progress = %+v, want %+v", got, record)
		}
		want[record.BookID] = record
	}
	if err := db.SaveProgressContext(ctx, ProgressRecord{BookID: "a", UserID: "other", Chapter: 9, Percent: 0.9}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAllProgressContext(ctx, "reader")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(got, want) {
		t.Fatalf("progress rows = %+v, want %+v", got, want)
	}
	delete(got, "a")
	changed := got["b"]
	changed.Chapter = 99
	got["b"] = changed
	again, err := db.GetAllProgressContext(ctx, "reader")
	if err != nil || !maps.Equal(again, want) {
		t.Fatalf("mutating returned map changed stored rows: %+v, %v", again, err)
	}
	if _, err := db.GetProgressContext(ctx, "a", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user's progress = %v, want ErrNotFound", err)
	}
	empty, err := db.GetAllProgressContext(ctx, "missing")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("missing user's progress map = (%v, %v), want non-nil empty map", empty, err)
	}
}

func TestProgressUpsertClearsNullableCFI(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("book", "hash", "/lib/book.epub"))
	for _, cfi := range []sql.NullString{
		{String: "epubcfi(/6/2)", Valid: true},
		{},
		{Valid: true},
	} {
		record := ProgressRecord{BookID: "book", UserID: "reader", Chapter: 2, Percent: 0.5, CFI: cfi}
		if err := db.SaveProgressContext(ctx, record); err != nil {
			t.Fatal(err)
		}
		got, err := db.GetProgressContext(ctx, record.BookID, record.UserID)
		if err != nil || got.CFI != cfi {
			t.Errorf("CFI = (%+v, %v), want %+v", got.CFI, err, cfi)
		}
	}
}

func TestProgressCancellationDoesNotWrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("book", "hash", "/lib/book.epub"))
	original := ProgressRecord{BookID: "book", UserID: "reader", Chapter: 1, Percent: 0.25}
	if err := db.SaveProgressContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetProgressContext(ctx, original.BookID, original.UserID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.GetProgressContext(canceled, "book", "reader"); !errors.Is(err, context.Canceled) {
		t.Errorf("get canceled progress: %v", err)
	}
	if _, err := db.GetAllProgressContext(canceled, "reader"); !errors.Is(err, context.Canceled) {
		t.Errorf("list canceled progress: %v", err)
	}
	original.Chapter = 9
	if err := db.SaveProgressContext(canceled, original); !errors.Is(err, context.Canceled) {
		t.Errorf("save canceled progress: %v", err)
	}
	after, err := db.GetProgressContext(ctx, "book", "reader")
	if err != nil || after != before {
		t.Errorf("canceled save changed progress: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestProgressRejectsMissingBook(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	if err := db.SaveProgressContext(ctx, ProgressRecord{BookID: "missing", UserID: "reader"}); err == nil {
		t.Fatal("progress saved for a missing book")
	}
	got, err := db.GetAllProgressContext(ctx, "reader")
	if err != nil || len(got) != 0 {
		t.Errorf("failed write left progress: %+v, %v", got, err)
	}
}
