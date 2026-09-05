package epub

import (
	"archive/zip"
	"errors"
	"fmt"
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
	readers := make([]*zip.Reader, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			<-start
			reader, index, err := store.OpenIndexed(zipPath)
			if err != nil {
				t.Errorf("OpenIndexed: %v", err)
				return
			}
			defer store.Release(zipPath)
			readers[i] = reader
			if reader == nil || index == nil {
				t.Errorf("OpenIndexed returned nil reader/index")
				return
			}
			data, err := readZipFileIndexed(index, "OEBPS/ch1.xhtml")
			if err != nil || string(data) != "<html><body><p>One</p></body></html>" {
				t.Errorf("borrowed chapter: %q, %v", data, err)
			}
		})
	}
	close(start)
	wg.Wait()
	for i, reader := range readers {
		if reader == nil || reader != readers[0] {
			t.Errorf("borrower %d received a different reader", i)
		}
	}
	if !store.TryCloseForReplace(zipPath) {
		t.Error("completed borrowers left references behind")
	}
}

// TestStoreConcurrentOpenDifferentBooks exercises retention pressure while
// different books have live borrowers. Shared bookkeeping must stay race-free
// without closing or confusing another request's reader.
func TestStoreConcurrentOpenDifferentBooks(t *testing.T) {
	t.Parallel()
	const books = 8
	paths := make([]string, books)
	for i := range paths {
		paths[i] = writeTestEPUB(t, map[string]string{
			"ch.xhtml": fmt.Sprintf("<html><body>book %d</body></html>", i),
		})
	}
	// Cap below the number of books so eviction runs under contention too.
	store := NewStore(books / 2)
	defer store.Close()

	start := make(chan struct{})
	var wg sync.WaitGroup
	const itersPerBook = 16
	for i, p := range paths {
		for range itersPerBook {
			wg.Go(func() {
				<-start
				_, index, err := store.OpenIndexed(p)
				if err != nil {
					t.Errorf("OpenIndexed(%s): %v", p, err)
					return
				}
				defer store.Release(p)
				data, err := readZipFileIndexed(index, "ch.xhtml")
				want := fmt.Sprintf("<html><body>book %d</body></html>", i)
				if err != nil || string(data) != want {
					t.Errorf("borrowed chapter for %s: %q, %v", p, data, err)
				}
			})
		}
	}
	close(start)
	wg.Wait()
	for _, p := range paths {
		if !store.TryCloseForReplace(p) {
			t.Errorf("completed borrowers still pin %s", p)
		}
	}
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
	for range goroutines {
		wg.Go(func() {
			<-start
			if _, _, err := store.OpenIndexed(missing); err == nil {
				store.Release(missing)
				t.Error("expected error opening missing epub")
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Errorf("missing epub error lost its cause: %v", err)
			}
		})
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
	defer store.Release(missing)
	data, err := readZipFileIndexed(index, "ch.xhtml")
	if err != nil || string(data) != "<html><body>ok</body></html>" {
		t.Errorf("chapter after recovery: %q, %v", data, err)
	}
}
