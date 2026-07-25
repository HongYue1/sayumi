package storage

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// The settings columns are kept in step by hand in four places: the SELECT list,
// the row.Scan targets, the INSERT column list, and the INSERT arguments.
// Transposing two same-type columns in any one of them still compiles and still
// passes vet and lint, and it silently swaps two saved reader preferences.
// TestSettingsUpsert covers six columns by name; the two tests below cover every
// column, and cover new columns automatically as they are added.

// TestSettingsRoundTripsEveryColumn fills every SettingsRecord field with a
// value that is unique among the fields sharing its type, saves it, reads it
// back, and compares field by field. A transposition shows up as two fields
// reporting each other's value. A field that never reaches SQL comes back as its
// zero value.
func TestSettingsRoundTripsEveryColumn(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	var want SettingsRecord
	set := reflect.ValueOf(&want).Elem()
	rt := set.Type()
	for i := range rt.NumField() {
		field := rt.Field(i)
		// UserID is the lookup key, set below. UpdatedAt is assigned by
		// SaveSettingsContext, which ignores whatever the record carried.
		if field.Name == "UserID" || field.Name == "UpdatedAt" {
			continue
		}
		set.Field(i).Set(distinctSettingsValue(t, field, i))
	}
	want.UserID = "round-trip"

	if err := db.SaveSettingsContext(ctx, want); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	got, err := db.GetSettingsContext(ctx, want.UserID)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}

	if _, err := time.Parse(time.DateTime, got.UpdatedAt); err != nil {
		t.Errorf("updated_at = %q, want a %s timestamp: %v", got.UpdatedAt, time.DateTime, err)
	}

	read := reflect.ValueOf(got)
	for i := range rt.NumField() {
		field := rt.Field(i)
		if field.Name == "UpdatedAt" {
			continue
		}
		gotValue := read.Field(i).Interface()
		wantValue := set.Field(i).Interface()
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("%s = %+v, want %+v: columns are transposed, or this one is missing from the SELECT or the INSERT",
				field.Name, gotValue, wantValue)
		}
	}
}

// TestSettingsBoolColumnsRoundTripIndependently covers what the value sweep
// cannot: a bool has two states, so two transposed sql.NullBool columns are
// indistinguishable unless exactly one of them is true at a time. Each pass sets
// one bool column and clears the rest, so a swap surfaces as the wrong column
// coming back true. Every non-bool column stays unset here, which also proves a
// NULL survives the round trip instead of arriving as a valid zero.
func TestSettingsBoolColumnsRoundTripIndependently(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	rt := reflect.TypeFor[SettingsRecord]()
	boolType := reflect.TypeFor[sql.NullBool]()
	boolFields := make([]int, 0, rt.NumField())
	for i := range rt.NumField() {
		if rt.Field(i).Type == boolType {
			boolFields = append(boolFields, i)
		}
	}
	if len(boolFields) == 0 {
		t.Skip("SettingsRecord has no sql.NullBool columns")
	}

	for _, on := range boolFields {
		t.Run(rt.Field(on).Name, func(t *testing.T) {
			rec := SettingsRecord{UserID: "bool-" + rt.Field(on).Name}
			set := reflect.ValueOf(&rec).Elem()
			for _, i := range boolFields {
				set.Field(i).Set(reflect.ValueOf(sql.NullBool{Bool: i == on, Valid: true}))
			}

			if err := db.SaveSettingsContext(ctx, rec); err != nil {
				t.Fatalf("save settings: %v", err)
			}
			got, err := db.GetSettingsContext(ctx, rec.UserID)
			if err != nil {
				t.Fatalf("get settings: %v", err)
			}

			read := reflect.ValueOf(got)
			for _, i := range boolFields {
				value, ok := read.Field(i).Interface().(sql.NullBool)
				if !ok {
					t.Fatalf("%s is not a sql.NullBool", rt.Field(i).Name)
				}
				if !value.Valid || value.Bool != (i == on) {
					t.Errorf("%s = %+v, want %v", rt.Field(i).Name, value, i == on)
				}
			}

			if got.FontSize.Valid || got.Theme.Valid || got.LetterSpacing.Valid {
				t.Errorf("unset columns came back non-NULL: font_size=%+v theme=%+v letter_spacing=%+v",
					got.FontSize, got.Theme, got.LetterSpacing)
			}
		})
	}
}

// distinctSettingsValue returns a non-zero value for one SettingsRecord field,
// unique among the fields sharing its type. sql.NullBool is the exception, and is
// covered by TestSettingsBoolColumnsRoundTripIndependently instead. An unhandled
// type fails the test on purpose: a newly added column has to be taught to this
// helper before it counts as covered.
func distinctSettingsValue(t *testing.T, field reflect.StructField, index int) reflect.Value {
	t.Helper()
	switch field.Type {
	case reflect.TypeFor[string]():
		return reflect.ValueOf(fmt.Sprintf("text-%d", index))
	case reflect.TypeFor[sql.NullString]():
		return reflect.ValueOf(sql.NullString{String: fmt.Sprintf("value-%d", index), Valid: true})
	case reflect.TypeFor[sql.NullInt64]():
		return reflect.ValueOf(sql.NullInt64{Int64: int64(100 + index), Valid: true})
	case reflect.TypeFor[sql.NullFloat64]():
		return reflect.ValueOf(sql.NullFloat64{Float64: float64(index) + 0.5, Valid: true})
	case reflect.TypeFor[sql.NullBool]():
		return reflect.ValueOf(sql.NullBool{Bool: true, Valid: true})
	default:
		t.Fatalf("field %s has type %s, which distinctSettingsValue does not build: add it so the round-trip keeps covering every column",
			field.Name, field.Type)
		return reflect.Value{}
	}
}
