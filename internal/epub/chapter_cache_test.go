package epub

import (
	"archive/zip"
	"context"
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

	r0, err := ProcessChapter(context.Background(), store, zipPath, spine, 0, "book1", "ltr", "tok123")
	if err != nil {
		t.Fatalf("ProcessChapter(0): %v", err)
	}

	// The shared stylesheet must be cached after the first cold render.
	if _, ok := store.GetCSSFragment(zipPath, "style.css"); !ok {
		t.Fatalf("expected style.css fragment to be cached after first render")
	}

	r1, err := ProcessChapter(context.Background(), store, zipPath, spine, 1, "book1", "ltr", "tok123")
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

// Entry counts alone are not a memory bound: one zip entry may decompress to
// maxZipEntryBytes, and a crafted EPUB compresses ~1000:1, so a few-megabyte
// book with many spine entries could pin gigabytes across the count-limited
// slots after a single search (which walks and caches every chapter's text).
func TestSizedLRUEvictsOnByteBudget(t *testing.T) {
	t.Parallel()

	const budget = 1000
	// Entry count is deliberately generous so only the byte budget can bind.
	cache := newSizedLRUCache[int, string](100, budget, func(s string) int { return len(s) })

	for i := range 10 {
		cache.Put(i, strings.Repeat("x", 300))
	}

	cache.mu.Lock()
	gotBytes, gotLen := cache.bytes, cache.order.Len()
	cache.mu.Unlock()

	if gotBytes > budget {
		t.Errorf("cache holds %d bytes, over the %d budget", gotBytes, budget)
	}
	if gotLen > 4 {
		t.Errorf("cache holds %d entries; 300-byte values under a %d budget should keep ~3", gotLen, budget)
	}
	// The most recent write must survive, and the oldest must not.
	if _, ok := cache.Get(9); !ok {
		t.Error("most recently written entry was evicted")
	}
	if _, ok := cache.Get(0); ok {
		t.Error("oldest entry survived past the byte budget")
	}
}

// A value larger than the whole budget must not be admitted: caching it would
// evict every other entry and still leave the cache over budget.
func TestSizedLRURejectsOversizedValue(t *testing.T) {
	t.Parallel()

	cache := newSizedLRUCache[int, string](100, 1000, func(s string) int { return len(s) })
	cache.Put(1, "keep me")
	cache.Put(2, strings.Repeat("y", 5000))

	if _, ok := cache.Get(2); ok {
		t.Error("value larger than the byte budget was cached")
	}
	if _, ok := cache.Get(1); !ok {
		t.Error("an oversized Put evicted an unrelated entry")
	}

	cache.mu.Lock()
	gotBytes := cache.bytes
	cache.mu.Unlock()
	if gotBytes != len("keep me") {
		t.Errorf("byte accounting drifted: got %d, want %d", gotBytes, len("keep me"))
	}
}

// Byte accounting must stay correct across overwrite and delete, or the cache
// slowly starves itself (phantom bytes) or grows unbounded (lost bytes).
func TestSizedLRUByteAccountingAcrossOverwriteAndDelete(t *testing.T) {
	t.Parallel()

	cache := newSizedLRUCache[int, string](100, 10000, func(s string) int { return len(s) })
	cache.Put(1, strings.Repeat("a", 100))
	cache.Put(1, strings.Repeat("b", 30)) // overwrite, smaller
	cache.Put(2, strings.Repeat("c", 50))

	assertBytes := func(want int) {
		t.Helper()
		cache.mu.Lock()
		got := cache.bytes
		cache.mu.Unlock()
		if got != want {
			t.Errorf("bytes = %d, want %d", got, want)
		}
	}
	assertBytes(80)

	cache.Delete(1)
	assertBytes(50)

	cache.DeleteFunc(func(k int) bool { return false })
	assertBytes(0)

	cache.Put(3, "abc")
	cache.Clear()
	assertBytes(0)
}
