package fonts

import "testing"

func TestDetectRoles(t *testing.T) {
	tests := []struct {
		name                  string
		files                 []string
		regular, italic, bold string
	}{
		{
			name:    "explicit names",
			files:   []string{"Foo-Regular.woff2", "Foo-Italic.woff2", "Foo-Bold.woff2"},
			regular: "Foo-Regular.woff2", italic: "Foo-Italic.woff2", bold: "Foo-Bold.woff2",
		},
		{
			name:    "oblique counts as italic, semibold as bold",
			files:   []string{"X-Roman.otf", "X-Oblique.otf", "X-SemiBold.otf"},
			regular: "X-Roman.otf", italic: "X-Oblique.otf", bold: "X-SemiBold.otf",
		},
		{
			name:    "regular falls back to first non-italic/bold file",
			files:   []string{"Plain.ttf", "Plain-Italic.ttf"},
			regular: "Plain.ttf", italic: "Plain-Italic.ttf", bold: "",
		},
		{
			name:    "single file becomes regular",
			files:   []string{"Only.woff2"},
			regular: "Only.woff2", italic: "", bold: "",
		},
		{
			name:    "weight tokens",
			files:   []string{"Mono-400.woff2", "Mono-700.woff2"},
			regular: "Mono-400.woff2", italic: "", bold: "Mono-700.woff2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectRoles(tc.files)
			if got.Regular != tc.regular || got.Italic != tc.italic || got.Bold != tc.bold {
				t.Errorf("detectRoles(%v) = %+v, want regular=%q italic=%q bold=%q",
					tc.files, got, tc.regular, tc.italic, tc.bold)
			}
		})
	}
}

func TestParseUserFontPath(t *testing.T) {
	tests := []struct {
		path      string
		dir, file string
		ok        bool
	}{
		{"/user/Minion/Reg.woff2", "Minion", "Reg.woff2", true},
		{"/user/Minion/sub/Reg.woff2", "", "", false}, // nested
		{"/user/../etc/passwd", "", "", false},        // traversal
		{"/user/Minion/", "", "", false},              // no file
		{"/Spectral.woff2", "", "", false},            // embedded path, not user
		{`/user/Minion/..\x`, "", "", false},          // backslash
	}
	for _, tc := range tests {
		dir, file, ok := parseUserFontPath(tc.path)
		if ok != tc.ok || dir != tc.dir || file != tc.file {
			t.Errorf("parseUserFontPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.path, dir, file, ok, tc.dir, tc.file, tc.ok)
		}
	}
}
