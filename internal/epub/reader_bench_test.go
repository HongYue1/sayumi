package epub

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func BenchmarkReadZipEntry(b *testing.B) {
	for _, tt := range []struct {
		name   string
		size   int
		method uint16
	}{
		{name: "Store4KiB", size: 4 << 10, method: zip.Store},
		{name: "Deflate64KiB", size: 64 << 10, method: zip.Deflate},
	} {
		b.Run(tt.name, func(b *testing.B) {
			body := strings.Repeat("<p>text</p>\n", tt.size/12+1)[:tt.size]
			zr := benchmarkZIP(b, []benchmarkZipEntry{{name: "chapter.xhtml", body: body}}, tt.method)
			entry := zr.File[0]
			got, err := readZipEntry(entry)
			if err != nil || !bytes.Equal(got, []byte(body)) {
				b.Fatalf("fixture: bytes=%d, err=%v", len(got), err)
			}
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := readZipEntry(entry); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
