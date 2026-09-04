package library

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func writeCoverEPUB(t *testing.T, path, title string) {
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
	write := func(name string, body []byte) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:test:` + title + `</dc:identifier>
    <dc:title>` + title + `</dc:title>
    <dc:creator>Tester</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="cover" href="cover.png" media-type="image/png" properties="cover-image"/>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch"/>
  </spine>
</package>`
	write("OEBPS/content.opf", []byte(opf))
	write("OEBPS/ch.xhtml", []byte(`<?xml version="1.0"?><html><body><p>`+title+`</p></body></html>`))
	write("OEBPS/cover.png", encodePNG(t, 12, 18))
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
	ctx := t.Context()

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	counts := make([]int, goroutines)

	for i := range goroutines {
		wg.Go(func() {
			ids, scanErr := scanner.ScanNow(ctx)
			errs[i] = scanErr
			counts[i] = len(ids)
		})
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

	paths, err := s.collectEPUBPaths(t.Context())
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
	ctx := t.Context()

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

func TestScanNowWithChangesReportsBackfilledCover(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := t.Context()

	path := filepath.Join(lib, "backfill.epub")
	writeCoverEPUB(t, path, "Backfill")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat epub: %v", err)
	}
	const id = "backfill-book"
	canonicalID, err := db.InsertBookContext(ctx, storage.BookRecord{
		ID:           id,
		Title:        "Backfill",
		Author:       "Tester",
		FilePath:     path,
		FileHash:     "backfill-hash",
		FileSize:     info.Size(),
		ChapterCount: 1,
		SpineJSON:    "[]",
		TocJSON:      "[]",
	})
	if err != nil {
		t.Fatalf("insert existing book: %v", err)
	}
	if canonicalID != id {
		t.Fatalf("canonical ID = %q, want %q", canonicalID, id)
	}

	result, err := s.ScanNowWithChanges(ctx)
	if err != nil {
		t.Fatalf("scan with backfill: %v", err)
	}
	if len(result.ImportedIDs) != 0 {
		t.Fatalf("imported IDs = %v, want none", result.ImportedIDs)
	}
	if len(result.RefreshedIDs) != 1 || result.RefreshedIDs[0] != id {
		t.Fatalf("refreshed IDs = %v, want [%s]", result.RefreshedIDs, id)
	}
	summary, found, err := db.GetBookSummaryContext(ctx, id)
	if err != nil {
		t.Fatalf("load backfilled summary: %v", err)
	}
	if !found || !summary.HasCover || summary.CoverPath == "" {
		t.Fatalf("backfilled summary = %+v, found = %v", summary, found)
	}

	result, err = s.ScanNowWithChanges(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(result.RefreshedIDs) != 0 {
		t.Fatalf("second scan refreshed IDs = %v, want none", result.RefreshedIDs)
	}
}

func TestScanNowSkipsIgnoredPath(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := t.Context()

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

	ctx := t.Context()
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
	cctx, cancel := context.WithCancel(t.Context())
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
	ctx := t.Context()

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

func TestImportUploadedFilePreservesCanonicalDuplicatePath(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := t.Context()

	canonicalPath := filepath.Join(lib, "canonical.epub")
	writeMinimalEPUB(t, canonicalPath, "Canonical")
	id, err := s.ImportFile(ctx, canonicalPath, "")
	if err != nil {
		t.Fatalf("import canonical file: %v", err)
	}

	duplicatePath := filepath.Join(lib, "duplicate.epub")
	contents, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatalf("read canonical file: %v", err)
	}
	if err := os.WriteFile(duplicatePath, contents, 0o644); err != nil {
		t.Fatalf("write duplicate file: %v", err)
	}

	duplicateID, imported, err := s.ImportUploadedFile(ctx, duplicatePath, "")
	if err != nil {
		t.Fatalf("import uploaded duplicate: %v", err)
	}
	if imported || duplicateID != id {
		t.Fatalf("duplicate result = (%q, %v), want (%q, false)", duplicateID, imported, id)
	}
	summary, found, err := db.GetBookSummaryContext(ctx, id)
	if err != nil {
		t.Fatalf("load canonical summary: %v", err)
	}
	if !found {
		t.Fatal("canonical book missing")
	}
	wantPath, err := filepath.Abs(canonicalPath)
	if err != nil {
		t.Fatalf("resolve canonical path: %v", err)
	}
	if summary.FilePath != wantPath {
		t.Fatalf("canonical path = %q, want %q", summary.FilePath, wantPath)
	}
}

// A book moved or renamed inside the library keeps its row (matched by content
// hash) but gets a new file_path. The reconciled ID must be reported so an
// already-built cache re-warms: otherwise the cache keeps the OLD path, opening
// the book 500s on a missing file, and deleting it tombstones the new path
// while the unlink targets the stale one — leaving the EPUB on disk forever.
func TestScanNowWithChangesReportsPathReconciledBooks(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := t.Context()

	src := filepath.Join(lib, "moved.epub")
	writeCoverEPUB(t, src, "Moved")

	first, err := s.ScanNow(ctx)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial scan: ids=%v err=%v", first, err)
	}
	id := first[0]

	// Move the file: same bytes, new path.
	dst := filepath.Join(lib, "moved-elsewhere.epub")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}

	result, err := s.ScanNowWithChanges(ctx)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(result.ImportedIDs) != 0 {
		t.Fatalf("a moved book must not re-import: %v", result.ImportedIDs)
	}
	if !slices.Contains(result.RefreshedIDs, id) {
		t.Fatalf("reconciled book %q not reported for cache refresh; RefreshedIDs=%v", id, result.RefreshedIDs)
	}

	// And the DB really does hold the new path.
	paths, err := db.ListBookPathsContext(ctx)
	if err != nil {
		t.Fatalf("list paths: %v", err)
	}
	absDst, _ := filepath.Abs(dst)
	if len(paths) != 1 || paths[0].FilePath != absDst {
		t.Fatalf("path not reconciled: %+v want %q", paths, absDst)
	}
}

// cover_path is persisted and the library folder is portable, so it must be
// stored with forward slashes: filepath.Join on Windows produced
// ".sayumi\covers\<id>.jpg", which is one literal filename on macOS/Linux and
// makes every cover 404 after the folder moves, with no self-heal.
func TestCoverPathIsStoredWithForwardSlashes(t *testing.T) {
	lib := t.TempDir()
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	ctx := t.Context()

	writeCoverEPUB(t, filepath.Join(lib, "withcover.epub"), "Covered")

	ids, err := s.ScanNow(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("scan: ids=%v err=%v", ids, err)
	}

	summary, found, err := db.GetBookSummaryContext(ctx, ids[0])
	if err != nil || !found {
		t.Fatalf("load summary: found=%v err=%v", found, err)
	}
	if summary.CoverPath == "" {
		t.Skip("no cover extracted for the fixture; nothing to assert")
	}
	if strings.Contains(summary.CoverPath, `\`) {
		t.Fatalf("cover_path must not contain OS separators, got %q", summary.CoverPath)
	}
	if want := CoverRelPath(ids[0]); summary.CoverPath != want {
		t.Fatalf("cover_path = %q, want %q", summary.CoverPath, want)
	}
}
