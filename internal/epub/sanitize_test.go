package epub

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func sanitizeHTML(t *testing.T, in string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	Sanitize(doc)
	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestSanitizeStripsScriptsAndHandlers(t *testing.T) {
	out := sanitizeHTML(t, `<p>hi</p><script>alert(1)</script>`+
		`<a href="javascript:evil()" onclick="steal()">x</a>`+
		`<img src="ok.png" onerror="boom()">`)

	checks := []struct {
		desc, needle string
		wantPresent  bool
	}{
		{"keeps benign text", "hi", true},
		{"removes <script>", "alert(1)", false},
		{"strips javascript: URI", "javascript:", false},
		{"strips onclick handler", "onclick", false},
		{"strips onerror handler", "onerror", false},
		{"keeps safe img src", "ok.png", true},
	}
	for _, c := range checks {
		got := strings.Contains(strings.ToLower(out), strings.ToLower(c.needle))
		if got != c.wantPresent {
			t.Errorf("%s: presence of %q = %v, want %v\noutput: %s", c.desc, c.needle, got, c.wantPresent, out)
		}
	}
}

func TestContentTypeByExt(t *testing.T) {
	tests := map[string]string{
		"a/b/chapter.xhtml": "text/html; charset=utf-8",
		"style.CSS":         "text/css; charset=utf-8",
		"pic.JPG":           "image/jpeg",
		"pic.jpeg":          "image/jpeg",
		"icon.svg":          "image/svg+xml",
		"font.woff2":        "font/woff2",
		"font.otf":          "font/otf",
		"noext":             "", // unknown → empty (caller sniffs)
		"weird.zzz":         "",
	}
	for path, want := range tests {
		if got := ContentTypeByExt(path); got != want {
			t.Errorf("ContentTypeByExt(%q) = %q, want %q", path, got, want)
		}
	}
}
