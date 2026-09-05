package storage

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// testBuiltinFlairs mirrors the production built-in set used by the API.
var testBuiltinFlairs = map[string]struct{}{
	"reading":      {},
	"finished":     {},
	"dropped":      {},
	"plan-to-read": {},
}

func TestBookFlairOwnershipAndScopedDeletion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	for _, id := range []string{"a", "b", "c"} {
		mustInsertBook(t, db, sampleBook(id, "hash-"+id, "/lib/"+id+".epub"))
	}
	if err := db.InsertFlairContext(ctx, FlairRecord{ID: "flair_one", UserID: "reader", Label: "Favorite", Color: "#123456"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "flair_one", "b": "flair_one", "c": "reading"}
	for bookID, flairID := range want {
		if err := db.SetBookFlairCheckedContext(ctx, bookID, "reader", flairID, testBuiltinFlairs); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetBookFlairCheckedContext(ctx, "a", "other", "finished", testBuiltinFlairs); err != nil {
		t.Fatal(err)
	}
	// The schema permits this reference; cleanup must still respect its user scope.
	if _, err := db.ExecContext(ctx, `INSERT INTO book_flairs (book_id, user_id, flair_id) VALUES ('b', 'other', 'flair_one')`); err != nil {
		t.Fatal(err)
	}
	otherWant := map[string]string{"a": "finished", "b": "flair_one"}
	got, err := db.GetAllBookFlairsContext(ctx, "reader")
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("assignments = %v, %v; want %v", got, err, want)
	}
	got["a"] = "caller mutation"
	delete(got, "b")
	again, err := db.GetAllBookFlairsContext(ctx, "reader")
	if err != nil || !maps.Equal(again, want) {
		t.Fatalf("assignments after caller mutation = %v, %v; want %v", again, err, want)
	}
	if empty, err := db.GetAllBookFlairsContext(ctx, "missing"); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty assignments = %v, %v; want non-nil empty map", empty, err)
	}
	if err := db.DeleteFlairContext(ctx, "flair_one", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-user delete: %v", err)
	}
	if err := db.DeleteFlairContext(ctx, "flair_one", "reader"); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.GetAllBookFlairsContext(ctx, "reader")
	if err != nil || !maps.Equal(remaining, map[string]string{"c": "reading"}) {
		t.Fatalf("assignments after delete = %v, %v", remaining, err)
	}
	other, err := db.GetAllBookFlairsContext(ctx, "other")
	if err != nil || !maps.Equal(other, otherWant) {
		t.Fatalf("other user's assignments = %v, %v; want %v", other, err, otherWant)
	}
}

func TestDeleteFlairRollsBackOnAssignmentFailure(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	rec := FlairRecord{ID: "flair_one", UserID: "reader", Label: "Favorite", Color: "#123456", CreatedAt: "2026-01-02 03:04:05"}
	if err := db.InsertFlairContext(ctx, rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": rec.ID, "b": rec.ID}
	for id := range want {
		mustInsertBook(t, db, sampleBook(id, "hash-"+id, "/lib/"+id+".epub"))
		if err := db.SetBookFlairCheckedContext(ctx, id, rec.UserID, rec.ID, testBuiltinFlairs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER reject_flair_cleanup BEFORE DELETE ON book_flairs
		BEGIN SELECT RAISE(ABORT, 'assignment cleanup rejected'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteFlairContext(ctx, rec.ID, rec.UserID); err == nil || !strings.Contains(err.Error(), "assignment cleanup rejected") {
		t.Fatalf("delete: %v; want injected cleanup failure", err)
	}
	flairs, err := db.ListFlairsContext(ctx, rec.UserID)
	if err != nil || !slices.Equal(flairs, []FlairRecord{rec}) {
		t.Fatalf("flairs after rollback = %+v, %v; want %+v", flairs, err, rec)
	}
	assigned, err := db.GetAllBookFlairsContext(ctx, rec.UserID)
	if err != nil || !maps.Equal(assigned, want) {
		t.Fatalf("assignments after rollback = %v, %v; want %v", assigned, err, want)
	}
}

func TestFlairCancellationDoesNotWrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("book", "hash-book", "/lib/book.epub"))
	rec := FlairRecord{ID: "flair_one", UserID: "reader", Label: "Favorite", Color: "#123456", CreatedAt: "2026-01-02 03:04:05"}
	if err := db.InsertFlairContext(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := db.SetBookFlairCheckedContext(ctx, "book", "reader", rec.ID, testBuiltinFlairs); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ListFlairsContext(canceled, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if _, err := db.GetAllBookFlairsContext(canceled, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled assignments: %v", err)
	}
	if _, err := db.GetBookFlairContext(canceled, "book", "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled point read: %v", err)
	}
	if err := db.InsertFlairContext(canceled, FlairRecord{ID: "flair_new", UserID: "reader"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled insert: %v", err)
	}
	if err := db.DeleteFlairContext(canceled, rec.ID, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete: %v", err)
	}
	for _, flairID := range []string{"finished", rec.ID, ""} {
		if err := db.SetBookFlairCheckedContext(canceled, "book", "reader", flairID, testBuiltinFlairs); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled assignment %q: %v", flairID, err)
		}
	}
	flairs, err := db.ListFlairsContext(ctx, "reader")
	if err != nil || !slices.Equal(flairs, []FlairRecord{rec}) {
		t.Fatalf("flairs after cancellation = %+v, %v; want %+v", flairs, err, rec)
	}
	assigned, err := db.GetAllBookFlairsContext(ctx, "reader")
	if err != nil || !maps.Equal(assigned, map[string]string{"book": rec.ID}) {
		t.Fatalf("assignments after cancellation = %v, %v", assigned, err)
	}
}

func TestGenerateFlairID(t *testing.T) {
	t.Parallel()
	checkGeneratedCustomizationIDs(t, "flair_", GenerateFlairID)
}

func TestSetBookFlairCheckedContext(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))

	if err := db.InsertFlairContext(ctx, FlairRecord{
		ID: "flair_1", UserID: "default", Label: "Favorite", Color: "#ffffff",
	}); err != nil {
		t.Fatalf("insert flair: %v", err)
	}

	// Built-in ids are accepted without a flairs row.
	if err := db.SetBookFlairCheckedContext(ctx, "id1", "default", "reading", testBuiltinFlairs); err != nil {
		t.Fatalf("assign builtin: %v", err)
	}
	assigned, err := db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs: %v", err)
	}
	if assigned["id1"] != "reading" {
		t.Fatalf("builtin assignment = %q, want reading", assigned["id1"])
	}

	// Custom flair that exists for the user.
	if err := db.SetBookFlairCheckedContext(ctx, "id1", "default", "flair_1", testBuiltinFlairs); err != nil {
		t.Fatalf("assign custom: %v", err)
	}
	assigned, err = db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs: %v", err)
	}
	if assigned["id1"] != "flair_1" {
		t.Fatalf("custom assignment = %q, want flair_1", assigned["id1"])
	}

	// Missing custom flair.
	if err := db.SetBookFlairCheckedContext(ctx, "id1", "default", "flair_missing", testBuiltinFlairs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing custom err = %v, want ErrNotFound", err)
	}
	// Wrong-user custom flair must not assign.
	if err := db.SetBookFlairCheckedContext(ctx, "id1", "other", "flair_1", testBuiltinFlairs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-user custom err = %v, want ErrNotFound", err)
	}
	// The upsert itself must report a concurrently deleted/missing book as a
	// domain not-found error rather than leaking a foreign-key constraint error.
	if err := db.SetBookFlairCheckedContext(ctx, "missing", "default", "reading", testBuiltinFlairs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing book err = %v, want ErrNotFound", err)
	}

	assigned, err = db.GetAllBookFlairsContext(ctx, "default")
	if err != nil || !maps.Equal(assigned, map[string]string{"id1": "flair_1"}) {
		t.Fatalf("failed validation changed assignments: %v, %v", assigned, err)
	}

	// Clearing is idempotent, including for a missing book, and needs no built-in set.
	for _, bookID := range []string{"id1", "id1", "missing"} {
		if err := db.SetBookFlairCheckedContext(ctx, bookID, "default", "", nil); err != nil {
			t.Fatalf("clear %q: %v", bookID, err)
		}
	}
	assigned, err = db.GetAllBookFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("get book flairs after clear: %v", err)
	}
	if _, ok := assigned["id1"]; ok {
		t.Fatal("assignment survived clear")
	}
}

func TestListFlairsStableTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	want := []FlairRecord{
		{ID: "flair_z", UserID: "reader", Label: "Earlier", Color: "#123456", CreatedAt: "2026-01-01 02:03:04"},
		{ID: "flair_a", UserID: "reader", Label: "Favorite", Color: "#ffffff", CreatedAt: "2026-01-02 03:04:05"},
		{ID: "flair_m", UserID: "reader", Label: "Revisit", Color: "#000000", CreatedAt: "2026-01-02 03:04:05"},
	}
	for _, rec := range slices.Backward(want) {
		if err := db.InsertFlairContext(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertFlairContext(ctx, FlairRecord{ID: "flair_other", UserID: "other", Label: "Other", Color: "#abcdef"}); err != nil {
		t.Fatal(err)
	}
	duplicate := want[1]
	duplicate.Label = "Replacement"
	duplicate.UserID = "other"
	if err := db.InsertFlairContext(ctx, duplicate); err == nil {
		t.Fatal("duplicate ID was accepted")
	}

	got, err := db.ListFlairsContext(ctx, "reader")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("list = %+v, %v; want %+v", got, err, want)
	}
	got[0] = FlairRecord{}
	again, err := db.ListFlairsContext(ctx, "reader")
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("list after caller mutation = %+v, %v; want %+v", again, err, want)
	}
	if empty, err := db.ListFlairsContext(ctx, "missing"); err != nil || empty != nil {
		t.Fatalf("empty list = %+v, %v; want nil, nil", empty, err)
	}
}

// GetBookFlairContext is the single-book counterpart of
// GetAllBookFlairsContext. Single-book responses need exactly one row, and an
// unassigned book is a normal state rather than an error, so sql.ErrNoRows has
// to collapse to the empty string instead of surfacing.
func TestGetBookFlairContext(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("id2", "hash-b", "/lib/b.epub"))

	flairID, err := db.GetBookFlairContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("unassigned book err = %v, want nil", err)
	}
	if flairID != "" {
		t.Fatalf("unassigned flair = %q, want empty", flairID)
	}

	if err := db.SetBookFlairCheckedContext(ctx, "id1", "default", "reading", testBuiltinFlairs); err != nil {
		t.Fatalf("assign flair: %v", err)
	}
	flairID, err = db.GetBookFlairContext(ctx, "id1", "default")
	if err != nil {
		t.Fatalf("get assigned flair: %v", err)
	}
	if flairID != "reading" {
		t.Fatalf("assigned flair = %q, want reading", flairID)
	}

	// Assignments are scoped to one (book, user) pair, so neither another user
	// nor another book may pick up id1's flair.
	flairID, err = db.GetBookFlairContext(ctx, "id1", "other")
	if err != nil || flairID != "" {
		t.Fatalf("other user flair = %q, err = %v; want empty and nil", flairID, err)
	}
	flairID, err = db.GetBookFlairContext(ctx, "id2", "default")
	if err != nil || flairID != "" {
		t.Fatalf("other book flair = %q, err = %v; want empty and nil", flairID, err)
	}

	// A book deleted between the write and this read is the same "no
	// assignment" answer, not a failure that would break the response.
	flairID, err = db.GetBookFlairContext(ctx, "missing", "default")
	if err != nil || flairID != "" {
		t.Fatalf("missing book flair = %q, err = %v; want empty and nil", flairID, err)
	}
}
