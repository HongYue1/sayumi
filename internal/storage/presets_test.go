package storage

import (
	"context"
	"errors"
	"slices"
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
	ctx := t.Context()

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
	ctx := t.Context()
	want := []PresetRecord{
		samplePreset("preset_z", "reader", "Earlier", "{\n  \"fontSize\": 18, \"theme\": \"rose-pine\"\n}\n"),
		samplePreset("preset_a", "reader", "Automatic", `{}`),
		samplePreset("preset_m", "reader", "Night", `{"theme":"rose-pine","fontRoles":{}}`),
	}
	for i := range want {
		want[i].CreatedAt = "2026-01-02 03:04:05"
		want[i].UpdatedAt = "2026-01-03 04:05:06"
	}
	want[0].CreatedAt = "2026-01-01 02:03:04"
	for _, rec := range slices.Backward(want) {
		if err := db.InsertPresetContext(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertPresetContext(ctx, samplePreset("preset_other", "other", "Other", `{}`)); err != nil {
		t.Fatal(err)
	}
	duplicate := want[1]
	duplicate.Name = "Replacement"
	duplicate.SettingsJSON = `{"fontSize":30}`
	duplicate.UserID = "other"
	if err := db.InsertPresetContext(ctx, duplicate); err == nil {
		t.Fatal("duplicate ID was accepted")
	}

	// Storage must preserve the exact JSON string, including whitespace.
	got, err := db.ListPresetsContext(ctx, "reader")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("list = %+v, %v; want %+v", got, err, want)
	}
	got[0] = PresetRecord{}
	again, err := db.ListPresetsContext(ctx, "reader")
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("list after caller mutation = %+v, %v; want %+v", again, err, want)
	}
	if empty, err := db.ListPresetsContext(ctx, "missing"); err != nil || empty != nil {
		t.Fatalf("empty list = %+v, %v; want nil, nil", empty, err)
	}
}

func TestPresetCancellationDoesNotWrite(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	original := samplePreset("preset_one", "reader", "Original", `{"fontSize":18}`)
	original.CreatedAt = "2026-01-02 03:04:05"
	original.UpdatedAt = original.CreatedAt
	if err := db.InsertPresetContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.ListPresetsContext(canceled, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if err := db.InsertPresetContext(canceled, samplePreset("preset_new", "reader", "New", `{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled insert: %v", err)
	}
	if err := db.DeletePresetContext(canceled, original.ID, "reader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete: %v", err)
	}
	got, err := db.ListPresetsContext(ctx, "reader")
	if err != nil || !slices.Equal(got, []PresetRecord{original}) {
		t.Fatalf("stored after cancellation = %+v, %v; want %+v", got, err, original)
	}
}

func TestGeneratePresetID(t *testing.T) {
	t.Parallel()
	checkGeneratedCustomizationIDs(t, "preset_", GeneratePresetID)
}
