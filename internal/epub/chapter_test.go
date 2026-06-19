package epub

import (
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
			if got := rewriteSrcsetValue(tc.in, "", resourceBase, ""); got != tc.want {
				t.Errorf("rewriteSrcsetValue() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
