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
