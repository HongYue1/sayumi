package storage

import (
	"context"
	"errors"
	"testing"
)

// testBuiltinFlairs mirrors the production built-in set used by the API.
var testBuiltinFlairs = map[string]struct{}{
	"reading":      {},
	"finished":     {},
	"dropped":      {},
	"plan-to-read": {},
}

func TestDB_FlairExistsContext(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.InsertFlairContext(ctx, FlairRecord{
		ID: "flair_known", UserID: "default", Label: "Favorite", Color: "#abc",
	}); err != nil {
		t.Fatalf("seed flair: %v", err)
	}

	tests := []struct {
		name   string
		id     string
		userID string
		want   bool
	}{
		{"existing flair for user", "flair_known", "default", true},
		{"unknown id", "flair_missing", "default", false},
		{"existing id but wrong user", "flair_known", "other", false},
		{"empty id", "", "default", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := db.FlairExistsContext(ctx, tc.id, tc.userID)
			if err != nil {
				t.Fatalf("FlairExistsContext: %v", err)
			}
			if got != tc.want {
				t.Errorf("FlairExistsContext(%q, %q) = %v, want %v", tc.id, tc.userID, got, tc.want)
			}
		})
	}
}

func TestSetBookFlairCheckedContext(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
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

	// Clear.
	if err := db.SetBookFlairCheckedContext(ctx, "id1", "default", "", testBuiltinFlairs); err != nil {
		t.Fatalf("clear: %v", err)
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
	ctx := context.Background()

	const ts = "2026-01-02 03:04:05"
	for _, id := range []string{"flair_z", "flair_a", "flair_m"} {
		if err := db.InsertFlairContext(ctx, FlairRecord{
			ID: id, UserID: "default", Label: id, Color: "#000000", CreatedAt: ts,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	got, err := db.ListFlairsContext(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"flair_a", "flair_m", "flair_z"}
	if len(got) != len(want) {
		t.Fatalf("count = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

// GetBookFlairContext is the single-book counterpart of
// GetAllBookFlairsContext. Single-book responses need exactly one row, and an
// unassigned book is a normal state rather than an error, so sql.ErrNoRows has
// to collapse to the empty string instead of surfacing.
func TestGetBookFlairContext(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	ctx := context.Background()
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
