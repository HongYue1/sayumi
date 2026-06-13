package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestEPUB writes a minimal zip containing the given entries and returns
// its path on disk so it can be opened via the store.
func writeTestEPUB(t *testing.T, files map[string]string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close zip file: %v", cerr)
		}
	}()
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return zipPath
}

// TestProcessChapterSharesStylesheetCache verifies that a stylesheet linked by
// multiple chapters is decompressed and rewritten once, then replayed from the
// book-scoped cache, producing byte-identical CSS across chapters.
func TestProcessChapterSharesStylesheetCache(t *testing.T) {
	const css = `@font-face { font-family: "Fancy"; src: url("fonts/fancy.woff2"); }` + "\n" +
		`body { background: url("img/bg.png"); }`
	chapter := func(body string) string {
		return `<html><head><link rel="stylesheet" href="style.css"></head><body>` + body + `</body></html>`
	}
	zipPath := writeTestEPUB(t, map[string]string{
		"ch1.xhtml": chapter("<p>One</p>"),
		"ch2.xhtml": chapter("<p>Two</p>"),
		"style.css": css,
	})

	store := NewStore(10)
	defer store.Close()
	spine := []SpineEntry{{Href: "ch1.xhtml"}, {Href: "ch2.xhtml"}}

	r0, err := ProcessChapter(store, zipPath, spine, 0, "book1", "ltr", "tok123")
	if err != nil {
		t.Fatalf("ProcessChapter(0): %v", err)
	}

	// The shared stylesheet must be cached after the first cold render.
	if _, ok := store.GetCSSFragment(zipPath, "style.css"); !ok {
		t.Fatalf("expected style.css fragment to be cached after first render")
	}

	r1, err := ProcessChapter(store, zipPath, spine, 1, "book1", "ltr", "tok123")
	if err != nil {
		t.Fatalf("ProcessChapter(1): %v", err)
	}

	// Both chapters link the same sheet, so the rewritten output is identical.
	if r0.CSS != r1.CSS {
		t.Errorf("CSS differs between chapters sharing a stylesheet:\nch0: %q\nch1: %q", r0.CSS, r1.CSS)
	}
	if r0.FontFaceCSS != r1.FontFaceCSS {
		t.Errorf("FontFaceCSS differs between chapters sharing a stylesheet:\nch0: %q\nch1: %q", r0.FontFaceCSS, r1.FontFaceCSS)
	}

	// Sanity: @font-face split out, normal rules kept, resource URLs rewritten.
	if r0.CSS == "" {
		t.Errorf("expected non-empty CSS")
	}
	if !strings.Contains(strings.ToLower(r0.FontFaceCSS), "@font-face") {
		t.Errorf("expected @font-face block in FontFaceCSS, got %q", r0.FontFaceCSS)
	}
	if strings.Contains(strings.ToLower(r0.CSS), "@font-face") {
		t.Errorf("did not expect @font-face block in CSS, got %q", r0.CSS)
	}
	if !strings.Contains(r0.CSS, "/api/books/book1/resources") {
		t.Errorf("expected resource URLs rewritten with resourceBase, got %q", r0.CSS)
	}

	// EvictBook must drop the cached fragment for that book.
	store.EvictBook(zipPath)
	if _, ok := store.GetCSSFragment(zipPath, "style.css"); ok {
		t.Errorf("expected style.css fragment to be evicted after EvictBook")
	}
}
