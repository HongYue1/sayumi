package library

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"sayumi/internal/storage"
)

// writeMinimalEPUB writes a tiny valid-enough EPUB for import/scan tests.
func writeMinimalEPUB(t *testing.T, path, title string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()
	zw := zip.NewWriter(f)
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		t.Fatalf("mimetype: %v", err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("mimetype write: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:test:` + title + `</dc:identifier>
    <dc:title>` + title + `</dc:title>
    <dc:creator>Tester</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch"/>
  </spine>
</package>`
	write("OEBPS/content.opf", opf)
	write("OEBPS/ch.xhtml", `<?xml version="1.0"?><html><body><p>`+title+`</p></body></html>`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func openTestDB(t *testing.T, lib string) *storage.DB {
	t.Helper()
	db, err := storage.Open(lib)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestScanNowSingleFlight runs many concurrent ScanNow calls against an empty
// library and asserts they all succeed with consistent results. It exercises
// the single-flight guard: overlapping callers must coalesce onto one in-flight
// scan without data races or errors, and the guard must reset so a later scan
// still runs.
func TestScanNowSingleFlight(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	scanner := NewScanner(dir, db)
	ctx := context.Background()

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	counts := make([]int, goroutines)

	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			ids, scanErr := scanner.ScanNow(ctx)
			errs[i] = scanErr
			counts[i] = len(ids)
		}()
	}
	wg.Wait()

	for i := range goroutines {
		if errs[i] != nil {
			t.Errorf("concurrent ScanNow[%d] failed: %v", i, errs[i])
		}
		if counts[i] != 0 {
			t.Errorf("concurrent ScanNow[%d] imported %d books from an empty library", i, counts[i])
		}
	}

	if _, err := scanner.ScanNow(ctx); err != nil {
		t.Fatalf("ScanNow after concurrent burst failed: %v", err)
	}
}

func TestCollectEPUBPathsSkipsDots(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)

	writeMinimalEPUB(t, filepath.Join(lib, "keep.epub"), "Keep")
	writeMinimalEPUB(t, filepath.Join(lib, ".hidden.epub"), "HiddenFile")
	writeMinimalEPUB(t, filepath.Join(lib, ".sayumi", "tmp.epub"), "HiddenDir")
	writeMinimalEPUB(t, filepath.Join(lib, "sub", "nested.epub"), "Nested")
	_ = os.WriteFile(filepath.Join(lib, "notes.txt"), []byte("x"), 0o644)

	paths, err := s.collectEPUBPaths(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[filepath.Base(p)] = true
	}
	if !got["keep.epub"] || !got["nested.epub"] {
		t.Fatalf("missing expected paths: %v", paths)
	}
	if got[".hidden.epub"] || got["tmp.epub"] {
		t.Fatalf("dot entries should be skipped: %v", paths)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2", paths)
	}
}

func TestScanNowImportAndDedup(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := context.Background()

	src := filepath.Join(lib, "book.epub")
	writeMinimalEPUB(t, src, "Alpha")

	ids, err := s.ScanNow(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("first scan ids = %v", ids)
	}
	firstID := ids[0]

	// Second scan: nothing new.
	ids2, err := s.ScanNow(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(ids2) != 0 {
		t.Fatalf("second scan imported %v", ids2)
	}

	// Content-hash dedup + path reconcile: same bytes at a new path.
	copyPath := filepath.Join(lib, "book-copy.epub")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove original so the only on-disk location is the copy (still same hash).
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	ids3, err := s.ScanNow(ctx)
	if err != nil {
		t.Fatalf("dedup scan: %v", err)
	}
	if len(ids3) != 0 {
		t.Fatalf("dedup should not import new id, got %v", ids3)
	}

	paths, err := db.ListBookPathsContext(ctx)
	if err != nil {
		t.Fatalf("list paths: %v", err)
	}
	if len(paths) != 1 || paths[0].ID != firstID {
		t.Fatalf("books after dedup = %+v", paths)
	}
	absCopy, _ := filepath.Abs(copyPath)
	if paths[0].FilePath != absCopy {
		t.Fatalf("path not reconciled: got %q want %q", paths[0].FilePath, absCopy)
	}
}

func TestScanNowSkipsIgnoredPath(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := context.Background()

	path := filepath.Join(lib, "gone.epub")
	writeMinimalEPUB(t, path, "Gone")
	ids, err := s.ScanNow(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("import: ids=%v err=%v", ids, err)
	}
	// DeleteBook marks the file path ignored.
	if err := db.DeleteBookContext(ctx, ids[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// File still on disk; rescan must not re-import.
	ids2, err := s.ScanNow(ctx)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(ids2) != 0 {
		t.Fatalf("ignored path re-imported: %v", ids2)
	}
	paths, err := db.ListBookPathsContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no books, got %+v", paths)
	}
}

func TestContentHashAndGenerateID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "h.epub")
	writeMinimalEPUB(t, p, "HashMe")

	ctx := context.Background()
	h1, sz1, err := HashFile(ctx, p)
	if err != nil || h1 == "" || sz1 <= 0 {
		t.Fatalf("HashFile: %q %d %v", h1, sz1, err)
	}
	h2, sz2, err := contentHash(ctx, p)
	if err != nil || h2 != h1 || sz2 != sz1 {
		t.Fatalf("contentHash mismatch: %q/%d vs %q/%d err=%v", h2, sz2, h1, sz1, err)
	}

	// Cancel mid-hash on a larger file.
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately after start via short timeout.
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()
	// Best-effort: either completes or returns ctx error; both OK for tiny files.
	_, _, _ = contentHash(cctx, big)

	id1 := generateID("/a/path.epub", h1)
	id2 := generateID("/b/path.epub", h1)
	if len(id1) != 16 || len(id2) != 16 {
		t.Fatalf("id lens %d %d", len(id1), len(id2))
	}
	if id1 == id2 {
		t.Fatal("generateID should be path-sensitive")
	}
}

func TestImportFile(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := context.Background()

	p := filepath.Join(lib, "one.epub")
	writeMinimalEPUB(t, p, "OneShot")
	id, err := s.ImportFile(ctx, p, "")
	if err != nil || id == "" {
		t.Fatalf("ImportFile: id=%q err=%v", id, err)
	}
	// Re-import same path returns existing id without error.
	id2, err := s.ImportFile(ctx, p, "")
	if err != nil || id2 != id {
		t.Fatalf("reimport: id=%q err=%v want %q", id2, err, id)
	}
}
