package epub

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
// sequential chapters is decompressed and rewritten once, then replayed from the
// book-scoped cache, producing byte-identical CSS across chapters.
func TestProcessChapterSharesStylesheetCache(t *testing.T) {
	t.Parallel()
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
	// Configure the decompressor before publication; shared readers/indexes must
	// never be modified after OpenIndexed returns them to callers.
	var decompressions atomic.Int32
	if _, err := store.acquireWithOpener(zipPath, func(name string) (*zip.ReadCloser, error) {
		reader, err := zip.OpenReader(name)
		if err != nil {
			return reader, err
		}
		reader.RegisterDecompressor(zip.Deflate, func(r io.Reader) io.ReadCloser {
			decompressions.Add(1)
			return flate.NewReader(r)
		})
		return reader, nil
	}); err != nil {
		t.Fatal(err)
	}
	store.Release(zipPath)
	spine := []SpineEntry{{Href: "ch1.xhtml"}, {Href: "ch2.xhtml"}}

	r0, err := ProcessChapter(t.Context(), store, zipPath, spine, 0, "book1", "ltr", "tok123")
	if err != nil {
		t.Fatalf("ProcessChapter(0): %v", err)
	}

	if got := decompressions.Load(); got != 2 {
		t.Fatalf("first chapter + stylesheet decompressions = %d; want 2", got)
	}

	// The shared stylesheet must be cached after the first cold render.
	if _, ok := store.GetCSSFragment(zipPath, "style.css"); !ok {
		t.Fatalf("expected style.css fragment to be cached after first render")
	}

	r1, err := ProcessChapter(t.Context(), store, zipPath, spine, 1, "book1", "ltr", "tok123")
	if err != nil {
		t.Fatalf("ProcessChapter(1): %v", err)
	}

	if got := decompressions.Load(); got != 3 {
		t.Fatalf("two chapters + shared stylesheet decompressions = %d; want 3", got)
	}
	if !strings.Contains(r1.HTML, "<p>Two</p>") {
		t.Fatalf("second chapter was not rendered: %q", r1.HTML)
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

func TestProcessChapterCacheSurvivesZIPClose(t *testing.T) {
	t.Parallel()
	filePath := writeTestEPUB(t, map[string]string{
		"ch.xhtml": `<html dir="rtl"><head><style>@font-face { font-family: Book; src: url(font.woff2); } p { writing-mode: vertical-rl; }</style></head><body><img src="image.png">Text</body></html>`,
	})
	store := NewStore(1)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "ch.xhtml"}}
	store.SetChapter(filePath, 0, "old-render-version", ChapterResponse{HTML: "stale"})
	want, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "ltr", "token")
	if err != nil || want.HTML == "stale" || want.CSS == "" || want.FontFaceCSS == "" || want.Direction != "rtl" || want.WritingMode != "vertical-rl" {
		t.Fatalf("cold render = %+v, %v", want, err)
	}
	store.CloseBook(filePath)
	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	// The missing ZIP makes an accidental cache miss observable without mutating
	// a published reader/index. Derived responses outlive ZIP retention.
	got, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "ltr", "token")
	if err != nil || got != want {
		t.Fatalf("warm render = %+v, %v; want %+v", got, err, want)
	}
	store.EvictBook(filePath)
	got, err = ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "ltr", "token")
	if err == nil || got != (ChapterResponse{}) {
		t.Fatalf("evicted render = %+v, %v; want zero response and open error", got, err)
	}
	if _, ok := store.GetChapter(filePath, 0, ChapterRenderVersion); ok {
		t.Error("failed render was cached")
	}
}

// Cancellation on decompressor close occurs after a successful archive read but
// before HTML processing, so the render-error ownership path is deterministic.
type cancelChapterReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r cancelChapterReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

func TestProcessChapterBorrowOwnership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		href       string
		cancelRead bool
		wantError  bool
	}{
		{name: "success", href: "ch.xhtml"},
		{name: "missing chapter", href: "missing.xhtml", wantError: true},
		{name: "canceled after read", href: "ch.xhtml", cancelRead: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath := writeTestEPUB(t, map[string]string{"ch.xhtml": "<html><body>Text</body></html>"})
			store := NewStore(1)
			t.Cleanup(store.Close)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			func() {
				// Keep an independent borrow pinned across ProcessChapter to catch
				// both an extra Release and a missing Release, not merely leaks.
				if _, err := store.acquireWithOpener(filePath, func(name string) (*zip.ReadCloser, error) {
					reader, err := zip.OpenReader(name)
					if err != nil {
						return reader, err
					}
					if tc.cancelRead {
						reader.RegisterDecompressor(zip.Deflate, func(r io.Reader) io.ReadCloser {
							return cancelChapterReadCloser{ReadCloser: flate.NewReader(r), cancel: cancel}
						})
					}
					return reader, nil
				}); err != nil {
					t.Fatal(err)
				}
				defer store.Release(filePath)
				got, err := ProcessChapter(ctx, store, filePath, []SpineEntry{{Href: tc.href}}, 0, "book", "ltr", "token")
				if (err != nil) != tc.wantError {
					t.Errorf("render error = %v; wantError %v", err, tc.wantError)
				}
				if tc.cancelRead && !errors.Is(err, context.Canceled) {
					t.Errorf("render error = %v; want context.Canceled", err)
				}
				if tc.wantError && got != (ChapterResponse{}) {
					t.Errorf("failed render returned a response: %+v", got)
				}
				if cached, ok := store.GetChapter(filePath, 0, ChapterRenderVersion); ok != !tc.wantError || ok && cached != got {
					t.Errorf("cache after render = %+v, %v", cached, ok)
				}
				if store.TryCloseForReplace(filePath) {
					t.Error("ProcessChapter released the independent caller's borrow")
				}
			}()
			if !store.TryCloseForReplace(filePath) {
				t.Error("ProcessChapter retained a borrow after returning")
			}
		})
	}
}

func TestProcessChapterReplacementAndIsolation(t *testing.T) {
	t.Parallel()
	chapter := func(text string) string {
		return `<html><head><link rel="stylesheet" href="style.css"></head><body><img src="img.png"><p>` + text + `</p></body></html>`
	}
	filePath := writeTestEPUB(t, map[string]string{"ch.xhtml": chapter("old"), "style.css": "p { color: red; }"})
	otherPath := writeTestEPUB(t, map[string]string{"ch.xhtml": chapter("other"), "style.css": "p { color: blue; }"})
	store := NewStore(2)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "ch.xhtml"}}
	old, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "ltr", "old-token")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ProcessChapter(t.Context(), store, otherPath, spine, 0, "other-book", "rtl", "other-token")
	if err != nil {
		t.Fatal(err)
	}
	if old.CSS == other.CSS || old.HTML == other.HTML || !strings.Contains(other.HTML, "other-token") {
		t.Fatalf("book-scoped outputs are not isolated: first=%+v other=%+v", old, other)
	}
	// No new acquisitions occur during replacement, matching the API generation
	// lock contract. Both derived caches must be invalidated for this path only.
	if !store.TryCloseForReplace(filePath) {
		t.Fatal("chapter render did not release the old generation")
	}
	if _, ok := store.GetChapter(filePath, 0, ChapterRenderVersion); ok {
		t.Error("old chapter survived replacement invalidation")
	}
	if _, ok := store.GetCSSFragment(filePath, "style.css"); ok {
		t.Error("old stylesheet survived replacement invalidation")
	}
	if _, ok := store.GetCSSFragment(otherPath, "style.css"); !ok {
		t.Error("replacement evicted another book's stylesheet")
	}
	newPath := writeTestEPUB(t, map[string]string{"ch.xhtml": chapter("new"), "style.css": "p { color: green; writing-mode: vertical-lr; }"})
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "rtl", "new-token")
	if err != nil || got == old || !strings.Contains(got.HTML, "<p>new</p>") || !strings.Contains(got.HTML, "new-token") || strings.Contains(got.HTML, "old-token") || !strings.Contains(got.CSS, "green") || got.Direction != "rtl" || got.WritingMode != "vertical-lr" {
		t.Fatalf("replacement render = %+v, %v", got, err)
	}
	if cached, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "rtl", "new-token"); err != nil || cached != got {
		t.Fatalf("replacement cache = %+v, %v; want %+v", cached, err, got)
	}
	if cached, err := ProcessChapter(t.Context(), store, otherPath, spine, 0, "other-book", "rtl", "other-token"); err != nil || cached != other {
		t.Fatalf("other book changed = %+v, %v; want %+v", cached, err, other)
	}
}

func TestProcessChapterConcurrentRenders(t *testing.T) {
	t.Parallel()
	filePath := writeTestEPUB(t, map[string]string{
		"one.xhtml": `<html><head><link rel="stylesheet" href="style.css"></head><body>One<img src="one.png"></body></html>`,
		"two.xhtml": `<html><head><link rel="stylesheet" href="style.css"></head><body>Two<img src="two.png"></body></html>`,
		"style.css": `@font-face { font-family: Book; src: url(book.woff2); } body { background: url(bg.png); }`,
	})
	store := NewStore(2)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "one.xhtml"}, {Href: "two.xhtml"}}
	ctx := t.Context()
	var want [2]ChapterResponse
	for i := range want {
		var err error
		want[i], err = ProcessChapter(ctx, store, filePath, spine, i, "book", "ltr", "token")
		if err != nil {
			t.Fatal(err)
		}
	}
	store.EvictBook(filePath)
	// Concurrent misses may duplicate work; the cache is not a singleflight
	// promise. All completed responses must nevertheless be identical and safe.
	type result struct {
		chapter int
		resp    ChapterResponse
		err     error
	}
	const workers = 16
	start := make(chan struct{})
	results := make(chan result, workers)
	for i := range workers {
		go func() {
			<-start
			chapter := i % len(want)
			resp, err := ProcessChapter(ctx, store, filePath, spine, chapter, "book", "ltr", "token")
			results <- result{chapter: chapter, resp: resp, err: err}
		}()
	}
	close(start)
	for range workers {
		got := <-results
		if got.err != nil || got.resp != want[got.chapter] {
			t.Errorf("concurrent chapter %d = %+v, %v; want %+v", got.chapter, got.resp, got.err, want[got.chapter])
		}
	}
	if !store.TryCloseForReplace(filePath) {
		t.Error("concurrent renders retained ZIP borrows")
	}
}

func TestProcessChapterCancellation(t *testing.T) {
	t.Parallel()
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			filePath := writeTestEPUB(t, map[string]string{"ch.xhtml": "<html><body>Chapter</body></html>"})
			store := NewStore(1)
			t.Cleanup(store.Close)
			spine := []SpineEntry{{Href: "ch.xhtml"}}
			if warm {
				if _, err := ProcessChapter(t.Context(), store, filePath, spine, 0, "book", "ltr", "token"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			got, err := ProcessChapter(ctx, store, filePath, spine, 0, "book", "ltr", "token")
			if !errors.Is(err, context.Canceled) || got != (ChapterResponse{}) {
				t.Errorf("canceled render = %+v, %v; want zero response, context.Canceled", got, err)
			}
			if _, ok := store.GetChapter(filePath, 0, ChapterRenderVersion); ok != warm {
				t.Errorf("cache presence = %v; want %v after cancellation", ok, warm)
			}
			// Validation remains ahead of cancellation, independent of cache state.
			if _, err := ProcessChapter(ctx, store, filePath, spine, -1, "book", "ltr", "token"); err == nil || errors.Is(err, context.Canceled) {
				t.Errorf("invalid chapter index error = %v; want range error", err)
			}
			if !store.TryCloseForReplace(filePath) {
				t.Fatal("chapter render leaked a ZIP borrow")
			}
		})
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

	if gotBytes != 900 || gotLen != 3 {
		t.Errorf("cache holds %d bytes / %d entries; want 900 bytes / 3 entries", gotBytes, gotLen)
	}
	for i := range 10 {
		if _, ok := cache.Get(i); ok != (i >= 7) {
			t.Errorf("entry %d retained = %v; want %v", i, ok, i >= 7)
		}
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
