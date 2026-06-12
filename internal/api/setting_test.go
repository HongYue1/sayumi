package api

import "testing"

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
			for i := 0; i < 101; i++ {
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
