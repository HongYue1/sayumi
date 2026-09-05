package epub

import (
	"fmt"
	"slices"
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

// <desc>, <title> and <foreignObject> are HTML integration points: the parser
// places real HTML-namespace elements inside an <svg> subtree. Before the
// shared element policy, sanitizeSVG only knew about script/foreignObject, so a
// meta refresh, iframe, form or base smuggled through an <svg><desc> wrapper
// survived untouched and the meta refresh navigated the reader frame off to a
// remote origin on chapter open.
func TestSanitizeSVGIntegrationPointAppliesHTMLPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc, in, forbidden string
	}{
		{"meta refresh", `<svg><desc><meta http-equiv="refresh" content="0;url=https://evil.example/"></desc></svg>`, "http-equiv"},
		{"iframe", `<svg><desc><iframe srcdoc="<script>x</script>"></iframe></desc></svg>`, "iframe"},
		{"form", `<svg><desc><form action="https://evil.example/"><input name="a"></form></desc></svg>`, "<form"},
		{"base", `<svg><desc><base href="https://evil.example/"></desc></svg>`, "<base"},
		{"nested script", `<svg><title><script>alert(1)</script></title></svg>`, "alert(1)"},
	}
	for _, c := range cases {
		out := strings.ToLower(sanitizeHTML(t, c.in))
		if strings.Contains(out, strings.ToLower(c.forbidden)) {
			t.Errorf("%s: %q survived inside an SVG integration point\noutput: %s", c.desc, c.forbidden, out)
		}
	}
}

// SMIL animation rewrites an attribute after the static attribute check has
// run, so <animate attributeName="href" values="javascript:…"> installs a
// script URL on a link whose href was itself perfectly benign.
func TestSanitizeStripsURLAnimation(t *testing.T) {
	t.Parallel()

	cases := []string{
		`<svg><a href="#x"><animate attributeName="href" values="javascript:alert(1)"/>t</a></svg>`,
		`<svg><a><set attributeName="href" to="javascript:alert(1)"/></a></svg>`,
		`<svg><a><set attributeName="xlink:href" to="javascript:alert(1)"/></a></svg>`,
	}
	for _, in := range cases {
		out := strings.ToLower(sanitizeHTML(t, in))
		if strings.Contains(out, "javascript:") {
			t.Errorf("URL animation survived: %s", out)
		}
	}

	// A geometry animation carries no URL, so it is left alone.
	out := sanitizeHTML(t, `<svg><rect><animate attributeName="width" values="0;100"/></rect></svg>`)
	if !strings.Contains(strings.ToLower(out), "animate") {
		t.Errorf("non-URL animation should be kept: %s", out)
	}
}

// At the cutoff the SVG root is retained as a leaf, just like an HTML element.
// Its own handlers and dangerous URIs must be removed even when its children
// cannot be visited. Cover both walker handoffs and an all-SVG ancestor chain.
func TestSanitizeSVGDepthBoundary(t *testing.T) {
	t.Parallel()
	for _, ancestry := range []string{"html", "svg", "integration"} {
		for _, depth := range []int{maxSanitizeDepth, maxSanitizeDepth + 1, maxSanitizeDepth + 2} {
			t.Run(fmt.Sprintf("%s/depth=%d", ancestry, depth), func(t *testing.T) {
				t.Parallel()
				doc := &html.Node{Type: html.DocumentNode}
				parent := doc
				for i := range depth - 1 {
					level := i + 1
					tag, namespace := "div", ""
					switch ancestry {
					case "svg":
						tag, namespace = "g", "svg"
						if level == 1 {
							tag = "svg"
						}
					case "integration":
						switch level {
						case 1:
							tag, namespace = "svg", "svg"
						case 2:
							tag, namespace = "desc", "svg"
						}
					}
					child := &html.Node{
						Type: html.ElementNode, DataAtom: atom.Lookup([]byte(tag)), Data: tag, Namespace: namespace,
					}
					parent.AppendChild(child)
					parent = child
				}
				edge := &html.Node{
					Type: html.ElementNode, DataAtom: atom.Svg, Data: "svg", Namespace: "svg",
					Attr: []html.Attribute{
						{Key: "id", Val: "edge"},
						{Key: "onload", Val: "drop()"},
						{Key: "href", Val: "javascript:drop()"},
					},
				}
				edge.AppendChild(&html.Node{Type: html.TextNode, Data: "inside"})
				parent.AppendChild(edge)
				doc.AppendChild(&html.Node{Type: html.TextNode, Data: "outside"})

				Sanitize(doc)
				assertSanitizedTreeLinks(t, doc)
				out := renderSanitizerTestTree(t, doc)
				wantEdge := depth <= maxSanitizeDepth+1
				if got := strings.Contains(out, `id="edge"`); got != wantEdge {
					t.Fatalf("retained boundary node = %v, want %v", got, wantEdge)
				}
				if wantEdge && !slices.Equal(edge.Attr, []html.Attribute{{Key: "id", Val: "edge"}}) {
					t.Errorf("boundary attributes = %+v; want only safe id", edge.Attr)
				}
				if got := strings.Contains(out, "inside"); got != (depth <= maxSanitizeDepth) {
					t.Errorf("boundary child retained = %v at depth %d", got, depth)
				}
				if !strings.HasSuffix(out, "outside") {
					t.Error("pruning lost a sibling outside the deep subtree")
				}
			})
		}
	}
}

func TestSanitizeParsedSVGDepthBoundary(t *testing.T) {
	t.Parallel()
	// This is within html.Parse's open-element limit: the bug is reachable
	// from a real chapter, not just an artificially constructed node tree.
	in := `<html><body>` + strings.Repeat(`<div>`, maxSanitizeDepth-2) +
		`<svg id="edge" onload="drop()" href="javascript:drop()"><g>inside</g></svg>` +
		strings.Repeat(`</div>`, maxSanitizeDepth-2) + `<p>outside</p></body></html>`
	store := NewStore(1)
	t.Cleanup(store.Close)
	resp, err := processChapterHTML(
		t.Context(),
		[]byte(in),
		"OPS",
		"/api/books/test/resources",
		0,
		"ltr",
		nil,
		store,
		"unused.epub",
		"token",
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"sanitizer": sanitizeHTML(t, in), "chapter": resp.HTML} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out, `id="edge"`) || !strings.Contains(out, `<p>outside</p>`) {
				t.Fatal("boundary leaf or outside content was lost")
			}
			for _, forbidden := range []string{"onload", "javascript:", "drop()", "inside"} {
				if strings.Contains(out, forbidden) {
					t.Errorf("%q survived the SVG cutoff", forbidden)
				}
			}
		})
	}
}

func TestSanitizePreservesBenignMarkup(t *testing.T) {
	t.Parallel()
	in := `<!DOCTYPE html><html lang="en"><head>` +
		`<meta name="viewport" content="width=device-width">` +
		`<link rel="alternate stylesheet" href="Styles/Book.CSS"></head><body class="book">` +
		`<p id="p1" data-note="Authored" contenteditable="true">Read <em>this</em> &amp; that.</p>` +
		`<a href="Text/Ch%20One.xhtml?x=1&amp;y=2#Part">go</a>` +
		`<svg viewBox="0 0 10 10"><title>Illustration</title><rect width="10">` +
		`<animate attributeName="width" values="0;10"></animate></rect>` +
		`<image xlink:href="../Images/Cover.PNG"></image></svg><math><mi>x</mi></math></body></html>`
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := renderSanitizerTestTree(t, doc)
	Sanitize(doc)
	assertSanitizedTreeLinks(t, doc)
	if got := renderSanitizerTestTree(t, doc); got != want {
		t.Errorf("benign markup changed\ngot:  %s\nwant: %s", got, want)
	}
}

func TestSanitizePromotedChildrenOrder(t *testing.T) {
	t.Parallel()
	content := `<form>before<button><span onclick="drop()">keep</span><script>drop()</script></button>after</form>`
	for _, tc := range []struct {
		name, in, want string
	}{
		{
			name: "html",
			in:   content + `<p>tail</p>`,
			want: `<html><head></head><body>before<span>keep</span>after<p>tail</p></body></html>`,
		},
		{
			name: "svg integration point",
			in:   `<svg><desc>` + content + `</desc></svg><p>tail</p>`,
			want: `<html><head></head><body><svg><desc>before<span>keep</span>after</desc></svg><p>tail</p></body></html>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, err := html.Parse(strings.NewReader(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			Sanitize(doc)
			assertSanitizedTreeLinks(t, doc)
			if got := renderSanitizerTestTree(t, doc); got != tc.want {
				t.Errorf("promoted content\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestNormalizeURIForSafetyCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{name: "empty"},
		{name: "lowercase path", in: "../images/a%20b.png?x=1#part", want: "../images/a%20b.png?x=1#part"},
		{name: "mixed case", in: "JaVaScRiPt:alert(1)", want: "javascript:alert(1)"},
		{name: "controls", in: "\x00java\t\n\r\x7fscript: x", want: "javascript:x"},
		{name: "non ASCII", in: "java\u00a0\u200b\ufffdscript:x", want: "javascript:x"},
		{name: "invalid UTF8", in: "java\xffscript:x", want: "javascript:x"},
		{name: "do not percent decode", in: "java%73cript:x", want: "java%73cript:x"},
		{name: "comparison only", in: "Images/日本語 Cover.PNG", want: "images/cover.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeURIForSafetyCheck(tc.in); got != tc.want {
				t.Errorf("normalized %q = %q, want %q", tc.in, got, tc.want)
			}
			if got := needsURINormalization(tc.in); got != (tc.in != tc.want) {
				t.Errorf("normalization needed for %q = %v", tc.in, got)
			}
		})
	}
}

func FuzzSanitizeStableTree(f *testing.F) {
	f.Add(`<form><button><span onclick="drop()">keep</span></button><script>drop()</script></form>`)
	f.Add(`<svg onload="drop()"><desc><form><a href="java&#10;script:x">keep</a></form></desc><image xlink:href="a.png"></image></svg>`)
	f.Add(`<math><mtext><svg><set attributeName="href" to="javascript:x"></set></svg></mtext></math>`)
	f.Add(strings.Repeat(`<div>`, maxSanitizeDepth-2) + `<svg onload="drop()"></svg>` + strings.Repeat(`</div>`, maxSanitizeDepth-2))
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		doc, err := html.Parse(strings.NewReader(input))
		if err != nil {
			t.Skip()
		}
		Sanitize(doc)
		assertSanitizedTreeLinks(t, doc)
		want := renderSanitizerTestTree(t, doc)
		Sanitize(doc)
		assertSanitizedTreeLinks(t, doc)
		if got := renderSanitizerTestTree(t, doc); got != want {
			t.Fatalf("sanitizing the same tree is not idempotent\ngot: %q\nwant: %q", truncateForTest(got, 400), truncateForTest(want, 400))
		}
	})
}

func renderSanitizerTestTree(t *testing.T, doc *html.Node) string {
	t.Helper()
	var out strings.Builder
	if err := html.Render(&out, doc); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Promotion and pruning must leave a valid bidirectional tree. The visited set
// also catches sibling cycles without hanging the regression or fuzz runner.
func assertSanitizedTreeLinks(t *testing.T, root *html.Node) {
	t.Helper()
	seen := map[*html.Node]bool{root: true}
	pending := []*html.Node{root}
	for len(pending) > 0 {
		n := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		var previous *html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if seen[c] {
				t.Fatal("cycle or multiply owned node after sanitizing")
			}
			seen[c] = true
			if c.Parent != n || c.PrevSibling != previous {
				t.Fatal("inconsistent parent or previous-sibling link")
			}
			pending = append(pending, c)
			previous = c
		}
		if n.LastChild != previous {
			t.Fatal("inconsistent last-child link")
		}
	}
}
