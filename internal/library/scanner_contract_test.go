package library

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"sayumi/internal/storage"
)

func testLibraryDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return path
}

func TestScanNowAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := testLibraryDir(t)
	for _, path := range []string{root, filepath.Join(root, "missing")} {
		s := NewScanner(path, nil)
		result, err := s.ScanNowWithChanges(ctx)
		if !errors.Is(err, context.Canceled) || len(result.ImportedIDs) != 0 || len(result.RefreshedIDs) != 0 {
			t.Fatalf("canceled scan of %q = %+v, %v", path, result, err)
		}
	}
}

type scanWaitContext struct {
	context.Context
	ready chan struct{}
	once  sync.Once
}

func (c *scanWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.ready) })
	return c.Context.Done()
}

func TestScanWaiterReceivesPartialResult(t *testing.T) {
	t.Parallel()
	call := &scanCall{done: make(chan struct{})}
	s := &Scanner{current: call}
	ctx := &scanWaitContext{Context: t.Context(), ready: make(chan struct{})}
	type outcome struct {
		result ScanResult
		err    error
	}
	resultCh := make(chan outcome, 1)
	var wg sync.WaitGroup
	release := sync.OnceFunc(func() { close(call.done) })
	defer func() {
		release()
		wg.Wait()
	}()
	wg.Go(func() {
		result, err := s.ScanNowWithChanges(ctx)
		resultCh <- outcome{result, err}
	})
	select {
	case <-ctx.ready:
	case result := <-resultCh:
		t.Fatalf("waiter returned before publication: %+v", result)
	}
	call.result = ScanResult{ImportedIDs: []string{"new"}, RefreshedIDs: []string{"updated"}}
	call.err = context.DeadlineExceeded
	release()
	got := <-resultCh
	if !errors.Is(got.err, context.DeadlineExceeded) || !slices.Equal(got.result.ImportedIDs, call.result.ImportedIDs) || !slices.Equal(got.result.RefreshedIDs, call.result.RefreshedIDs) {
		t.Fatalf("published result = %+v, %v", got.result, got.err)
	}
}

func TestScanWaiterCancellation(t *testing.T) {
	t.Parallel()
	call := &scanCall{done: make(chan struct{})}
	s := &Scanner{current: call}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanWaitContext{Context: parent, ready: make(chan struct{})}
	resultCh := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		close(call.done)
	}()
	wg.Go(func() {
		_, err := s.ScanNowWithChanges(ctx)
		resultCh <- err
	})
	select {
	case <-ctx.ready:
	case err := <-resultCh:
		t.Fatalf("waiter returned before cancellation: %v", err)
	}
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want cancellation", err)
	}
	if s.current != call {
		t.Fatal("canceling a waiter cleared the leader's scan")
	}
}

type scanLogHook struct{ onRecord func(slog.Record) }

func (h scanLogHook) Enabled(context.Context, slog.Level) bool { return true }
func (h scanLogHook) Handle(_ context.Context, record slog.Record) error {
	h.onRecord(record)
	return nil
}
func (h scanLogHook) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h scanLogHook) WithGroup(string) slog.Handler      { return h }

func hookScanLogs(t *testing.T, onRecord func(slog.Record)) {
	t.Helper()
	// These tests are deliberately serial because slog's default is process-wide.
	previous := slog.Default()
	slog.SetDefault(slog.New(scanLogHook{onRecord: onRecord}))
	t.Cleanup(func() { slog.SetDefault(previous) })
}

func insertPendingCover(t *testing.T, db *storage.DB, id, path string) {
	t.Helper()
	got, err := db.InsertBookContext(t.Context(), storage.BookRecord{
		ID: id, Title: id, FilePath: path, FileHash: "hash-" + id,
		SpineJSON: "[]", TocJSON: "[]",
	})
	if err != nil || got != id {
		t.Fatalf("insert pending cover = %q, %v", got, err)
	}
}

func TestScanNowCanceledDuringBackfill(t *testing.T) {
	lib := testLibraryDir(t)
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	path := filepath.Join(lib, "pending.epub")
	writeCoverEPUB(t, path, "Pending")
	insertPendingCover(t, db, "pending", path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hookScanLogs(t, func(record slog.Record) {
		if record.Message == "backfilling covers" {
			cancel()
		}
	})
	result, err := s.ScanNowWithChanges(ctx)
	if !errors.Is(err, context.Canceled) || len(result.ImportedIDs) != 0 || len(result.RefreshedIDs) != 0 {
		t.Fatalf("canceled backfill = %+v, %v", result, err)
	}
	pending, err := db.ListBooksMissingCoversContext(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("canceled cover must remain retryable: %v, %v", pending, err)
	}
	result, err = s.ScanNowWithChanges(t.Context())
	if err != nil || !slices.Equal(result.RefreshedIDs, []string{"pending"}) {
		t.Fatalf("retry = %+v, %v", result, err)
	}
}

func TestScanNowReportsBackfillQueryFailure(t *testing.T) {
	lib := testLibraryDir(t)
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	writeMinimalEPUB(t, filepath.Join(lib, "new.epub"), "New")
	var once sync.Once
	var closeErr error
	hookScanLogs(t, func(record slog.Record) {
		if record.Message == "imported book" {
			once.Do(func() { closeErr = db.Close() })
		}
	})
	result, err := s.ScanNowWithChanges(t.Context())
	if closeErr != nil {
		t.Fatalf("close storage: %v", closeErr)
	}
	if err == nil || len(result.ImportedIDs) != 1 {
		t.Fatalf("backfill failure must preserve committed imports: %+v, %v", result, err)
	}
}

func TestCoverFilesystemFailureRemainsRetryable(t *testing.T) {
	t.Parallel()
	lib := testLibraryDir(t)
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	path := filepath.Join(lib, "covered.epub")
	writeCoverEPUB(t, path, "Covered")
	blocker := filepath.Join(lib, ".sayumi", "covers")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, imported, err := s.ImportUploadedFile(t.Context(), path, "")
	if err != nil || !imported || id == "" {
		t.Fatalf("import = %q, %v, %v", id, imported, err)
	}
	pending, err := db.ListBooksMissingCoversContext(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("filesystem failure must remain pending: %v, %v", pending, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	result, err := s.ScanNowWithChanges(t.Context())
	if err != nil || !slices.Equal(result.RefreshedIDs, []string{id}) {
		t.Fatalf("cover retry = %+v, %v", result, err)
	}
	summary, found, err := db.GetBookSummaryContext(t.Context(), id)
	if err != nil || !found || !summary.HasCover || summary.CoverPath != CoverRelPath(id) {
		t.Fatalf("retried cover = %+v, %v, %v", summary, found, err)
	}
}

func TestBackfillMissingFileRemainsRetryable(t *testing.T) {
	t.Parallel()
	lib := testLibraryDir(t)
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	path := filepath.Join(lib, "temporarily-missing.epub")
	insertPendingCover(t, db, "missing", path)
	if s.backfillCover(t.Context(), storage.BookPath{ID: "missing", FilePath: path}) {
		t.Fatal("missing EPUB unexpectedly resolved a cover")
	}
	pending, err := db.ListBooksMissingCoversContext(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("missing EPUB must remain pending: %v, %v", pending, err)
	}
	writeCoverEPUB(t, path, "Restored")
	result, err := s.ScanNowWithChanges(t.Context())
	if err != nil || !slices.Equal(result.RefreshedIDs, []string{"missing"}) {
		t.Fatalf("restored EPUB retry = %+v, %v", result, err)
	}
}

func TestUnrenderableCoversAreResolved(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		coverPath string
		data      []byte
	}{
		{"undeclared", "", nil},
		{"missing_entry", "missing.png", encodePNG(t, 12, 18)},
		{"invalid_image", "cover.png", []byte("not an image")},
		{"pixel_limit", "cover.png", pngWithDimensions(t, 5000, 5000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := testLibraryDir(t)
			db := openTestDB(t, lib)
			s := NewScanner(lib, db)
			insertPendingCover(t, db, "book", filepath.Join(lib, "book.epub"))
			zr := coverZIP(t, "cover.png", tc.data)
			if s.resolveBookCover(t.Context(), "book", "Book", tc.coverPath, zr) {
				t.Fatal("unrenderable cover reported a visible change")
			}
			pending, err := db.ListBooksMissingCoversContext(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("permanent cover failure remained pending: %v, %v", pending, err)
			}
			summary, found, err := db.GetBookSummaryContext(t.Context(), "book")
			if err != nil || !found || summary.HasCover || summary.CoverPath != "" {
				t.Fatalf("unrenderable cover advertised as present: %+v, %v, %v", summary, found, err)
			}
			if _, err := os.Stat(filepath.Join(lib, filepath.FromSlash(CoverRelPath("book")))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unrenderable cover created a sidecar: %v", err)
			}
		})
	}
}

func TestBackfillUnparseableEPUBIsResolved(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writer, err := zw.Create("META-INF/container.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("<container>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"invalid_zip", []byte("not a zip")},
		{"invalid_xml", buf.Bytes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := testLibraryDir(t)
			db := openTestDB(t, lib)
			s := NewScanner(lib, db)
			path := filepath.Join(lib, "broken.epub")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			insertPendingCover(t, db, "broken", path)
			if s.backfillCover(t.Context(), storage.BookPath{ID: "broken", FilePath: path}) {
				t.Fatal("unparseable EPUB reported a visible cover change")
			}
			pending, err := db.ListBooksMissingCoversContext(t.Context())
			if err != nil || len(pending) != 0 {
				t.Fatalf("unparseable EPUB remained pending: %v, %v", pending, err)
			}
		})
	}
}

func TestScanCancellationPreservesCommittedImport(t *testing.T) {
	lib := testLibraryDir(t)
	db := openTestDB(t, lib)
	s := NewScanner(lib, db)
	writeMinimalEPUB(t, filepath.Join(lib, "new.epub"), "New")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hookScanLogs(t, func(record slog.Record) {
		if record.Message == "imported book" {
			cancel()
		}
	})
	result, err := s.ScanNowWithChanges(ctx)
	if !errors.Is(err, context.Canceled) || len(result.ImportedIDs) != 1 {
		t.Fatalf("committed import was lost on cancellation: %+v, %v", result, err)
	}
	if _, found, err := db.GetBookSummaryContext(t.Context(), result.ImportedIDs[0]); err != nil || !found {
		t.Fatalf("reported import was not committed: found=%v err=%v", found, err)
	}
	if ids, err := s.ScanNow(t.Context()); err != nil || len(ids) != 0 {
		t.Fatalf("retry imported a duplicate: %v, %v", ids, err)
	}
}

// checkHookContext runs a test action on a specific Err call, exercising
// cancellation and file growth at stage boundaries without timing assumptions.
type checkHookContext struct {
	context.Context
	checks atomic.Int32
	at     int32
	action func()
}

func (c *checkHookContext) Err() error {
	if c.checks.Add(1) == c.at {
		c.action()
	}
	return c.Context.Err()
}

func TestHashFileCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(testLibraryDir(t), "book.epub")
	if err := os.WriteFile(path, make([]byte, 3<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &checkHookContext{Context: parent, at: 3, action: cancel}
	if hash, size, err := HashFile(ctx, path); hash != "" || size != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash = %q, %d, %v", hash, size, err)
	}
	if hash, size, err := HashFile(parent, path+".missing"); hash != "" || size != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("already-canceled hash = %q, %d, %v", hash, size, err)
	}
}

func TestHashFileSizeMatchesHashedBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(testLibraryDir(t), "growing.epub")
	data := bytes.Repeat([]byte{0x5a}, 1<<20)
	tail := []byte("appended while hashing")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := &checkHookContext{Context: t.Context(), at: 2, action: func() {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(tail)
		closeErr := f.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			t.Fatal(err)
		}
	}}
	hash, size, err := HashFile(ctx, path)
	digest := sha256.Sum256(append(data, tail...))
	want := hex.EncodeToString(digest[:])
	if err != nil || hash != want || size != int64(len(data)+len(tail)) {
		t.Fatalf("hash/size = %q/%d, %v; want %q/%d", hash, size, err, want, len(data)+len(tail))
	}
}

func TestGenerateIDCompatibility(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "/library/book.epub", `C:\Library\Book.epub`, "/日本語/كتاب.epub", strings.Repeat("long/", 100)} {
		for _, hash := range []string{"", strings.Repeat("a", 64), strings.Repeat("b", 64)} {
			digest := sha256.Sum256([]byte(path + hash))
			want := hex.EncodeToString(digest[:8])
			if got := generateID(path, hash); got != want {
				t.Fatalf("generateID(%q, %q) = %q, want %q", path, hash, got, want)
			}
		}
	}
}
