package storage

import (
	"context"
	"errors"
	"testing"
)

func samplePreset(id, userID, name, settingsJSON string) PresetRecord {
	return PresetRecord{
		ID:           id,
		UserID:       userID,
		Name:         name,
		SettingsJSON: settingsJSON,
	}
}

func TestPresetsCRUDAndScope(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	const settingsJSON = `{"fontSize":18,"theme":"rose-pine"}`
	rec := samplePreset("preset_a", "default", "Night", settingsJSON)
	if err := db.InsertPresetContext(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}

	list, err := db.ListPresetsContext(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "preset_a" || list[0].Name != "Night" {
		t.Fatalf("list = %+v, want one Night preset_a", list)
	}
	if list[0].SettingsJSON != settingsJSON {
		t.Fatalf("settings_json = %q, want %q", list[0].SettingsJSON, settingsJSON)
	}
	if list[0].CreatedAt == "" || list[0].UpdatedAt == "" {
		t.Fatalf("timestamps not set: created=%q updated=%q", list[0].CreatedAt, list[0].UpdatedAt)
	}

	// Other users must not see or delete this preset.
	otherList, err := db.ListPresetsContext(ctx, "other")
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(otherList) != 0 {
		t.Fatalf("other user list = %+v, want empty", otherList)
	}
	if err := db.DeletePresetContext(ctx, "preset_a", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete wrong user err = %v, want ErrNotFound", err)
	}

	if err := db.DeletePresetContext(ctx, "missing", "default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v, want ErrNotFound", err)
	}

	if err := db.DeletePresetContext(ctx, "preset_a", "default"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = db.ListPresetsContext(ctx, "default")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list after delete = %+v, want empty", list)
	}
}

func TestListPresetsStableTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	// Same created_at forces the id tie-breaker.
	const ts = "2026-01-02 03:04:05"
	for _, id := range []string{"preset_z", "preset_a", "preset_m"} {
		rec := samplePreset(id, "default", id, `{}`)
		rec.CreatedAt = ts
		rec.UpdatedAt = ts
		if err := db.InsertPresetContext(ctx, rec); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	got, err := db.ListPresetsContext(ctx, "default")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"preset_a", "preset_m", "preset_z"}
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

func TestGeneratePresetID(t *testing.T) {
	t.Parallel()
	a := GeneratePresetID()
	b := GeneratePresetID()
	if a == "" || b == "" || a == b {
		t.Fatalf("ids not unique/non-empty: %q %q", a, b)
	}
	if len(a) < len("preset_")+8 {
		t.Fatalf("id too short: %q", a)
	}
}
