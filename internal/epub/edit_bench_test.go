package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep these fixtures self-contained and frozen for before/after comparisons.
// OPF cases include parsing, scanning, and splicing; archive cases also include
// temp creation, raw entry copying, Sync, Close, and removal. ZIP/OS caches are
// warm. Source creation, warmup, and semantic validation are outside timing.
const benchmarkEditOPF = `<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"><metadata><dc:title>Original</dc:title><dc:creator opf:file-as="Original Author">Original Author</dc:creator><meta name="cover" content="cover"/></metadata><manifest><item id="cover" href="cover.jpg" media-type="image/jpeg"/><item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch"/></spine></package>`

func BenchmarkRewriteOPF(b *testing.B) {
	title, author := "New title", "New author"
	jpegData := benchmarkEditJPEG(b)
	var items strings.Builder
	for i := range 2048 {
		fmt.Fprintf(&items, `<item id="r%d" href="r%d.xhtml" media-type="application/xhtml+xml"/>`, i, i)
	}
	large := strings.Replace(benchmarkEditOPF, "</manifest>", items.String()+"</manifest>", 1)
	noCover := strings.Replace(benchmarkEditOPF, `<meta name="cover" content="cover"/>`, "", 1)
	noCover = strings.Replace(noCover, `<item id="cover" href="cover.jpg" media-type="image/jpeg"/>`, "", 1)
	index := map[string]*zip.File{"OPS/cover.jpg": {FileHeader: zip.FileHeader{Name: "OPS/cover.jpg"}}}
	for _, tc := range []struct {
		name string
		opf  string
		edit MetadataEdit
	}{
		{"MetadataSmall", benchmarkEditOPF, MetadataEdit{Title: &title, Author: &author}},
		{"MetadataLarge", large, MetadataEdit{Title: &title, Author: &author}},
		{"CoverReplace", benchmarkEditOPF, MetadataEdit{CoverJPEG: jpegData}},
		{"CoverInsert", noCover, MetadataEdit{CoverJPEG: jpegData}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			data := []byte(tc.opf)
			out, cover, _, err := rewriteOPF(data, "OPS", tc.edit, index)
			if err != nil {
				b.Fatal(err)
			}
			checkBenchmarkEdit(b, out, tc.edit, cover)
			b.ReportAllocs()
			for b.Loop() {
				out, cover, _, err = rewriteOPF(data, "OPS", tc.edit, index)
				if err != nil {
					b.Fatal(err)
				}
			}
			checkBenchmarkEdit(b, out, tc.edit, cover)
			if !bytes.Equal(data, []byte(tc.opf)) {
				b.Fatal("rewrite mutated the source OPF")
			}
		})
	}
}

func BenchmarkRewriteBook(b *testing.B) {
	title, author := "New title", "New author"
	jpegData := benchmarkEditJPEG(b)
	for _, tc := range []struct {
		name string
		edit MetadataEdit
	}{
		{"Metadata", MetadataEdit{Title: &title, Author: &author}},
		{"CoverReplace", MetadataEdit{CoverJPEG: jpegData}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			src := benchmarkEditArchive(b, jpegData)
			tmp, err := RewriteBook(src, tc.edit)
			if err != nil {
				b.Fatal(err)
			}
			checkBenchmarkEditArchive(b, tmp, tc.edit)
			if err := os.Remove(tmp); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				tmp, err = RewriteBook(src, tc.edit)
				if err != nil {
					b.Fatal(err)
				}
				if err := os.Remove(tmp); err != nil {
					b.Fatal(err)
				}
			}
			tmp, err = RewriteBook(src, tc.edit)
			if err != nil {
				b.Fatal(err)
			}
			checkBenchmarkEditArchive(b, tmp, tc.edit)
			if err := os.Remove(tmp); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func benchmarkEditJPEG(b *testing.B) []byte {
	b.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil); err != nil {
		b.Fatal(err)
	}
	return out.Bytes()
}

func benchmarkEditArchive(b *testing.B, cover []byte) string {
	b.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, entry := range []struct {
		name   string
		method uint16
		data   []byte
	}{
		{"mimetype", zip.Store, []byte("application/epub+zip")},
		{"META-INF/container.xml", zip.Deflate, []byte(`<container><rootfiles><rootfile full-path="OPS/content.opf"/></rootfiles></container>`)},
		{"OPS/content.opf", zip.Deflate, []byte(benchmarkEditOPF)},
		{"OPS/ch.xhtml", zip.Deflate, []byte(`<html><body>Chapter</body></html>`)},
		{"OPS/resource.bin", zip.Store, bytes.Repeat([]byte("unchanged resource"), 16384)},
		{"OPS/cover.jpg", zip.Store, cover},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: entry.method})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			b.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		b.Fatal(err)
	}
	src := filepath.Join(b.TempDir(), "source.epub")
	if err := os.WriteFile(src, out.Bytes(), 0o600); err != nil {
		b.Fatal(err)
	}
	return src
}

func checkBenchmarkEdit(b *testing.B, opf []byte, edit MetadataEdit, cover string) {
	b.Helper()
	meta, _, err := parseOPF(opf, "OPS")
	if err != nil {
		b.Fatal(err)
	}
	if edit.Title != nil && meta.Title != *edit.Title || edit.Author != nil && meta.Author != *edit.Author {
		b.Fatalf("metadata did not round-trip: %+v", meta)
	}
	if edit.CoverJPEG != nil && (cover == "" || meta.CoverPath != cover) {
		b.Fatalf("cover reference = %q; want %q", meta.CoverPath, cover)
	}
}

func checkBenchmarkEditArchive(b *testing.B, path string, edit MetadataEdit) {
	b.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := zr.Close(); err != nil {
			b.Error(err)
		}
	}()
	index := buildIndex(&zr.Reader)
	opf, err := readZipFileIndexed(index, "OPS/content.opf")
	if err != nil {
		b.Fatal(err)
	}
	checkBenchmarkEdit(b, opf, edit, "OPS/cover.jpg")
	if edit.CoverJPEG != nil {
		cover, err := readZipFileIndexed(index, "OPS/cover.jpg")
		if err != nil || !bytes.Equal(cover, edit.CoverJPEG) {
			b.Fatalf("cover bytes mismatch: %v", err)
		}
	}
}
