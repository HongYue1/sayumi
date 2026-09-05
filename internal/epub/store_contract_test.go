package epub

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestStoreDerivedCacheIsolation(t *testing.T) {
	t.Parallel()
	store := NewStore(2)
	t.Cleanup(store.Close)
	for _, book := range []string{"first.epub", "second.epub"} {
		store.SetChapter(book, 0, "v1", ChapterResponse{HTML: book})
		store.SetChapter(book, 0, "v2", ChapterResponse{HTML: book + " v2"})
		store.SetText(book, 0, book, book)
		store.SetCSSFragment(book, "style.css", cssFragment{css: book, fontFace: "font"})
	}
	if _, ok := store.GetChapter("first.epub", 0, "missing"); ok {
		t.Fatal("render versions must not share a cache entry")
	}
	store.EvictBook("first.epub")
	for _, version := range []string{"v1", "v2"} {
		if _, ok := store.GetChapter("first.epub", 0, version); ok {
			t.Errorf("evicted chapter version %s remains", version)
		}
		if _, ok := store.GetChapter("second.epub", 0, version); !ok {
			t.Errorf("unrelated chapter version %s was evicted", version)
		}
	}
	if _, _, ok := store.GetText("first.epub", 0); ok {
		t.Error("evicted text remains")
	}
	if _, ok := store.GetCSSFragment("first.epub", "style.css"); ok {
		t.Error("evicted stylesheet remains")
	}
	orig, lower, ok := store.GetText("second.epub", 0)
	if !ok || orig != "second.epub" || lower != orig {
		t.Errorf("unrelated text = %q, %q, %v", orig, lower, ok)
	}
	if frag, ok := store.GetCSSFragment("second.epub", "style.css"); !ok || frag.css != "second.epub" {
		t.Errorf("unrelated stylesheet = %+v, %v", frag, ok)
	}
	store.Close()
	if _, ok := store.GetChapter("second.epub", 0, "v1"); ok {
		t.Error("shutdown retained a chapter")
	}
	if _, _, ok := store.GetText("second.epub", 0); ok {
		t.Error("shutdown retained extracted text")
	}
	if _, ok := store.GetCSSFragment("second.epub", "style.css"); ok {
		t.Error("shutdown retained a stylesheet")
	}
}

func TestStorePinnedEvictionAndReacquire(t *testing.T) {
	t.Parallel()
	first := writeTestEPUB(t, map[string]string{"item.txt": "first"})
	second := writeTestEPUB(t, map[string]string{"item.txt": "second"})
	store := NewStore(1)
	t.Cleanup(store.Close)
	firstReader, firstIndex, releaseFirst := openStoreTestEntry(t, store, first)
	_, _, releaseSecond := openStoreTestEntry(t, store, second)
	if got, err := readZipFileIndexed(firstIndex, "item.txt"); err != nil || string(got) != "first" {
		t.Fatalf("eviction invalidated a live reference: %q, %v", got, err)
	}
	if store.TryCloseForReplace(first) {
		t.Fatal("an evicted but pinned book was replaceable")
	}
	readerAgain, _, releaseAgain := openStoreTestEntry(t, store, first)
	if readerAgain != firstReader {
		t.Error("reacquire opened a duplicate reader for the pinned path")
	}
	releaseSecond()
	releaseFirst()
	if store.TryCloseForReplace(first) {
		t.Fatal("one remaining reference was not retained")
	}
	releaseAgain()
	if !store.TryCloseForReplace(first) {
		t.Fatal("idle book was not replaceable")
	}
	if _, err := readZipFileIndexed(firstIndex, "item.txt"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("replaced reader remains usable: %v", err)
	}
}

func TestStoreResourceFailureReleasesReference(t *testing.T) {
	t.Parallel()
	filePath := writeTestEPUB(t, map[string]string{"good.bin": "data"})
	store := NewStore(1)
	t.Cleanup(store.Close)
	_, index, release := openStoreTestEntry(t, store, filePath)
	index["good.bin"].Method = 99 // Private fixture: force the ZIP entry-open error path.
	release()
	for _, resourcePath := range []string{"../bad", "missing.bin", "good.bin"} {
		t.Run(resourcePath, func(t *testing.T) {
			reader, err := store.OpenResource(filePath, resourcePath)
			if err == nil || reader != nil {
				t.Fatalf("OpenResource = %v, %v; want nil reader and error", reader, err)
			}
			store.mu.Lock()
			entry := store.openFiles[filePath]
			refs := 0
			if entry != nil {
				refs = entry.refs
			}
			store.mu.Unlock()
			if refs != 0 {
				t.Errorf("failed resource open retained %d references", refs)
			}
		})
	}
	if !store.TryCloseForReplace(filePath) {
		t.Fatal("resource failures left the book pinned")
	}
}

type resourceCloseProbe struct {
	calls atomic.Int32
	err   error
	block <-chan struct{}
}

func (*resourceCloseProbe) Read([]byte) (int, error) { return 0, io.EOF }
func (p *resourceCloseProbe) Close() error {
	if p.calls.Add(1) == 1 && p.block != nil {
		<-p.block
	}
	return p.err
}

func TestResourceReaderClosesExactlyOnce(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		concurrent bool
		closeErr   error
	}{
		{name: "sequential"},
		{name: "sequential error", closeErr: io.ErrClosedPipe},
		{name: "concurrent", concurrent: true},
		{name: "concurrent error", concurrent: true, closeErr: io.ErrClosedPipe},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(1)
			t.Cleanup(store.Close)
			entry := &zipEntry{filePath: "book", refs: 2}
			store.openFiles["book"] = entry
			probe := &resourceCloseProbe{err: tt.closeErr}
			reader := &ResourceReader{rc: probe, store: store, filePath: "book"}
			closeReader := func() {
				if err := reader.Close(); !errors.Is(err, tt.closeErr) {
					t.Errorf("Close = %v; want %v", err, tt.closeErr)
				}
			}
			if tt.concurrent {
				start := make(chan struct{})
				var wg sync.WaitGroup
				for range 32 {
					wg.Go(func() {
						<-start
						closeReader()
					})
				}
				close(start)
				wg.Wait()
			} else {
				closeReader()
				closeReader()
			}
			if got := probe.calls.Load(); got != 1 {
				t.Errorf("underlying Close calls = %d; want 1", got)
			}
			if entry.refs != 1 {
				t.Errorf("remaining independent references = %d; want 1", entry.refs)
			}
			if store.TryCloseForReplace("book") {
				t.Error("resource close released another borrower's reference")
			}
			store.Release("book")
		})
	}
}

func TestStoreOpenResourceMetadata(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, contentType string }{
		{name: "Image.PNG", contentType: "image/png"},
		{name: "font.woff2", contentType: "font/woff2"},
		{name: "unknown.bin", contentType: "application/octet-stream"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			filePath := writeTestEPUB(t, map[string]string{tt.name: "payload"})
			store := NewStore(1)
			t.Cleanup(store.Close)
			reader, err := store.OpenResource(filePath, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			})
			if reader.Size != -1 || reader.ContentType != tt.contentType {
				t.Errorf("resource metadata: size=%d, type=%q", reader.Size, reader.ContentType)
			}
			if got, err := io.ReadAll(reader); err != nil || string(got) != "payload" {
				t.Errorf("resource body = %q, %v", got, err)
			}
		})
	}
}

func TestSizedLRUReplacementBoundaries(t *testing.T) {
	t.Parallel()
	cache := newSizedLRUCache[string, string](2, 8, func(value string) int { return len(value) })
	cache.Put("a", "1234")
	cache.Put("b", "5678")
	cache.Put("a", "x")
	cache.Put("c", "1234567")
	if _, ok := cache.Get("b"); ok {
		t.Error("least-recent entry survived a count eviction")
	}
	if got, ok := cache.Get("a"); !ok || got != "x" {
		t.Errorf("shrunk value = %q, %v", got, ok)
	}
	cache.Put("a", strings.Repeat("x", 9))
	if _, ok := cache.Get("a"); ok {
		t.Error("oversized replacement retained the stale previous value")
	}
	if got, ok := cache.Get("c"); !ok || got != "1234567" {
		t.Errorf("oversized replacement evicted unrelated value: %q, %v", got, ok)
	}
	cache.Put("c", "")
	cache.Put("d", "12345678")
	if got, ok := cache.Get("c"); !ok || got != "" {
		t.Errorf("zero-size value = %q, %v", got, ok)
	}
	if cache.bytes != 8 {
		t.Errorf("accounted bytes = %d; want 8", cache.bytes)
	}
}

func TestStoreRejectedZIPClosesReader(t *testing.T) {
	// This test changes archive/zip's process-wide strict-path setting.
	t.Setenv("GODEBUG", "zipinsecurepath=0")
	filePath := writeTestEPUB(t, map[string]string{
		"../outside.txt": "never extracted",
		"item.txt":       "payload",
	})
	store := NewStore(1)
	t.Cleanup(store.Close)
	var opened *zip.ReadCloser
	entry, err := store.acquireWithOpener(filePath, func(name string) (*zip.ReadCloser, error) {
		var openErr error
		opened, openErr = zip.OpenReader(name)
		return opened, openErr
	})
	if opened == nil {
		t.Fatal("strict-path fixture did not return a ZIP reader")
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	if entry != nil || !errors.Is(err, zip.ErrInsecurePath) {
		t.Fatalf("acquire = %v, %v; want nil and ErrInsecurePath", entry, err)
	}
	if data, readErr := readZipFileIndexed(buildIndex(&opened.Reader), "item.txt"); !errors.Is(readErr, os.ErrClosed) {
		t.Errorf("rejected ZIP reader remains usable: %q, %v", data, readErr)
	}
	if len(store.openFiles) != 0 || store.lru.len() != 0 {
		t.Error("failed open left a cached placeholder")
	}
}

func TestStoreLoadingOwnership(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		closeBook   bool
		evict       bool
		newBorrower bool
	}{
		{name: "shared load"},
		{name: "LRU eviction", evict: true},
		{name: "explicit close", closeBook: true},
		{name: "borrow after close", closeBook: true, newBorrower: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			filePath := writeTestEPUB(t, map[string]string{"item.txt": "payload"})
			otherPath := writeTestEPUB(t, map[string]string{"item.txt": "other"})
			opened := openStoreTestZIP(t, filePath)
			synctest.Test(t, func(t *testing.T) {
				store := NewStore(1)
				t.Cleanup(store.Close)
				openGate := make(chan struct{})
				borrowGate := make(chan struct{})
				allowOpen := sync.OnceFunc(func() { close(openGate) })
				finishBorrows := sync.OnceFunc(func() { close(borrowGate) })
				defer allowOpen()
				defer finishBorrows()
				var calls atomic.Int32
				opener := func(string) (*zip.ReadCloser, error) {
					calls.Add(1)
					<-openGate
					return opened, nil
				}
				results := make(chan *zipEntry, 2)
				borrow := func() {
					entry, err := store.acquireWithOpener(filePath, opener)
					if err != nil {
						t.Error(err)
						results <- nil
						return
					}
					defer store.Release(filePath)
					results <- entry
					<-borrowGate
				}
				go borrow()
				synctest.Wait()
				if tt.newBorrower {
					store.CloseBook(filePath)
				}
				go borrow()
				synctest.Wait()
				if calls.Load() != 1 || len(results) != 0 {
					t.Errorf("loading: opens=%d, published=%d", calls.Load(), len(results))
				}
				if store.TryCloseForReplace(filePath) {
					t.Error("loading book was replaceable")
				}
				if tt.closeBook && !tt.newBorrower {
					store.CloseBook(filePath)
				}
				if tt.evict {
					_, _, releaseOther := openStoreTestEntry(t, store, otherPath)
					releaseOther()
				}
				allowOpen()
				synctest.Wait()
				first, second := <-results, <-results
				if first == nil || second != first {
					t.Fatal("same-path waiters did not share one published entry")
				}
				if data, err := readZipFileIndexed(first.index, "item.txt"); err != nil || string(data) != "payload" {
					t.Fatalf("published reader: %q, %v", data, err)
				}
				finishBorrows()
				synctest.Wait()
				_, retained := store.openFiles[filePath]
				wantRetained := !tt.closeBook || tt.newBorrower
				if retained != wantRetained {
					t.Errorf("reader retained after final release = %v; want %v", retained, wantRetained)
				}
				if !wantRetained {
					if _, err := readZipFileIndexed(first.index, "item.txt"); !errors.Is(err, os.ErrClosed) {
						t.Errorf("CloseBook during load did not close the reader: %v", err)
					}
				}
			})
		})
	}
}

func TestStoreFailedLoadSharedAndRetried(t *testing.T) {
	t.Parallel()
	filePath := writeTestEPUB(t, map[string]string{"item.txt": "retry"})
	synctest.Test(t, func(t *testing.T) {
		store := NewStore(1)
		t.Cleanup(store.Close)
		gate := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(gate) })
		defer unblock()
		var calls atomic.Int32
		opener := func(string) (*zip.ReadCloser, error) {
			calls.Add(1)
			<-gate
			return nil, io.ErrUnexpectedEOF
		}
		const borrowers = 8
		results := make(chan error, borrowers)
		for range borrowers {
			go func() {
				entry, err := store.acquireWithOpener(filePath, opener)
				if entry != nil {
					store.Release(filePath)
					t.Error("failed load returned a referenced entry")
				}
				results <- err
			}()
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Errorf("concurrent failed-load open calls = %d; want 1", calls.Load())
		}
		unblock()
		synctest.Wait()
		for range borrowers {
			if err := <-results; !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("shared load error = %v", err)
			}
		}
		if len(store.openFiles) != 0 || store.lru.len() != 0 {
			t.Fatal("failed placeholder remained cached")
		}
		_, index, release := openStoreTestEntry(t, store, filePath)
		if data, err := readZipFileIndexed(index, "item.txt"); err != nil || string(data) != "retry" {
			t.Errorf("retry: %q, %v", data, err)
		}
		release()
		if !store.TryCloseForReplace(filePath) {
			t.Error("failed waiters leaked references into the retry")
		}
	})
}

func TestResourceReaderCloseWaitsForCleanup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := NewStore(1)
		t.Cleanup(store.Close)
		entry := &zipEntry{filePath: "book", refs: 2}
		store.openFiles["book"] = entry
		gate := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(gate) })
		defer unblock()
		probe := &resourceCloseProbe{err: io.ErrClosedPipe, block: gate}
		reader := &ResourceReader{rc: probe, store: store, filePath: "book"}
		results := make(chan error, 1)
		go func() { results <- reader.Close() }()
		synctest.Wait()
		if got := probe.calls.Load(); got != 1 {
			t.Errorf("underlying close calls while blocked = %d; want 1", got)
		}
		if len(results) != 0 || entry.refs != 2 {
			t.Errorf("Close completed before cleanup: returns=%d, refs=%d", len(results), entry.refs)
		}
		unblock()
		synctest.Wait()
		if err := <-results; !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("Close = %v; want the first close error", err)
		}
		// Repeat after cleanup: sync.Once's mutex wait is not durably blocking
		// in synctest. Concurrent callers are covered by the barrier-based test.
		if err := reader.Close(); !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("repeated Close = %v; want the first close error", err)
		}
		if got := probe.calls.Load(); got != 1 {
			t.Errorf("underlying Close calls = %d; want 1", got)
		}
		if entry.refs != 1 {
			t.Errorf("references after cleanup = %d; want 1", entry.refs)
		}
		store.Release("book")
	})
}

func openStoreTestZIP(t *testing.T, filePath string) *zip.ReadCloser {
	t.Helper()
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	return reader
}

func openStoreTestEntry(
	t *testing.T,
	store *EPUBStore,
	filePath string,
) (*zip.Reader, map[string]*zip.File, func()) {
	t.Helper()
	reader, index, err := store.OpenIndexed(filePath)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { store.Release(filePath) })
	t.Cleanup(release)
	return reader, index, release
}
