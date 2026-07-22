package api

import (
	"strings"
	"testing"
)

func validCustomThemeBody(name string) customThemeBody {
	return customThemeBody{
		Name:   name,
		Group:  "light",
		Bg:     "#ffffff",
		Fg:     "#111111",
		Accent: "",
	}
}

func TestNormalizeCustomThemeBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     customThemeBody
		wantName string
		wantMsg  string
		wantOK   bool
	}{
		{
			name:     "trims valid payload",
			body:     customThemeBody{Name: "  Paper  ", Group: "light", Bg: "  #fff  ", Fg: " #111111 ", Accent: " #2563eb "},
			wantName: "Paper",
			wantOK:   true,
		},
		{
			name:    "empty name",
			body:    validCustomThemeBody("   "),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:     "sixty ASCII characters",
			body:     validCustomThemeBody(strings.Repeat("a", maxThemeNameLen)),
			wantName: strings.Repeat("a", maxThemeNameLen),
			wantOK:   true,
		},
		{
			name:    "sixty one ASCII characters",
			body:    validCustomThemeBody(strings.Repeat("a", maxThemeNameLen+1)),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:     "sixty Arabic characters",
			body:     validCustomThemeBody(strings.Repeat("م", maxThemeNameLen)),
			wantName: strings.Repeat("م", maxThemeNameLen),
			wantOK:   true,
		},
		{
			name:    "sixty one Arabic characters",
			body:    validCustomThemeBody(strings.Repeat("م", maxThemeNameLen+1)),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:    "invalid group",
			body:    customThemeBody{Name: "Paper", Group: "sepia", Bg: "#fff", Fg: "#111"},
			wantMsg: "group must be 'light' or 'dark'",
		},
		{
			name:    "invalid background",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "white", Fg: "#111"},
			wantMsg: "bg must be a hex color like #1c1917",
		},
		{
			name:    "invalid foreground",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "#fff", Fg: "black"},
			wantMsg: "fg must be a hex color like #1c1917",
		},
		{
			name:    "invalid accent",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "#fff", Fg: "#111", Accent: "blue"},
			wantMsg: "accent must be a hex color like #2563eb",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.body
			msg, ok := normalizeCustomThemeBody(&body)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (msg %q)", ok, tc.wantOK, msg)
			}
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if tc.wantName != "" && body.Name != tc.wantName {
				t.Errorf("name = %q, want %q", body.Name, tc.wantName)
			}
		})
	}
}
