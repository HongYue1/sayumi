package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestListBookSummariesStableTitleTies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

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

// TestBookSummaryFieldsAreValueTypes pins the invariant that makes the reused
// scan-destination slice in ListBookSummariesContext safe: every BookSummary
// field must be a value type, so appending the reused struct copies it and no
// returned row can alias another. Adding a pointer, slice, map, or interface
// field would silently make every row share state with the last one scanned --
// a bug that would not show up as a compile error and only some of the time in
// behavioral tests. Fail loudly here instead.
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
	ctx := context.Background()

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
	ctx := context.Background()

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
	ctx := context.Background()

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
	ctx := context.Background()

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
	ctx := context.Background()
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
	ctx := context.Background()

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
	ctx := context.Background()

	const workers = 8
	var (
		wg   sync.WaitGroup
		ids  = make([]string, workers)
		errs = make([]error, workers)
	)
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			book := sampleBook(fmt.Sprintf("id-%d", i), "hash-shared", fmt.Sprintf("/lib/%d.epub", i))
			ids[i], errs[i] = db.InsertBookContext(ctx, book)
		}(i)
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
