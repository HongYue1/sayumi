package epub

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func TestFindOPFPathSelection(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		rootfiles string
		want      string
	}{
		{
			name: "media type before extension fallback",
			rootfiles: `<rootfile full-path="decoy.opf" media-type="text/xml"/>` +
				`<rootfile full-path="OPS/book.xml" media-type="application/oebps-package+xml"/>`,
			want: "OPS/book.xml",
		},
		{
			name: "extension before arbitrary fallback",
			rootfiles: `<rootfile full-path="other.xml"/>` +
				`<rootfile full-path=" OPS/BOOK.OPF "/>`,
			want: "OPS/BOOK.OPF",
		},
		{
			name: "first package wins",
			rootfiles: `<rootfile full-path="first.opf" media-type="application/oebps-package+xml"/>` +
				`<rootfile full-path="second.opf" media-type="application/oebps-package+xml"/>`,
			want: "first.opf",
		},
		{
			name: "blank rootfile is ignored",
			rootfiles: `<rootfile full-path=" " media-type="application/oebps-package+xml"/>` +
				`<rootfile full-path="fallback.xml"/>`,
			want: "fallback.xml",
		},
		{name: "no usable rootfile", rootfiles: `<rootfile full-path=" "/>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			index := testZipIndex(t, map[string]string{
				"META-INF/container.xml": "<container><rootfiles>" + tt.rootfiles + "</rootfiles></container>",
			})
			got, err := findOPFPath(index)
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("findOPFPath = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestParseOPFMetadataAndSpine(t *testing.T) {
	t.Parallel()
	data := []byte(`<package xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
<metadata><dc:title> </dc:title><dc:title> Title </dc:title>
<dc:creator opf:file-as=" Stored Author " opf:role="aut">Display Author</dc:creator>
<dc:language> en </dc:language><dc:publisher> Publisher </dc:publisher>
<dc:description><![CDATA[<p>Summary</p>]]></dc:description><dc:date> 2026-01-01 </dc:date>
<dc:identifier>not-an-isbn</dc:identifier><dc:identifier opf:scheme="ISBN"> 978-0-306-40615-7 </dc:identifier>
<meta property="dcterms:modified">2026-01-02T00:00:00Z</meta></metadata>
<manifest><item id="one" href="one.xhtml" media-type="application/xhtml+xml"/>
<item id="two" href="two.xhtml" media-type="application/xhtml+xml"/>
<item id="empty" href=" "/><item href="no-id.xhtml"/></manifest>
<spine><itemref idref="missing"/><itemref idref="empty"/><itemref idref="two" linear="no"/>
<itemref idref="one"/></spine></package>`)
	meta, _, err := parseOPF(data, "OPS")
	if err != nil {
		t.Fatal(err)
	}
	want := BookMeta{
		Title: "Title", Author: "Stored Author", Language: "en", Publisher: "Publisher",
		Description: "<p>Summary</p>", PubDate: "2026-01-01", ISBN: "978-0-306-40615-7", Direction: "ltr",
		Spine: []SpineEntry{
			{Href: "OPS/two.xhtml", ID: "two", MediaType: "application/xhtml+xml", Linear: false},
			{Href: "OPS/one.xhtml", ID: "one", MediaType: "application/xhtml+xml", Linear: true},
		},
	}
	if !reflect.DeepEqual(meta, want) {
		t.Fatalf("metadata = %+v; want %+v", meta, want)
	}
}

func TestParseOPFSpineDirectionPrecedence(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, packageDir, spineDir, want string }{
		{name: "explicit ltr", packageDir: "rtl", spineDir: "ltr", want: "ltr"},
		{name: "explicit rtl", packageDir: "ltr", spineDir: "rtl", want: "rtl"},
		{name: "case insensitive", packageDir: "RTL", spineDir: "LTR", want: "ltr"},
		{name: "legacy fallback", packageDir: "rtl", want: "rtl"},
		{name: "default fallback", packageDir: "rtl", spineDir: "default", want: "rtl"},
		{name: "default ltr", want: "ltr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opf := fmt.Sprintf(
				`<package dir=%q><spine page-progression-direction=%q/></package>`,
				tt.packageDir, tt.spineDir,
			)
			meta, _, err := parseOPF([]byte(opf), "OPS")
			if err != nil || meta.Direction != tt.want {
				t.Fatalf("direction = %q, %v; want %q", meta.Direction, err, tt.want)
			}
		})
	}
}

func TestFindCoverPathSkipsEmptyReferences(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, metadata, manifest string }{
		{
			name:     "legacy metadata",
			metadata: `<meta name="cover" content="empty"/>`,
			manifest: `<item id="empty" href=" " media-type="image/png"/>` +
				`<item id="actual" href="cover.png" properties="cover-image" media-type="image/png"/>`,
		},
		{
			name: "properties",
			manifest: `<item id="empty" href=" " properties="cover-image" media-type="image/png"/>` +
				`<item id="actual" href="cover.png" properties="cover-image" media-type="image/png"/>`,
		},
		{
			name: "ID fallback",
			manifest: `<item id="cover-empty" href=" " media-type="image/png"/>` +
				`<item id="cover-actual" href="cover.png" media-type="image/png"/>`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opf := "<package><metadata>" + tt.metadata + "</metadata><manifest>" + tt.manifest + "</manifest></package>"
			meta, _, err := parseOPF([]byte(opf), "OPS")
			if err != nil || meta.CoverPath != "OPS/cover.png" {
				t.Fatalf("cover = %q, %v; want OPS/cover.png", meta.CoverPath, err)
			}
		})
	}
}

func TestParseNavHTMLSpanWrappedLinks(t *testing.T) {
	t.Parallel()
	for _, wrapper := range []string{"span", "div", "p"} {
		t.Run(wrapper, func(t *testing.T) {
			nav := `<nav epub:type="toc"><ol><li><` + wrapper + `>` +
				`<span><a href="ch.xhtml#part"><em>Chapter</em></a></span></` + wrapper + `>` +
				`<div><ol><li><span><a href="child.xhtml">Child</a></span></li></ol></div></li>` +
				`<li><span>Heading</span><ol><li><a href="last.xhtml">Last</a></li></ol></li></ol></nav>`
			got := parseNavHTML([]byte(nav), "OPS/nav.xhtml")
			want := []TocEntry{
				{
					Title:    "Chapter",
					Href:     "OPS/ch.xhtml#part",
					Children: []TocEntry{{Title: "Child", Href: "OPS/child.xhtml", Depth: 1}},
				},
				{Title: "Heading", Children: []TocEntry{{Title: "Last", Href: "OPS/last.xhtml", Depth: 1}}},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("TOC = %+v; want %+v", got, want)
			}
		})
	}
}

func TestResolveArchiveRootReferences(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, href, wantPath, wantRef string }{
		{name: "root path", href: "/Images/cover.png", wantPath: "Images/cover.png", wantRef: "Images/cover.png"},
		{
			name: "root path and suffix", href: "/Text/ch.xhtml?q=1#part",
			wantPath: "Text/ch.xhtml", wantRef: "Text/ch.xhtml?q=1#part",
		},
		{
			name: "relative traversal stays inside archive", href: "../../../Text/ch.xhtml",
			wantPath: "Text/ch.xhtml", wantRef: "Text/ch.xhtml",
		},
		{
			name: "escaped delimiters stay escaped", href: "a%23b.xhtml?x=%3F#part",
			wantPath: "OPS/a%23b.xhtml", wantRef: "OPS/a%23b.xhtml?x=%3F#part",
		},
		{name: "query on same document", href: "?q=1#part", wantRef: "OPS/nav.xhtml?q=1#part"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePath("OPS", tt.href); got != tt.wantPath {
				t.Errorf("path = %q; want %q", got, tt.wantPath)
			}
			if got := resolveReference("OPS/nav.xhtml", tt.href); got != tt.wantRef {
				t.Errorf("reference = %q; want %q", got, tt.wantRef)
			}
		})
	}
}

func TestParseOptionalNavigationFallback(t *testing.T) {
	t.Parallel()
	const validNCX = `<ncx><navMap><navPoint><navLabel><text>NCX</text></navLabel>` +
		`<content src="ch.xhtml"/></navPoint></navMap></ncx>`
	for _, tt := range []struct{ name, nav, ncx, wantTitle string }{
		{
			name: "NAV preferred",
			nav:  `<nav role="doc-toc"><ol><li><a href="ch.xhtml">NAV</a></li></ol></nav>`,
			ncx:  validNCX, wantTitle: "NAV",
		},
		{name: "NCX fallback", nav: `<p>No navigation</p>`, ncx: validNCX, wantTitle: "NCX"},
		{name: "invalid optional navigation", nav: `<p>No navigation</p>`, ncx: `<ncx><navMap>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{
				"META-INF/container.xml": `<container><rootfiles>` +
					`<rootfile full-path="OPS/book.opf"/></rootfiles></container>`,
				"OPS/book.opf": `<package><manifest><item id="nav" href="nav.xhtml" properties="nav"/>` +
					`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` +
					`</manifest><spine toc="ncx"/></package>`,
				"OPS/nav.xhtml": tt.nav,
				"OPS/toc.ncx":   tt.ncx,
			}
			rc, err := zip.OpenReader(writeTestEPUB(t, files))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := rc.Close(); err != nil {
					t.Error(err)
				}
			})
			meta, err := Parse(&rc.Reader)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Spine == nil || meta.TOC == nil {
				t.Fatalf("empty collections must not be null: %+v", meta)
			}
			if tt.wantTitle == "" {
				if len(meta.TOC) != 0 {
					t.Fatalf("invalid navigation = %+v", meta.TOC)
				}
			} else if len(meta.TOC) != 1 || meta.TOC[0].Title != tt.wantTitle {
				t.Fatalf("TOC = %+v; want %s", meta.TOC, tt.wantTitle)
			}
			encoded, err := json.Marshal(meta)
			if err != nil || !bytes.Contains(encoded, []byte(`"spine":[]`)) {
				t.Fatalf("JSON collection contract: %s, %v", encoded, err)
			}
		})
	}
}

func TestNodeTextBoundedSubtree(t *testing.T) {
	t.Parallel()
	parent := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Span, Data: "span"}
	parent.AppendChild(root)
	parent.AppendChild(&html.Node{Type: html.TextNode, Data: "outside"})
	root.AppendChild(&html.Node{Type: html.TextNode, Data: "first "})
	cur := root
	for range maxSanitizeDepth - 1 {
		child := &html.Node{Type: html.ElementNode, DataAtom: atom.Span, Data: "span"}
		cur.AppendChild(child)
		cur = child
	}
	cur.AppendChild(&html.Node{Type: html.TextNode, Data: "boundary "})
	tooDeep := &html.Node{Type: html.ElementNode, DataAtom: atom.Span, Data: "span"}
	cur.AppendChild(tooDeep)
	tooDeep.AppendChild(&html.Node{Type: html.TextNode, Data: "excluded"})
	root.AppendChild(&html.Node{Type: html.TextNode, Data: "last"})
	if got := nodeText(root); got != "first boundary last" {
		t.Fatalf("text = %q", got)
	}
	if got := nodeText(nil); got != "" {
		t.Fatalf("nil text = %q", got)
	}
}

func TestNavigationDepthBounds(t *testing.T) {
	t.Parallel()
	const levels = maxTOCDepth + 2
	nav := `<nav role="doc-toc">` +
		strings.Repeat(`<ol><li><a href="ch.xhtml">Chapter</a>`, levels) +
		strings.Repeat("</li></ol>", levels) + "</nav>"
	ncx := `<ncx><navMap>` +
		strings.Repeat(`<navPoint><navLabel><text>Chapter</text></navLabel><content src="ch.xhtml"/>`, levels) +
		strings.Repeat("</navPoint>", levels) + "</navMap></ncx>"
	for _, tt := range []struct {
		name string
		toc  []TocEntry
	}{
		{name: "NAV", toc: parseNavHTML([]byte(nav), "OPS/nav.xhtml")},
		{name: "NCX", toc: parseNCXData([]byte(ncx), "OPS/toc.ncx")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entries := tt.toc
			for depth := 0; depth <= maxTOCDepth; depth++ {
				if len(entries) != 1 || entries[0].Depth != depth || entries[0].Href != "OPS/ch.xhtml" {
					t.Fatalf("depth %d: entries = %+v", depth, entries)
				}
				entries = entries[0].Children
			}
			if len(entries) != 0 {
				t.Fatalf("navigation exceeded maximum depth: %+v", entries)
			}
		})
	}
}

func TestNavSpanWrapperDepthBounds(t *testing.T) {
	t.Parallel()
	for _, depth := range []int{maxSanitizeDepth, maxSanitizeDepth + 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			nav := `<nav role="doc-toc"><ol><li>` + strings.Repeat("<span>", depth) +
				`<a href="ch.xhtml">Chapter</a>` + strings.Repeat("</span>", depth) + "</li></ol></nav>"
			got := parseNavHTML([]byte(nav), "OPS/nav.xhtml")
			if depth == maxSanitizeDepth {
				if len(got) != 1 || got[0].Href != "OPS/ch.xhtml" {
					t.Fatalf("in-bound wrapper lost link: %+v", got)
				}
			} else {
				for _, entry := range got {
					if entry.Href != "" {
						t.Fatalf("out-of-bound wrapper exposed link: %+v", got)
					}
				}
			}
		})
	}
}

func FuzzNodeTextMatchesBoundedWalk(f *testing.F) {
	f.Add(`<div>one <span>two</span> three</div>`)
	f.Add(`<style>body { color: red; }</style>`)
	f.Add(strings.Repeat("<div>", maxSanitizeDepth+5) + "hidden")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		doc, err := html.Parse(strings.NewReader(input))
		if err != nil {
			return
		}
		var want strings.Builder
		var visit func(*html.Node, int)
		visit = func(n *html.Node, depth int) {
			if depth > maxSanitizeDepth {
				return
			}
			if n.Type == html.TextNode {
				want.WriteString(n.Data)
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				visit(child, depth+1)
			}
		}
		visit(doc, 0)
		if got := nodeText(doc); got != want.String() {
			t.Fatalf("text = %q; want %q", got, want.String())
		}
	})
}
