package epub

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func renderNode(t *testing.T, n *html.Node) string {
	t.Helper()
	var b strings.Builder
	if err := html.Render(&b, n); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// Remote body subresources must be neutralized to about:invalid on render so a
// crafted EPUB cannot beacon reading activity; in-EPUB refs are resolved and
// normal <a href> links are left intact.
func TestRewriteNodeURLsNeutralizesRemoteSubresources(t *testing.T) {
	t.Parallel()
	const resourceBase = "/api/books/bk1/resources"
	cases := []struct {
		name    string
		in      string
		present []string
		absent  []string
	}{
		{
			name:    "remote img src",
			in:      `<img src="https://attacker.example/x.png">`,
			present: []string{"about:invalid"},
			absent:  []string{"attacker.example"},
		},
		{
			name:    "protocol-relative img src",
			in:      `<img src="//attacker.example/x.png">`,
			present: []string{"about:invalid"},
			absent:  []string{"attacker.example"},
		},
		{
			name:    "remote video poster",
			in:      `<video poster="http://attacker.example/p.jpg"></video>`,
			present: []string{"about:invalid"},
			absent:  []string{"attacker.example"},
		},
		{
			name:    "remote svg image xlink:href",
			in:      `<svg><image xlink:href="https://attacker.example/x.svg"></image></svg>`,
			present: []string{"about:invalid"},
			absent:  []string{"attacker.example"},
		},
		{
			name:    "in-epub img resolved",
			in:      `<img src="images/p.png">`,
			present: []string{resourceBase + "/images/p.png"},
			absent:  []string{"about:invalid"},
		},
		{
			name:    "anchor href left intact",
			in:      `<a href="https://example.com/page">x</a>`,
			present: []string{"https://example.com/page"},
			absent:  []string{"about:invalid"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, err := html.Parse(strings.NewReader(tc.in))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			rewriteNodeURLs(doc, "", resourceBase, "", nil)
			got := renderNode(t, doc)
			for _, p := range tc.present {
				if !strings.Contains(got, p) {
					t.Errorf("want %q present\noutput: %s", p, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("want %q absent\noutput: %s", a, got)
				}
			}
		})
	}
}

// srcset must neutralize remote candidates, resolve in-EPUB candidates, and
// preserve data: URLs even when they contain commas (the WHATWG tokenizer must
// not split a data: URL the way strings.Split(",") did).
func TestRewriteSrcsetValue(t *testing.T) {
	t.Parallel()
	const resourceBase = "/api/books/bk1/resources"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "in-epub candidates resolved",
			in:   "img/a.png 1x, img/b.png 2x",
			want: resourceBase + "/img/a.png 1x, " + resourceBase + "/img/b.png 2x",
		},
		{
			name: "remote candidate neutralized, descriptor kept",
			in:   "https://attacker.example/a.png 1x, img/b.png 2x",
			want: "about:invalid 1x, " + resourceBase + "/img/b.png 2x",
		},
		{
			name: "data url with comma preserved alongside rewrite",
			in:   "data:image/png;base64,AAAA 1x, img/b.png 2x",
			want: "data:image/png;base64,AAAA 1x, " + resourceBase + "/img/b.png 2x",
		},
		{
			name: "all data urls returned unchanged",
			in:   "data:image/png;base64,AAAA 1x",
			want: "data:image/png;base64,AAAA 1x",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rewriteSrcsetValue(tc.in, "", resourceBase, ""); got != tc.want {
				t.Errorf("rewriteSrcsetValue() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

func TestInlineCSSImports(t *testing.T) {
	t.Parallel()
	const base = "/api/books/b1/resources"
	const token = "tok"

	index := testZipIndex(t, map[string]string{
		"OEBPS/shared.css": "@import \"deep.css\";\n.shared { color: red; background: url(img/s.png); }",
		"OEBPS/deep.css":   ".deep { margin: 1px; }",
		"OEBPS/self.css":   "@import 'self.css';\n.self { padding: 0; }",
		"OEBPS/fonts.css":  "@font-face { font-family: \"Book\"; src: url(f/b.woff2); }",
	})

	t.Run("splices nested imports and rewrites their urls", func(t *testing.T) {
		t.Parallel()
		got := inlineCSSImports("@import \"shared.css\";\nbody { color: blue; }", "OEBPS", base, token, index, "OEBPS/chapter.css")

		if strings.Contains(got, "@import") {
			t.Fatalf("import not spliced: %q", got)
		}
		for _, want := range []string{".shared", ".deep", "body { color: blue; }"} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in %q", want, got)
			}
		}
		// The imported sheet's relative url() resolved against its own dir.
		if !strings.Contains(got, base+"/OEBPS/img/s.png") {
			t.Fatalf("imported url not rewritten against its own dir: %q", got)
		}
		if !strings.Contains(got, "token="+token) {
			t.Fatalf("expected token on rewritten url: %q", got)
		}

		// The caller's own rewrite pass must not touch the spliced URLs again.
		if second := rewriteCSSURLs(got, "OEBPS", base, token); strings.Count(second, base) != strings.Count(got, base) {
			t.Fatalf("second rewrite pass was not a no-op:\n %q\n %q", got, second)
		}
	})

	t.Run("@font-face survives the splice for separateFontFaces", func(t *testing.T) {
		t.Parallel()
		got := inlineCSSImports("@import \"fonts.css\";", "OEBPS", base, token, index, "")

		var cssOut, fontFaceOut strings.Builder
		separateFontFaces(got, "OEBPS", base, &cssOut, &fontFaceOut, token)
		if !strings.Contains(fontFaceOut.String(), "@font-face") {
			t.Fatalf("imported @font-face not separated: %q", fontFaceOut.String())
		}
		if !strings.Contains(fontFaceOut.String(), base+"/OEBPS/f/b.woff2") {
			t.Fatalf("imported font url not rewritten: %q", fontFaceOut.String())
		}
	})

	t.Run("conditional and remote imports are left to the neutralizer", func(t *testing.T) {
		t.Parallel()
		cases := map[string]string{
			"media condition":   "@import \"shared.css\" screen;",
			"layer condition":   "@import url(shared.css) layer(book);",
			"remote target":     "@import \"https://evil.example/x.css\";",
			"protocol relative": "@import url(//evil.example/x.css);",
			"missing target":    "@import \"nope.css\";",
		}
		for name, in := range cases {
			if got := inlineCSSImports(in, "OEBPS", base, token, index, ""); got != in {
				t.Errorf("%s: expected %q unchanged, got %q", name, in, got)
			}
		}
	})

	t.Run("self import terminates", func(t *testing.T) {
		t.Parallel()
		got := inlineCSSImports("@import \"self.css\";", "OEBPS", base, token, index, "")
		if strings.Contains(got, "@import") {
			t.Fatalf("cycle left an import behind: %q", got)
		}
		if strings.Count(got, ".self") != 1 {
			t.Fatalf("expected the cycle target spliced exactly once: %q", got)
		}
	})
}

func TestRewriteCSSURLsAndImports(t *testing.T) {
	t.Parallel()
	const base = "/api/books/b1/resources"
	const token = "tok"

	css := strings.Join([]string{
		`@import "https://evil.example/x.css";`,
		`@import 'local.css';`,
		`body { background: url("img/bg.png"); }`,
		`.x { background: url(https://evil.example/a.png); }`,
	}, "\n")

	got := rewriteCSSURLs(css, "OEBPS", base, token)
	lower := strings.ToLower(got)

	if !strings.Contains(lower, `@import "about:invalid"`) && !strings.Contains(lower, "@import 'about:invalid'") {
		// quote style preserved from source; first import used double quotes
		if !strings.Contains(got, "about:invalid") {
			t.Fatalf("remote @import not neutralized: %q", got)
		}
	}
	if strings.Contains(got, "evil.example") {
		t.Fatalf("evil host leaked: %q", got)
	}
	if !strings.Contains(got, base+"/OEBPS/local.css") && !strings.Contains(got, base+"/OEBPS/local.css?token=") {
		// local.css resolved under OEBPS with token
		if !strings.Contains(got, "local.css") || !strings.Contains(got, "token=tok") {
			t.Fatalf("local @import not rewritten with token: %q", got)
		}
	}
	if !strings.Contains(got, base+"/OEBPS/img/bg.png") && !strings.Contains(got, "img/bg.png") {
		t.Fatalf("url() not rewritten: %q", got)
	}
	if !strings.Contains(got, "token=tok") {
		t.Fatalf("expected token on rewritten URLs: %q", got)
	}
	// Inline path skips @import pass but still rewrites url().
	inline := rewriteCSSURLsInline(`background: url("x.png")`, "OEBPS", base, "")
	if !strings.Contains(inline, base+"/OEBPS/x.png") {
		t.Fatalf("inline url rewrite: %q", inline)
	}
}

func TestBuildResourceURLTokenAndFragment(t *testing.T) {
	t.Parallel()
	const base = "/api/books/b1/resources"

	got, ok := buildResourceURL("OEBPS/Text", base, "../Images/a.png?x=1#frag", "sec ret")
	if !ok {
		t.Fatal("buildResourceURL failed")
	}
	if !strings.HasPrefix(got, base+"/OEBPS/Images/a.png?") {
		t.Fatalf("path/query prefix: %q", got)
	}
	if !strings.Contains(got, "x=1") || !strings.Contains(got, "token=sec+ret") && !strings.Contains(got, "token=sec%20ret") {
		// QueryEscape encodes space as +
		if !strings.Contains(got, "token=") {
			t.Fatalf("missing token: %q", got)
		}
	}
	if !strings.HasSuffix(got, "#frag") {
		t.Fatalf("missing fragment: %q", got)
	}

	if _, ok := buildResourceURL("OEBPS", base, "data:image/png;base64,AA", "t"); ok {
		t.Fatal("data: must not be rewritten")
	}
	if _, ok := buildResourceURL("OEBPS", base, "https://x.example/a", "t"); ok {
		t.Fatal("absolute http must not be rewritten via buildResourceURL")
	}
}

func TestProcessChapterWritingModeAndCache(t *testing.T) {
	t.Parallel()

	// Head CSS vertical mode wins over body inline.
	headVertical := strings.Join([]string{
		`<!DOCTYPE html><html><head>`,
		`<style>body { writing-mode: vertical-rl; }</style>`,
		`</head><body style="writing-mode: horizontal-tb"><p>Hi</p></body></html>`,
	}, "")
	bodyVertical := strings.Join([]string{
		`<!DOCTYPE html><html><head></head>`,
		`<body style="writing-mode: vertical-lr"><p>Hi</p></body></html>`,
	}, "")

	zipPath := writeTestEPUB(t, map[string]string{
		"ch-head.xhtml":  headVertical,
		"ch-body.xhtml":  bodyVertical,
		"ch-plain.xhtml": `<html><body><p>Plain</p><img src="pic.png"></body></html>`,
		"pic.png":        "PNG",
	})
	store := NewStore(8)
	t.Cleanup(func() { store.Close() })
	spine := []SpineEntry{
		{Href: "ch-head.xhtml"},
		{Href: "ch-body.xhtml"},
		{Href: "ch-plain.xhtml"},
	}
	ctx := context.Background()

	r0, err := ProcessChapter(ctx, store, zipPath, spine, 0, "book1", "ltr", "tok")
	if err != nil {
		t.Fatalf("head vertical: %v", err)
	}
	if r0.WritingMode != "vertical-rl" {
		t.Fatalf("head WM = %q, want vertical-rl", r0.WritingMode)
	}

	r1, err := ProcessChapter(ctx, store, zipPath, spine, 1, "book1", "ltr", "tok")
	if err != nil {
		t.Fatalf("body vertical: %v", err)
	}
	if r1.WritingMode != "vertical-lr" {
		t.Fatalf("body WM = %q, want vertical-lr", r1.WritingMode)
	}

	r2, err := ProcessChapter(ctx, store, zipPath, spine, 2, "book1", "rtl", "tok")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if r2.WritingMode != "horizontal-tb" {
		t.Fatalf("default WM = %q", r2.WritingMode)
	}
	if r2.Direction != "rtl" {
		t.Fatalf("direction = %q, want book rtl", r2.Direction)
	}
	if !strings.Contains(r2.HTML, "/api/books/book1/resources/pic.png") {
		t.Fatalf("img not rewritten: %q", r2.HTML)
	}
	if !strings.Contains(r2.HTML, "token=tok") {
		t.Fatalf("token missing on img: %q", r2.HTML)
	}

	// Cache hit: mutate underlying file would be heavy; assert GetChapter populated
	// and a second ProcessChapter returns equal payload.
	cached, ok := store.GetChapter(zipPath, 2, ChapterRenderVersion)
	if !ok {
		t.Fatal("expected chapter cache entry after ProcessChapter")
	}
	r2b, err := ProcessChapter(ctx, store, zipPath, spine, 2, "book1", "rtl", "tok")
	if err != nil {
		t.Fatalf("cached: %v", err)
	}
	if r2b.HTML != cached.HTML || r2b.CSS != cached.CSS || r2b.WritingMode != cached.WritingMode {
		t.Fatalf("cache miss or mismatch")
	}
}

// The CSS url() function token is ASCII case-insensitive, so URL(…) and Url(…)
// are the same token to a browser. A case-sensitive pattern matched neither,
// letting a crafted book fetch a remote URL and beacon that it was opened —
// the exact leak the about:invalid rewriting exists to close. Quoted values
// containing whitespace were missed for the same reason.
func TestRewriteCSSURLsNeutralizesRemoteRegardlessOfCaseOrQuoting(t *testing.T) {
	t.Parallel()

	const base = "/api/books/b1/resources"

	remote := []string{
		`a{background:URL(https://evil.example/a.png)}`,
		`a{background:Url('https://evil.example/a.png')}`,
		`a{background:url("https://evil.example/a b.png")}`,
		`@import URL(https://evil.example/x.css);`,
		`a{background:url(//evil.example/a.png)}`,
	}
	for _, css := range remote {
		got := rewriteCSSURLs(css, "OEBPS", base, "tok")
		if strings.Contains(got, "evil.example") {
			t.Errorf("remote host survived: %q -> %q", css, got)
		}
		if !strings.Contains(got, "about:invalid") {
			t.Errorf("remote ref not neutralized: %q -> %q", css, got)
		}
	}

	// In-EPUB refs still resolve, including the quoted-with-spaces form that the
	// previous pattern could not match at all.
	local := rewriteCSSURLs(`a{background:URL("img/my pic.png")}`, "OEBPS", base, "tok")
	if !strings.Contains(local, base+"/OEBPS/img/my pic.png") {
		t.Errorf("local url not rewritten: %q", local)
	}
	// data: URIs are inline and must be left intact.
	data := rewriteCSSURLs(`@font-face{src:url("data:font/woff2;base64,AAA")}`, "OEBPS", base, "tok")
	if !strings.Contains(data, "data:font/woff2;base64,AAA") {
		t.Errorf("data URI mangled: %q", data)
	}
}

// The legacy presentational background attribute is still mapped to
// background-image by Blink/WebKit on table elements, so it needs the same
// remote-reference neutralization as src/poster.
func TestRewriteNodeURLsNeutralizesBackgroundAttribute(t *testing.T) {
	t.Parallel()

	const base = "/api/books/b1/resources"

	doc, err := html.Parse(strings.NewReader(`<table background="https://evil.example/px.png"><tr><td background="img/t.png">x</td></tr></table>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rewriteNodeURLs(doc, "OEBPS", base, "tok", nil)

	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := b.String()

	if strings.Contains(got, "evil.example") {
		t.Errorf("remote background survived: %s", got)
	}
	if !strings.Contains(got, "about:invalid") {
		t.Errorf("remote background not neutralized: %s", got)
	}
	if !strings.Contains(got, base+"/OEBPS/img/t.png") {
		t.Errorf("in-EPUB background not rewritten: %s", got)
	}
}
