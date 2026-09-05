package storage

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleTheme(id, userID, name string) CustomThemeRecord {
	return CustomThemeRecord{
		ID:     id,
		UserID: userID,
		Name:   name,
		Group:  "light",
		Bg:     "#111111",
		Fg:     "#eeeeee",
		Accent: "#2563eb",
	}
}

func TestCustomThemesCRUDAndScope(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()

	rec := sampleTheme("theme_a", "default", "Forest")
	if err := db.InsertCustomThemeContext(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	list, err := db.ListCustomThemesContext(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "theme_a" || list[0].Name != "Forest" {
		t.Fatalf("list = %+v, want one Forest theme_a", list)
	}
	if list[0].CreatedAt == "" || list[0].UpdatedAt == "" {
		t.Fatalf("timestamps not set: created=%q updated=%q", list[0].CreatedAt, list[0].UpdatedAt)
	}
	createdAt := list[0].CreatedAt

	// Other users must not see or mutate this theme.
	otherList, err := db.ListCustomThemesContext(ctx, "other")
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(otherList) != 0 {
		t.Fatalf("other user list = %+v, want empty", otherList)
	}
	if _, err := db.UpdateCustomThemeContext(ctx, sampleTheme("theme_a", "other", "Hijack")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update wrong user err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteCustomThemeContext(ctx, "theme_a", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete wrong user err = %v, want ErrNotFound", err)
	}

	updatedIn := sampleTheme("theme_a", "default", "Grove")
	updatedIn.Group = "dark"
	updatedIn.Bg = "#0a0a0a"
	updatedIn.Fg = "#fafafa"
	updatedIn.Accent = "" // empty accent is allowed (client auto)
	updated, err := db.UpdateCustomThemeContext(ctx, updatedIn)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Grove" || updated.Group != "dark" || updated.Bg != "#0a0a0a" || updated.Accent != "" {
		t.Fatalf("updated fields = %+v", updated)
	}
	if updated.CreatedAt != createdAt {
		t.Fatalf("created_at changed: got %q want %q", updated.CreatedAt, createdAt)
	}
	if updated.UpdatedAt == "" {
		t.Fatal("updated_at empty after update")
	}

	if _, err := db.UpdateCustomThemeContext(ctx, sampleTheme("missing", "default", "Nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteCustomThemeContext(ctx, "missing", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v, want ErrNotFound", err)
	}

	if err := db.DeleteCustomThemeContext(ctx, "theme_a", "default"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = db.ListCustomThemesContext(ctx, "default")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list after delete = %+v, want empty", list)
	}
}

func TestListCustomThemesStableTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	want := []CustomThemeRecord{
		sampleTheme("theme_z", "reader", "Earlier"),
		sampleTheme("theme_a", "reader", "Forest"),
		sampleTheme("theme_m", "reader", "Night"),
	}
	for i := range want {
		want[i].CreatedAt = "2026-01-02 03:04:05"
		want[i].UpdatedAt = "2026-01-03 04:05:06"
	}
	want[0].CreatedAt = "2026-01-01 02:03:04"
	want[0].Accent = ""
	want[2].Group = "dark"
	want[2].Bg = "#222222"
	want[2].Fg = "#dddddd"
	for _, rec := range slices.Backward(want) {
		if err := db.InsertCustomThemeContext(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertCustomThemeContext(ctx, sampleTheme("theme_other", "other", "Other")); err != nil {
		t.Fatal(err)
	}
	duplicate := want[1]
	duplicate.Name = "Replacement"
	duplicate.UserID = "other"
	if err := db.InsertCustomThemeContext(ctx, duplicate); err == nil {
		t.Fatal("duplicate ID was accepted")
	}

	got, err := db.ListCustomThemesContext(ctx, "reader")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("list = %+v, %v; want %+v", got, err, want)
	}
	got[0] = CustomThemeRecord{}
	again, err := db.ListCustomThemesContext(ctx, "reader")
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("list after caller mutation = %+v, %v; want %+v", again, err, want)
	}
	if empty, err := db.ListCustomThemesContext(ctx, "missing"); err != nil || empty != nil {
		t.Fatalf("empty list = %+v, %v; want nil, nil", empty, err)
	}
}

func TestCustomThemeUpdateTimestamps(t *testing.T) {
	t.Parallel()
	for _, updatedAt := range []string{"2026-02-03 04:05:06", ""} {
		name := "supplied"
		if updatedAt == "" {
			name = "generated"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t)
			ctx := t.Context()
			original := sampleTheme("theme_one", "reader", "Original")
			original.CreatedAt = "2000-01-02 03:04:05"
			original.UpdatedAt = "2001-02-03 04:05:06"
			if err := db.InsertCustomThemeContext(ctx, original); err != nil {
				t.Fatal(err)
			}
			in := CustomThemeRecord{
				ID: original.ID, UserID: original.UserID, Name: "Replacement", Group: "dark",
				Bg: "#010203", Fg: "#fdfcfb", Accent: "",
				CreatedAt: "1999-01-01 00:00:00", UpdatedAt: updatedAt,
			}
			got, err := db.UpdateCustomThemeContext(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			want := in
			want.CreatedAt = original.CreatedAt
			if updatedAt == "" {
				if _, err := time.Parse(time.DateTime, got.UpdatedAt); err != nil {
					t.Fatalf("generated timestamp %q: %v", got.UpdatedAt, err)
				}
				if got.UpdatedAt == original.UpdatedAt {
					t.Fatal("update retained the old timestamp")
				}
				want.UpdatedAt = got.UpdatedAt
			}
			if got != want {
				t.Fatalf("updated = %+v, want %+v", got, want)
			}
			stored, err := db.ListCustomThemesContext(ctx, "reader")
			if err != nil || !slices.Equal(stored, []CustomThemeRecord{want}) {
				t.Fatalf("stored = %+v, %v; want %+v", stored, err, want)
			}
		})
	}
}

func TestCustomThemeCancellationDoesNotWrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	original := sampleTheme("theme_one", "reader", "Original")
	original.CreatedAt = "2026-01-02 03:04:05"
	original.UpdatedAt = original.CreatedAt
	if err := db.InsertCustomThemeContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ListCustomThemesContext(canceled, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if err := db.InsertCustomThemeContext(canceled, sampleTheme("theme_new", "reader", "New")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled insert: %v", err)
	}
	if got, err := db.UpdateCustomThemeContext(canceled, sampleTheme(original.ID, "reader", "Changed")); !errors.Is(err, context.Canceled) || got != (CustomThemeRecord{}) {
		t.Fatalf("canceled update: %+v, %v", got, err)
	}
	if err := db.DeleteCustomThemeContext(canceled, original.ID, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete: %v", err)
	}
	got, err := db.ListCustomThemesContext(ctx, "reader")
	if err != nil || !slices.Equal(got, []CustomThemeRecord{original}) {
		t.Fatalf("stored after cancellation = %+v, %v; want %+v", got, err, original)
	}
}

func TestGenerateCustomThemeID(t *testing.T) {
	t.Parallel()
	checkGeneratedCustomizationIDs(t, "theme_", GenerateCustomThemeID)
}

func checkGeneratedCustomizationIDs(t *testing.T, prefix string, generate func() (string, error)) {
	t.Helper()
	var results [8]struct {
		id  string
		err error
	}
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i].id, results[i].err = generate()
		})
	}
	// Join workers before reporting errors so a failed generation cannot strand the parent.
	wg.Wait()
	seen := make(map[string]bool, len(results))
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("generate ID: %v", result.err)
		}
		suffix, ok := strings.CutPrefix(result.id, prefix)
		if !ok || len(suffix) != 16 || strings.Trim(suffix, "0123456789abcdef") != "" {
			t.Fatalf("ID %q must be %s followed by 16 lowercase hex digits", result.id, prefix)
		}
		if seen[result.id] {
			t.Fatalf("duplicate ID %q", result.id)
		}
		seen[result.id] = true
	}
}
