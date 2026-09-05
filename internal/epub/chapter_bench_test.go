package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkChapterProcess separates full response hits from chapter misses with
// warm/cold stylesheet fragments. ZIP readers and OS file caches are warmed;
// misses include decompression, rendering, publication, and the small explicit
// derived-cache deletion below, not cold filesystem I/O. Fixtures and validation
// stay outside timing. Keep this harness unchanged across paired executables.
func BenchmarkChapterProcess(b *testing.B) {
	plain := `<html><head></head><body>` + strings.Repeat(`<p>A quiet page of chapter text.</p>`, 64) + `</body></html>`
	rich := `<html><head><link rel="stylesheet" href="style.css"></head><body>` +
		strings.Repeat(`<p style="background:url('../Images/tile.png')">A paragraph.</p><img src="../Images/it's fine.png" srcset="../Images/a.png 1x, ../Images/b.png 2x">`, 64) + `</body></html>`

	for _, tc := range []struct {
		name        string
		html        string
		warmChapter bool
		warmCSS     bool
	}{
		{name: "WarmChapter", html: rich, warmChapter: true, warmCSS: true},
		{name: "PlainMiss", html: plain, warmCSS: true},
		{name: "ResourcesWarmCSS", html: rich, warmCSS: true},
		{name: "ResourcesColdCSS", html: rich},
	} {
		b.Run(tc.name, func(b *testing.B) {
			filePath := writeChapterBenchmarkEPUB(b, tc.html)
			store := NewStore(4)
			b.Cleanup(store.Close)
			spine := []SpineEntry{{Href: "OEBPS/Text/ch.xhtml"}}
			ctx := b.Context()
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			want, err := ProcessChapter(ctx, store, filePath, spine, 0, "bench", "ltr", token)
			if err != nil || want.HTML == "" {
				b.Fatalf("warmup: response=%+v error=%v", want, err)
			}
			key := chapterRenderKey{filePath: filePath, chapterIndex: 0, renderVersion: ChapterRenderVersion}
			var got ChapterResponse
			b.ReportAllocs()
			for b.Loop() {
				if !tc.warmChapter {
					store.chapters.Delete(key)
				}
				if !tc.warmCSS {
					store.cssFrags.Clear()
				}
				got, err = ProcessChapter(ctx, store, filePath, spine, 0, "bench", "ltr", token)
				if err != nil {
					b.Fatal(err)
				}
			}
			if got != want {
				b.Fatal("rendered response changed across iterations")
			}
		})
	}
}

func BenchmarkChapterResourceURL(b *testing.B) {
	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "Safe", ref: "../Images/cover.png"},
		{name: "Quoting", ref: `../Images/it's fine.png?label="hello"#part one`},
		{name: "EscapesAndQuery", ref: "../Images/a%20b%2Fc.png?token=stale&x=1#frag"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			const base = "/api/books/bench/resources"
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			want, ok := buildResourceURL("OEBPS/Text", base, tc.ref, token)
			if !ok || !strings.HasPrefix(want, base+"/OEBPS/Images/") {
				b.Fatalf("warmup: %q, %v", want, ok)
			}
			var got string
			b.ReportAllocs()
			for b.Loop() {
				got, ok = buildResourceURL("OEBPS/Text", base, tc.ref, token)
			}
			if !ok || got != want {
				b.Fatal("resource URL changed across iterations")
			}
		})
	}
}

func BenchmarkChapterCSSRewrite(b *testing.B) {
	css := strings.Repeat(`p { background: url("img/it's fine.png?token=stale"); mask: url(img/mask.svg#shape); }`+"\n", 64)
	const base = "/api/books/bench/resources"
	want := rewriteCSSURLs(css, "OEBPS", base, "current-token")
	if !strings.Contains(want, base+"/OEBPS/img/") {
		b.Fatal("warmup did not rewrite resource URLs")
	}
	var got string
	b.SetBytes(int64(len(css)))
	b.ReportAllocs()
	for b.Loop() {
		got = rewriteCSSURLs(css, "OEBPS", base, "current-token")
	}
	if got != want {
		b.Fatal("rewritten CSS changed across iterations")
	}
}

// The fixture is private to this frozen harness, with deterministic entry order
// and real DEFLATE members. It must not depend on mutable test helper behavior.
func writeChapterBenchmarkEPUB(b *testing.B, chapter string) string {
	b.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body string
	}{
		{name: "OEBPS/Text/ch.xhtml", body: chapter},
		{name: "OEBPS/Text/style.css", body: `@import "../Styles/shared.css"; @font-face { font-family: Book; src: url("../Fonts/book font.woff2"); } body { background: url("../Images/it's fine.png"); }`},
		{name: "OEBPS/Styles/shared.css", body: strings.Repeat(`p { color: #333; background-image: url("../Images/paper.png?x=1"); }`+"\n", 16)},
	} {
		w, err := zw.Create(entry.name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			b.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		b.Fatal(err)
	}
	filePath := filepath.Join(b.TempDir(), "book.epub")
	if err := os.WriteFile(filePath, buf.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	return filePath
}
