package storage

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestListBookSummariesStableTitleTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()

	// Same title, different ids: order must be title then id, not insertion order.
	mustInsertBook(t, db, sampleBook("z-book", "hash-z", "/lib/z.epub"))
	mustInsertBook(t, db, sampleBook("a-book", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("m-book", "hash-m", "/lib/m.epub"))

	// Force identical titles after insert so the id tie-breaker is exercised.
	// Use distinct hashes so the partial unique index is not involved.
	for _, id := range []string{"z-book", "a-book", "m-book"} {
		hash := "hash-title-" + id
		if err := db.UpdateBookMetadataAndFileContext(ctx, id, "Same Title", "Author", hash, 10); err != nil {
			t.Fatalf("set title for %s: %v", id, err)
		}
	}

	got, err := db.ListBookSummariesContext(ctx)
	if err != nil {
		t.Fatalf("list book summaries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("book count = %d, want 3", len(got))
	}
	wantIDs := []string{"a-book", "m-book", "z-book"}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, got[i].ID, id, idsOf(got))
		}
		if got[i].Title != "Same Title" {
			t.Errorf("title[%d] = %q, want Same Title", i, got[i].Title)
		}
	}
}

func idsOf(books []BookSummary) []string {
	out := make([]string, len(books))
	for i, b := range books {
		out[i] = b.ID
	}
	return out
}

// Require scalar fields so adding mutable backing storage forces a review of
// row ownership. Copying a struct alone would not isolate slices or maps if a
// future scanner reused their backing storage across result rows.
func TestBookSummaryFieldsAreValueTypes(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[BookSummary]()
	for field := range typ.Fields() {
		switch field.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map,
			reflect.Chan, reflect.Func, reflect.Interface, reflect.UnsafePointer:
			t.Errorf("BookSummary.%s is %s: reference-typed fields alias across rows in "+
				"ListBookSummariesContext -- give it its own scan loop or drop the reused "+
				"destination slice (see bookSummaryScanDest in books.go)", field.Name, field.Type.Kind())
		}
	}
}

// TestIgnoredFileSurvivesLibraryPathCaseChange pins the tombstone half of path
// identity. Deleting a book records its exact path in ignored_files, so on a
// case-insensitive volume, reopening the same library under a differently-cased
// path -- c:\books instead of C:\Books, one keystroke on --library -- misses the
// tombstone and the next scan resurrects the deleted book.
func TestIgnoredFileSurvivesLibraryPathCaseChange(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	db.foldPaths = true // library lives on NTFS / APFS / exFAT
	ctx := t.Context()

	mustInsertBook(t, db, sampleBook("b1", "hash-1", "/Library/Books/Novel.epub"))
	if err := db.DeleteBookContext(ctx, "b1"); err != nil {
		t.Fatalf("delete book: %v", err)
	}

	ignored, err := db.IsFileIgnoredContext(ctx, "/library/books/novel.epub")
	if err != nil {
		t.Fatalf("is file ignored: %v", err)
	}
	if !ignored {
		t.Error("deleted book is not ignored once the library path case changes: the next scan re-imports it")
	}

	// Re-adding the same file must clear the tombstone regardless of case, or the
	// upload path can never un-ignore what a differently-cased delete recorded.
	if err := db.RemoveIgnoredFileContext(ctx, "/library/books/novel.epub"); err != nil {
		t.Fatalf("remove ignored file: %v", err)
	}
	stillIgnored, err := db.IsFileIgnoredContext(ctx, "/Library/Books/Novel.epub")
	if err != nil {
		t.Fatalf("is file ignored after removal: %v", err)
	}
	if stillIgnored {
		t.Error("tombstone survived removal under a different path case")
	}
}

// TestBookExistsByPathFoldsCaseOnFoldingVolume pins the dedup half: a book that
// is already imported must be recognized under any case the volume considers
// equal, or the scanner treats it as new and the content-hash path has to repoint
// every row on every rescan.
func TestBookExistsByPathFoldsCaseOnFoldingVolume(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	db.foldPaths = true
	ctx := t.Context()

	mustInsertBook(t, db, sampleBook("b1", "hash-1", "/Library/Books/Novel.epub"))

	id, found, err := db.BookExistsByPathContext(ctx, "/library/BOOKS/novel.epub")
	if err != nil {
		t.Fatalf("check book by path: %v", err)
	}
	if !found {
		t.Fatal("imported book not found under a different path case: the scanner re-imports it")
	}
	if id != "b1" {
		t.Errorf("book id = %q, want b1", id)
	}
}

// TestPathKeyKeepsCaseOnCaseSensitiveVolume is the other side of the contract:
// folding must NOT be applied on a case-sensitive volume, where Novel.epub and
// novel.epub are two different files that both deserve their own row.
func TestPathKeyKeepsCaseOnCaseSensitiveVolume(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	db.foldPaths = false
	ctx := t.Context()

	mustInsertBook(t, db, sampleBook("b1", "hash-1", "/library/Novel.epub"))

	if got, want := db.PathKey("/library/./Novel.epub"), "/library/Novel.epub"; got != want {
		t.Errorf("PathKey did not normalize the path: got %q, want %q", got, want)
	}
	if _, found, err := db.BookExistsByPathContext(ctx, "/library/novel.epub"); err != nil {
		t.Fatalf("check book by path: %v", err)
	} else if found {
		t.Error("case-sensitive volume: Novel.epub and novel.epub must stay distinct books")
	}
}

// TestDetectPathFoldingAgreesWithFilesystem checks the probe against an
// independent experiment on the same directory, so it stays correct on a
// case-sensitive CI box and on a developer's Windows or macOS volume, and
// verifies the probe leaves no file behind.
func TestDetectPathFoldingAgreesWithFilesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "probe"), nil, 0o600); err != nil {
		t.Fatalf("write probe file: %v", err)
	}
	_, statErr := os.Lstat(filepath.Join(dir, "PROBE"))
	wantFold := statErr == nil

	if got := detectPathFolding(dir); got != wantFold {
		t.Errorf("detectPathFolding = %v, but this filesystem folds case = %v", got, wantFold)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("probe left %d entries behind, want only the test's own file", len(entries))
	}
}

func TestBookUpdateMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()

	if err := db.UpdateBookFilePathContext(ctx, "missing", "/lib/x.epub"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookFilePathContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookCoverContext(ctx, "missing", "covers/x.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookCoverContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookMetadataAndFileContext(ctx, "missing", "t", "a", "h", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookMetadataAndFileContext err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateBookCoverAndFileContext(ctx, "missing", "covers/x.jpg", "h", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateBookCoverAndFileContext err = %v, want ErrNotFound", err)
	}
	if err := db.MarkCoverCheckedContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkCoverCheckedContext err = %v, want ErrNotFound", err)
	}
}

func TestUpdateBookMetadataFileHashConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("id1", "hash-a", "/lib/a.epub"))
	mustInsertBook(t, db, sampleBook("id2", "hash-b", "/lib/b.epub"))

	// Preflight path: assertFileHashFree should reject adopting another book's hash.
	err := db.UpdateBookMetadataAndFileContext(ctx, "id2", "Title id2", "Author", "hash-a", 99)
	if !errors.Is(err, ErrFileHashConflict) {
		t.Fatalf("metadata hash conflict err = %v, want ErrFileHashConflict", err)
	}
	err = db.UpdateBookCoverAndFileContext(ctx, "id2", "covers/id2.jpg", "hash-a", 99)
	if !errors.Is(err, ErrFileHashConflict) {
		t.Fatalf("cover hash conflict err = %v, want ErrFileHashConflict", err)
	}

	// Unchanged row still has its original hash.
	got, err := db.GetBookContext(ctx, "id2")
	if err != nil {
		t.Fatalf("get book: %v", err)
	}
	if got.FileHash != "hash-b" {
		t.Errorf("file hash = %q, want hash-b", got.FileHash)
	}
}

func TestGetBookContentAndSummary(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()

	book := sampleBook("id1", "hash-a", "/lib/a.epub")
	book.SpineJSON = `[{"id":"c1"}]`
	book.TocJSON = `[{"label":"One"}]`
	mustInsertBook(t, db, book)

	spine, toc, err := db.GetBookContentContext(ctx, "id1")
	if err != nil {
		t.Fatalf("get book content: %v", err)
	}
	if spine != book.SpineJSON || toc != book.TocJSON {
		t.Errorf("content = (%q, %q), want (%q, %q)", spine, toc, book.SpineJSON, book.TocJSON)
	}
	if _, _, err := db.GetBookContentContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing content err = %v, want ErrNotFound", err)
	}

	summary, found, err := db.GetBookSummaryContext(ctx, "id1")
	if err != nil || !found {
		t.Fatalf("get summary: found=%v err=%v", found, err)
	}
	if summary.Title != "Title id1" || summary.FileHash != "hash-a" {
		t.Errorf("summary = %+v", summary)
	}
	_, found, err = db.GetBookSummaryContext(ctx, "missing")
	if err != nil || found {
		t.Errorf("missing summary found=%v err=%v, want false/nil", found, err)
	}
}

func TestInsertBookConcurrentSameHash(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()

	const workers = 8
	var (
		wg   sync.WaitGroup
		ids  = make([]string, workers)
		errs = make([]error, workers)
	)
	for i := range workers {
		wg.Go(func() {
			book := sampleBook(fmt.Sprintf("id-%d", i), "hash-shared", fmt.Sprintf("/lib/%d.epub", i))
			ids[i], errs[i] = db.InsertBookContext(ctx, book)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	canonical := ids[0]
	for i, id := range ids {
		if id != canonical {
			t.Fatalf("worker %d canonical id = %q, want %q (all=%v)", i, id, canonical, ids)
		}
	}
	books, err := db.ListBookSummariesContext(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("book count = %d, want 1", len(books))
	}
}

func TestBookReadsHonorCancellation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	mustInsertBook(t, db, sampleBook("book", "hash", "/lib/book.epub"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, read := range map[string]func() error{
		"summaries": func() error {
			_, err := db.ListBookSummariesContext(ctx)
			return err
		},
		"paths": func() error {
			_, err := db.ListBookPathsContext(ctx)
			return err
		},
		"content": func() error {
			_, _, err := db.GetBookContentContext(ctx, "book")
			return err
		},
		"summary": func() error {
			_, _, err := db.GetBookSummaryContext(ctx, "book")
			return err
		},
		"book": func() error {
			_, err := db.GetBookContext(ctx, "book")
			return err
		},
		"hash": func() error {
			_, _, _, err := db.GetBookIDByHashContext(ctx, "hash")
			return err
		},
		"path_lookup": func() error {
			_, _, err := db.BookExistsByPathContext(ctx, "/lib/book.epub")
			return err
		},
		"covers": func() error {
			_, err := db.ListBooksMissingCoversContext(ctx)
			return err
		},
		"ignored_lookup": func() error {
			_, err := db.IsFileIgnoredContext(ctx, "/lib/book.epub")
			return err
		},
		"ignored_paths": func() error {
			_, err := db.ListIgnoredPathsContext(ctx)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestBookPathScansKeepRowsIndependent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	want := make(map[string]string)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		path := "/lib/" + id + ".epub"
		mustInsertBook(t, db, sampleBook(id, id, path))
		want[id] = path
	}
	paths, err := db.ListBookPathsContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, path := range paths {
		got[path.ID] = path.FilePath
	}
	if len(paths) != len(want) || !maps.Equal(got, want) {
		t.Fatalf("book paths = %v, want %v", paths, want)
	}

	for id := range want {
		if err := db.DeleteBookContext(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	ignored, err := db.ListIgnoredPathsContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ignored)
	if !slices.Equal(ignored, []string{"/lib/alpha.epub", "/lib/beta.epub", "/lib/gamma.epub"}) {
		t.Fatalf("ignored paths = %v", ignored)
	}
}

func TestDeleteBookRollsBackWhenIgnoringFails(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := t.Context()
	mustInsertBook(t, db, sampleBook("book", "hash", "/lib/book.epub"))
	if err := db.SaveProgressContext(ctx, ProgressRecord{BookID: "book", UserID: "reader", Chapter: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER reject_ignored BEFORE INSERT ON ignored_files
		BEGIN SELECT RAISE(ABORT, 'tombstone write rejected'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteBookContext(ctx, "book"); err == nil {
		t.Fatal("delete succeeded despite a failed tombstone write")
	}
	if _, err := db.GetBookContext(ctx, "book"); err != nil {
		t.Fatalf("book was not restored: %v", err)
	}
	if progress, err := db.GetProgressContext(ctx, "book", "reader"); err != nil || progress.Chapter != 2 {
		t.Fatalf("cascaded progress was not restored: %+v, %v", progress, err)
	}
	if ignored, err := db.IsFileIgnoredContext(ctx, "/lib/book.epub"); err != nil || ignored {
		t.Fatalf("failed deletion left a tombstone: ignored=%v err=%v", ignored, err)
	}
}
