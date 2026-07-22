package epub

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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
	t.Parallel()

	out := sanitizeHTML(t, strings.Join([]string{
		"<p>hi</p><script>alert(1)</script>",
		"<a href=\"javascript:evil()\" onclick=\"steal()\">x</a>",
		"<img src=\"ok.png\" onerror=\"boom()\">",
	}, ""))

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

func TestSanitizeFormUnwrapStillStripsNestedScript(t *testing.T) {
	t.Parallel()

	// <form> is unwrapped (children promoted); nested <script> must still be removed.
	out := sanitizeHTML(t, `<form action="javascript:x"><p>keep</p><script>evil()</script></form>`)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "keep") {
		t.Fatalf("promoted text lost: %s", out)
	}
	if strings.Contains(lower, "evil()") || strings.Contains(lower, "<script") {
		t.Fatalf("nested script survived form unwrap: %s", out)
	}
	if strings.Contains(lower, "<form") {
		t.Fatalf("form element survived: %s", out)
	}
}

func TestSanitizeSVGAndMetaAndLinks(t *testing.T) {
	t.Parallel()

	in := strings.Join([]string{
		`<meta http-equiv="refresh" content="0;url=http://evil">`,
		`<meta name="viewport" content="width=device-width">`,
		`<link rel="stylesheet" href="style.css">`,
		`<link rel="preload" href="font.woff2">`,
		`<link href="orphan.css">`,
		`<svg><script>s()</script><foreignObject><p>fo</p></foreignObject>`,
		`<image href="javascript:x" onclick="y()"></image></svg>`,
		`<p>ok</p>`,
	}, "")
	out := sanitizeHTML(t, in)
	lower := strings.ToLower(out)

	if strings.Contains(lower, "http-equiv") || strings.Contains(lower, "refresh") {
		t.Fatalf("meta http-equiv survived: %s", out)
	}
	if !strings.Contains(lower, "viewport") {
		t.Fatalf("benign meta stripped: %s", out)
	}
	if !strings.Contains(lower, "stylesheet") || !strings.Contains(lower, "style.css") {
		t.Fatalf("stylesheet link stripped: %s", out)
	}
	if strings.Contains(lower, "preload") || strings.Contains(lower, "font.woff2") {
		t.Fatalf("non-stylesheet link survived: %s", out)
	}
	if strings.Contains(lower, "orphan.css") {
		t.Fatalf("rel-less link survived: %s", out)
	}
	if strings.Contains(lower, "s()") || strings.Contains(lower, "<script") {
		t.Fatalf("svg script survived: %s", out)
	}
	if strings.Contains(lower, "foreignobject") || strings.Contains(lower, ">fo<") {
		t.Fatalf("svg foreignObject survived: %s", out)
	}
	if strings.Contains(lower, "javascript:") || strings.Contains(lower, "onclick") {
		t.Fatalf("svg dangerous attrs survived: %s", out)
	}
	if !strings.Contains(lower, "ok") {
		t.Fatalf("benign body lost: %s", out)
	}
}

func TestSanitizeDangerousAndBenignURIs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		absent  []string
		present []string
	}{
		{
			name:    "mixed-case javascript href",
			in:      `<a href="JavaScript:alert(1)">x</a>`,
			absent:  []string{"javascript:", "alert(1)"},
			present: []string{">x<"},
		},
		{
			name:    "control-char padded javascript",
			in:      "<a href=\"java\x00script:alert(1)\">x</a>",
			absent:  []string{"javascript:", "alert(1)"},
			present: []string{">x<"},
		},
		{
			name:    "whitespace padded javascript",
			in:      "<a href=\"java\nscript:alert(1)\">x</a>",
			absent:  []string{"javascript:", "alert(1)"},
			present: []string{">x<"},
		},
		{
			name:    "data text/html dropped",
			in:      `<a href="data:text/html,<script>x</script>">x</a>`,
			absent:  []string{"data:text/html", "script"},
			present: []string{">x<"},
		},
		{
			name:    "data application/javascript dropped",
			in:      `<a href="data:application/javascript,alert(1)">x</a>`,
			absent:  []string{"data:application/javascript", "alert(1)"},
			present: []string{">x<"},
		},
		{
			name:    "benign data image kept",
			in:      `<img src="data:image/png;base64,AAAA">`,
			present: []string{"data:image/png;base64,aaaa"}, // render may keep case; check via Contains fold below
		},
		{
			name:    "relative href kept",
			in:      `<a href="chap.xhtml#s">go</a>`,
			present: []string{"chap.xhtml#s", ">go<"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := strings.ToLower(sanitizeHTML(t, tc.in))
			for _, needle := range tc.absent {
				if strings.Contains(out, strings.ToLower(needle)) {
					t.Errorf("still contains %q\noutput: %s", needle, out)
				}
			}
			for _, needle := range tc.present {
				if !strings.Contains(out, strings.ToLower(needle)) {
					t.Errorf("missing %q\noutput: %s", needle, out)
				}
			}
		})
	}
}

func TestSanitizeDepthFailClosed(t *testing.T) {
	t.Parallel()

	// html.Parse rejects open stacks > 512 nodes, so build the deep tree by hand.
	// Fail-closed must prune the over-depth subtree so the script never survives.
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	cur := root
	for range maxSanitizeDepth + 10 {
		child := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
		cur.AppendChild(child)
		cur = child
	}
	script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: "deep()"})
	cur.AppendChild(script)
	span := &html.Node{Type: html.ElementNode, Data: "span", DataAtom: atom.Span}
	span.AppendChild(&html.Node{Type: html.TextNode, Data: "leaf"})
	cur.AppendChild(span)

	Sanitize(root)

	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := strings.ToLower(b.String())
	if strings.Contains(out, "deep()") || strings.Contains(out, "<script") {
		t.Fatalf("over-depth script survived: %s", truncateForTest(out, 400))
	}
}

func TestSanitizeAttributesInPlace(t *testing.T) {
	t.Parallel()

	// Direct node check: dangerous href removed, safe class kept.
	doc, err := html.Parse(strings.NewReader(`<a class="c" href="javascript:x" id="i">t</a>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	Sanitize(doc)
	var a *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.A {
			a = n
			return
		}
		for c := n.FirstChild; c != nil && a == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if a == nil {
		t.Fatal("anchor not found")
	}
	hasClass, hasID, hasHref := false, false, false
	for _, attr := range a.Attr {
		switch strings.ToLower(attr.Key) {
		case "class":
			hasClass = attr.Val == "c"
		case "id":
			hasID = attr.Val == "i"
		case "href":
			hasHref = true
		}
	}
	if !hasClass || !hasID {
		t.Fatalf("lost safe attrs: %+v", a.Attr)
	}
	if hasHref {
		t.Fatalf("dangerous href kept: %+v", a.Attr)
	}
}

func truncateForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestContentTypeByExt(t *testing.T) {
	t.Parallel()
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
