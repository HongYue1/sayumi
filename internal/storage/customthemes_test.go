package storage

import (
	"context"
	"errors"
	"testing"
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
	ctx := context.Background()

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
	ctx := context.Background()

	// Same created_at forces the id tie-breaker.
	const ts = "2026-01-02 03:04:05"
	for _, id := range []string{"theme_z", "theme_a", "theme_m"} {
		rec := sampleTheme(id, "default", id)
		rec.CreatedAt = ts
		rec.UpdatedAt = ts
		if err := db.InsertCustomThemeContext(ctx, rec); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	got, err := db.ListCustomThemesContext(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"theme_a", "theme_m", "theme_z"}
	if len(got) != len(want) {
		t.Fatalf("count = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q", i, got[i].ID, id)
		}
		if got[i].CreatedAt != ts {
			t.Errorf("created_at[%d] = %q, want %q", i, got[i].CreatedAt, ts)
		}
	}
}

func TestGenerateCustomThemeID(t *testing.T) {
	t.Parallel()
	a := GenerateCustomThemeID()
	b := GenerateCustomThemeID()
	if a == "" || b == "" || a == b {
		t.Fatalf("ids not unique/non-empty: %q %q", a, b)
	}
	if len(a) < len("theme_")+8 {
		t.Fatalf("id too short: %q", a)
	}
	done := make(chan string, 4)
	for range 4 {
		go func() { done <- GenerateCustomThemeID() }()
	}
	seen := map[string]bool{}
	for range 4 {
		id := <-done
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
