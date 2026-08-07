package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestValidateSettings(t *testing.T) {
	base := func() *settingsJSON {
		return &settingsJSON{
			FontSize: 26, FontFamily: "spectral", Theme: "rose-pine", DisplayMode: "scroll",
		}
	}
	tests := []struct {
		name   string
		mut    func(*settingsJSON)
		wantOK bool
	}{
		{"valid baseline", func(*settingsJSON) {}, true},
		{"fontSize too small", func(s *settingsJSON) { s.FontSize = 9 }, false},
		{"fontSize too large", func(s *settingsJSON) { s.FontSize = 51 }, false},
		{"empty fontFamily", func(s *settingsJSON) { s.FontFamily = "" }, false},
		{"empty theme", func(s *settingsJSON) { s.Theme = "" }, false},
		{"bad displayMode", func(s *settingsJSON) { s.DisplayMode = "sideways" }, false},
		{"paged-two ok", func(s *settingsJSON) { s.DisplayMode = "paged-two" }, true},
		{"lineHeight out of range", func(s *settingsJSON) { v := 9.0; s.LineHeight = &v }, false},
		{"too many font roles", func(s *settingsJSON) {
			s.FontRoles = map[string]fontRoleEntry{}
			for i := range 101 {
				s.FontRoles[string(rune('a'+i%26))+string(rune('0'+i/26))] = fontRoleEntry{Regular: "x"}
			}
		}, false},
		{"font role with path traversal", func(s *settingsJSON) {
			s.FontRoles = map[string]fontRoleEntry{"user:F": {Regular: "../etc/passwd"}}
		}, false},
		{"font role with slash", func(s *settingsJSON) {
			s.FontRoles = map[string]fontRoleEntry{"user:F": {Italic: "a/b.ttf"}}
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mut(s)
			if _, ok := validateSettings(s); ok != tc.wantOK {
				t.Errorf("validateSettings ok = %v, want %v", ok, tc.wantOK)
			}
		})
	}
}

func TestNormalizeSettingsPrunesEmptyFontRoles(t *testing.T) {
	s := &settingsJSON{
		FontFamily:  "  spectral  ",
		DisplayMode: " Scroll ",
		Theme:       " rose-pine ",
		FontRoles: map[string]fontRoleEntry{
			"user:Keep": {Regular: " Reg.ttf "},
			"user:Drop": {Regular: "  ", Italic: "", Bold: " "},
		},
	}
	normalizeSettings(s)

	if s.FontFamily != "spectral" || s.DisplayMode != "scroll" || s.Theme != "rose-pine" {
		t.Errorf("normalize did not trim: %+v", *s)
	}
	if _, ok := s.FontRoles["user:Drop"]; ok {
		t.Error("all-empty font role entry should have been pruned")
	}
	keep, ok := s.FontRoles["user:Keep"]
	if !ok || keep.Regular != "Reg.ttf" {
		t.Errorf("kept entry not trimmed/retained: %+v", s.FontRoles)
	}
}

func TestFontFamilyIDCapsAgree(t *testing.T) {
	// A user family id is "user:" plus a ./Fonts/ directory name, and the
	// scanner caps neither. That same id reaches fontFamily,
	// chapterTitleFontFamily and the fontRoles keys, so all three ceilings
	// must agree. While they disagreed you could save role overrides for a
	// family that could never be selected as the active font.
	id := "user:" + strings.Repeat("a", maxFontFamilyIDBytes-len("user:"))
	if len(id) != maxFontFamilyIDBytes {
		t.Fatalf("test id is %d bytes, want %d", len(id), maxFontFamilyIDBytes)
	}

	s := &settingsJSON{
		FontSize: 26, Theme: "rose-pine", DisplayMode: "scroll",
		FontFamily:       id,
		ChapterTitleFont: &id,
		FontRoles:        map[string]fontRoleEntry{id: {Regular: "Regular.otf"}},
	}
	if msg, ok := validateSettings(s); !ok {
		t.Errorf("an id exactly at the shared cap was rejected: %s", msg)
	}

	over := id + "a"
	s.FontFamily = over
	familyMsg, ok := validateSettings(s)
	if ok {
		t.Fatal("fontFamily one byte over the cap was accepted")
	}
	assertNamesCap(t, familyMsg)

	s.FontFamily = id
	s.ChapterTitleFont = &over
	titleMsg, ok := validateSettings(s)
	if ok {
		t.Fatal("chapterTitleFontFamily one byte over the cap was accepted")
	}
	assertNamesCap(t, titleMsg)
}

// assertNamesCap keeps an over-cap message honest about both the ceiling it
// enforces and the unit it counts. Without it the shared constant could be
// retuned while every message kept quoting the old number.
func assertNamesCap(t *testing.T, msg string) {
	t.Helper()
	if !strings.Contains(msg, "bytes") {
		t.Errorf("message should state the unit it enforces, got %q", msg)
	}
	if want := strconv.Itoa(maxFontFamilyIDBytes); !strings.Contains(msg, want) {
		t.Errorf("message should quote the %s byte cap, got %q", want, msg)
	}
}

func TestFontFamilyCapCountsBytesNotRunes(t *testing.T) {
	// len() counts bytes, so a folder name far under the cap in characters
	// can still exceed it. The message has to name the unit it enforces.
	s := &settingsJSON{
		FontSize: 26, Theme: "rose-pine", DisplayMode: "scroll",
		FontFamily: "user:" + strings.Repeat("あ", 50),
	}
	msg, ok := validateSettings(s)
	if ok {
		t.Fatal("a 155-byte family id should be rejected")
	}
	assertNamesCap(t, msg)
}

func TestFontRoleEntryOmitsEmptyRoles(t *testing.T) {
	// The client resolves an unset role with nullish coalescing against the
	// backend's detected file, which only works while an unset role is ABSENT
	// from the payload rather than "". normalizeSettings deliberately keeps a
	// partially filled entry, so omitempty on every field is the only thing
	// stopping the empty siblings from round-tripping and silently
	// suppressing the italic and bold faces.
	s := &settingsJSON{
		FontFamily:  "spectral",
		DisplayMode: "scroll",
		Theme:       "rose-pine",
		FontRoles: map[string]fontRoleEntry{
			"user:Minion": {Regular: "Regular.otf"},
		},
	}
	normalizeSettings(s)

	blob, err := json.Marshal(s.FontRoles)
	if err != nil {
		t.Fatalf("marshal font roles: %v", err)
	}
	got := string(blob)
	if want := `{"user:Minion":{"regular":"Regular.otf"}}`; got != want {
		t.Errorf("font roles serialized as %s, want %s", got, want)
	}
	for _, role := range []string{"italic", "bold", "boldItalic"} {
		if strings.Contains(got, role) {
			t.Errorf("unset role %q must not appear in the payload: %s", role, got)
		}
	}

	// Regular is not privileged: the panel writes whichever role the user
	// picked and deletes the others, so any single role can be the only one
	// present and must still travel alone.
	s.FontRoles = map[string]fontRoleEntry{
		"user:Minion": {Italic: "Italic.otf"},
	}
	normalizeSettings(s)

	blob, err = json.Marshal(s.FontRoles)
	if err != nil {
		t.Fatalf("marshal italic-only roles: %v", err)
	}
	if got, want := string(blob), `{"user:Minion":{"italic":"Italic.otf"}}`; got != want {
		t.Errorf("italic-only entry serialized as %s, want %s", got, want)
	}
}
