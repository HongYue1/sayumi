package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func BenchmarkParseEPUB(b *testing.B) {
	for _, tt := range []struct {
		name     string
		chapters int
		assets   int
		ncx      bool
	}{
		{name: "NAV20", chapters: 20, assets: 100},
		{name: "NAV500", chapters: 500, assets: 1500},
		{name: "NCX500", chapters: 500, assets: 1500, ncx: true},
	} {
		b.Run(tt.name, func(b *testing.B) {
			zr := benchmarkEPUB(
				b,
				tt.chapters,
				tt.assets,
				tt.ncx,
			)
			meta, err := Parse(zr)
			if err != nil || len(meta.Spine) != tt.chapters || len(meta.TOC) != tt.chapters {
				b.Fatalf(
					"fixture: spine=%d, toc=%d, err=%v",
					len(meta.Spine),
					len(meta.TOC),
					err,
				)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Parse(zr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNodeText(b *testing.B) {
	for _, tt := range []struct{ name, source, want string }{
		{
			name:   "style",
			source: "<style>" + strings.Repeat("p { color: red; }\n", 512) + "</style>",
			want:   strings.Repeat("p { color: red; }\n", 512),
		},
		{
			name:   "inline",
			source: "<p>" + strings.Repeat("<span>word </span>", 8) + "</p>",
			want:   strings.Repeat("word ", 8),
		},
		{
			name:   "wide",
			source: "<p>" + strings.Repeat("<span>word </span>", 2048) + "</p>",
			want:   strings.Repeat("word ", 2048),
		},
		{
			name:   "deep",
			source: strings.Repeat("<div>", 200) + "text" + strings.Repeat("</div>", 200),
			want:   "text",
		},
	} {
		b.Run(tt.name, func(b *testing.B) {
			doc, err := html.Parse(strings.NewReader(tt.source))
			if err != nil {
				b.Fatal(err)
			}
			if got := nodeText(doc); got != tt.want {
				b.Fatalf("fixture text = %q", got)
			}
			b.ReportAllocs()
			for b.Loop() {
				nodeText(doc)
			}
		})
	}
}

func benchmarkEPUB(b *testing.B, chapters, assets int, ncx bool) *zip.Reader {
	b.Helper()
	var opf, spine, nav strings.Builder
	opf.WriteString(`<package><metadata><title>Book</title><creator file-as="Author" role="aut">Author</creator>`)
	opf.WriteString(`<meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest>`)
	if ncx {
		opf.WriteString(`<item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
		nav.WriteString(`<ncx><navMap>`)
	} else {
		opf.WriteString(`<item id="toc" href="nav.xhtml" properties="nav" media-type="application/xhtml+xml"/>`)
		nav.WriteString(`<html><body><nav epub:type="toc"><ol>`)
	}
	spine.WriteString(`<spine toc="toc">`)
	entries := []benchmarkZipEntry{
		{
			name: "META-INF/container.xml",
			body: `<container><rootfiles><rootfile full-path="OPS/book.opf" ` +
				`media-type="application/oebps-package+xml"/></rootfiles></container>`,
		},
	}
	for i := range chapters {
		fmt.Fprintf(
			&opf,
			`<item id="c%d" href="ch%d.xhtml" media-type="application/xhtml+xml"/>`,
			i,
			i,
		)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		if ncx {
			fmt.Fprintf(
				&nav,
				`<navPoint><navLabel><text>Chapter %d</text></navLabel><content src="ch%d.xhtml"/></navPoint>`,
				i,
				i,
			)
		} else {
			fmt.Fprintf(
				&nav,
				`<li><a href="ch%d.xhtml">Chapter <em>%d</em></a></li>`,
				i,
				i,
			)
		}
		entries = append(entries, benchmarkZipEntry{name: fmt.Sprintf("OPS/ch%d.xhtml", i), body: "<p>Chapter</p>"})
	}
	for i := range assets {
		entries = append(entries, benchmarkZipEntry{name: fmt.Sprintf("OPS/assets/%d.svg", i), body: "<svg/>"})
	}
	opf.WriteString("</manifest>")
	spine.WriteString("</spine></package>")
	opf.WriteString(spine.String())
	entries = append(entries, benchmarkZipEntry{name: "OPS/book.opf", body: opf.String()})
	if ncx {
		nav.WriteString("</navMap></ncx>")
		entries = append(entries, benchmarkZipEntry{name: "OPS/toc.ncx", body: nav.String()})
	} else {
		nav.WriteString("</ol></nav></body></html>")
		entries = append(entries, benchmarkZipEntry{name: "OPS/nav.xhtml", body: nav.String()})
	}
	return benchmarkZIP(b, entries, zip.Deflate)
}

type benchmarkZipEntry struct{ name, body string }

func benchmarkZIP(b *testing.B, entries []benchmarkZipEntry, method uint16) *zip.Reader {
	b.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: method})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.body); err != nil {
			b.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		b.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		b.Fatal(err)
	}
	return zr
}
