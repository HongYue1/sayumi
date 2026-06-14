package epub

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestStoreConcurrentOpenSamePath hammers OpenIndexed for a single book from
// many goroutines at once. With the loading-placeholder design the burst must
// collapse to one open while every caller still receives a usable index, and
// the whole thing must be race-free under `go test -race`.
func TestStoreConcurrentOpenSamePath(t *testing.T) {
	t.Parallel()
	zipPath := writeTestEPUB(t, map[string]string{
		"OEBPS/ch1.xhtml": "<html><body><p>One</p></body></html>",
		"OEBPS/style.css": "body{}",
	})
	store := NewStore(10)
	defer store.Close()

	const goroutines = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			reader, index, err := store.OpenIndexed(zipPath)
			if err != nil {
				t.Errorf("OpenIndexed: %v", err)
				return
			}
			if reader == nil || index == nil {
				t.Errorf("OpenIndexed returned nil reader/index")
				return
			}
			if _, ok := index["OEBPS/ch1.xhtml"]; !ok {
				t.Errorf("index missing OEBPS/ch1.xhtml")
			}
			store.Release(zipPath)
		}()
	}
	close(start)
	wg.Wait()
}

// TestStoreConcurrentOpenDifferentBooks opens several distinct books
// concurrently (with repeated open/release churn per book) to exercise the
// cross-book path: opening or closing one book must never serialize or race
// against operations on another.
func TestStoreConcurrentOpenDifferentBooks(t *testing.T) {
	t.Parallel()
	const books = 8
	paths := make([]string, books)
	for i := range paths {
		paths[i] = writeTestEPUB(t, map[string]string{
			"ch.xhtml": "<html><body>book</body></html>",
		})
	}
	// Cap below the number of books so eviction runs under contention too.
	store := NewStore(books / 2)
	defer store.Close()

	start := make(chan struct{})
	var wg sync.WaitGroup
	const itersPerBook = 16
	for _, p := range paths {
		for range itersPerBook {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, index, err := store.OpenIndexed(p)
				if err != nil {
					t.Errorf("OpenIndexed(%s): %v", p, err)
					return
				}
				if _, ok := index["ch.xhtml"]; !ok {
					t.Errorf("index missing ch.xhtml for %s", p)
				}
				store.Release(p)
			}()
		}
	}
	close(start)
	wg.Wait()
}

// TestStoreConcurrentOpenMissingPathRecovers verifies the failed-load cleanup:
// concurrent opens of a non-existent file must all error without wedging the
// store or leaving a poisoned placeholder behind, and a later open of the same
// path (once the file exists) must succeed.
func TestStoreConcurrentOpenMissingPathRecovers(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent.epub")
	store := NewStore(10)
	defer store.Close()

	const goroutines = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			<-start
			if _, _, err := store.OpenIndexed(missing); err == nil {
				t.Errorf("expected error opening missing epub")
			}
		}()
	}
	close(start)
	wg.Wait()

	// Materialize a valid EPUB at the previously-missing path and confirm the
	// store recovers (the failed placeholder was removed, not cached).
	src := writeTestEPUB(t, map[string]string{
		"ch.xhtml": "<html><body>ok</body></html>",
	})
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read source epub: %v", err)
	}
	if err := os.WriteFile(missing, b, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}

	_, index, err := store.OpenIndexed(missing)
	if err != nil {
		t.Fatalf("OpenIndexed after file created: %v", err)
	}
	if _, ok := index["ch.xhtml"]; !ok {
		t.Errorf("index missing ch.xhtml after recovery")
	}
	store.Release(missing)
}
