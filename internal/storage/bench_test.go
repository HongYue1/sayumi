package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"sayumi/internal/epub"
)

// seedBooks inserts n books through the production insert path so read
// benchmarks operate on a realistic row count.
func seedBooks(tb testing.TB, db *DB, n int) {
	tb.Helper()
	ctx := tb.Context()
	for i := range n {
		book := sampleBook(
			fmt.Sprintf("id%04d", i),
			fmt.Sprintf("hash-%04d", i),
			fmt.Sprintf("/lib/book%04d.epub", i),
		)
		if _, err := db.InsertBookContext(ctx, book); err != nil {
			tb.Fatalf("seed book %d: %v", i, err)
		}
	}
}

// seedBooksShuffledTitles inserts n books whose titles are a deterministic
// permutation of the insert order, so the library-list ORDER BY has real sorting
// work to do. seedBooks numbers titles in insert order, which lets SQLite sort an
// already-ordered input and therefore understates what a title index can save.
// 7919 is prime and coprime with any n used here, so (i*7919)%n is a permutation
// and every title stays unique.
func seedBooksShuffledTitles(tb testing.TB, db *DB, n int) {
	tb.Helper()
	ctx := tb.Context()
	for i := range n {
		book := sampleBook(
			fmt.Sprintf("id%04d", i),
			fmt.Sprintf("hash-%04d", i),
			fmt.Sprintf("/lib/book%04d.epub", i),
		)
		book.Title = fmt.Sprintf("Title %04d", (i*7919)%n)
		if _, err := db.InsertBookContext(ctx, book); err != nil {
			tb.Fatalf("seed book %d: %v", i, err)
		}
	}
}

// seedBooksWithSpines inserts n books each carrying a realistic spine
// (spineLen entries) so cache-construction benchmarks reflect production-sized
// JSON rather than the empty "[]" spines of seedBooks.
func seedBooksWithSpines(tb testing.TB, db *DB, n, spineLen int) {
	tb.Helper()
	ctx := tb.Context()

	spine := make([]epub.SpineEntry, spineLen)
	for i := range spine {
		spine[i] = epub.SpineEntry{
			Href:      fmt.Sprintf("text/chapter%04d.xhtml", i),
			ID:        fmt.Sprintf("chap%04d", i),
			MediaType: "application/xhtml+xml",
			Linear:    true,
		}
	}
	raw, err := json.Marshal(spine)
	if err != nil {
		tb.Fatalf("marshal spine: %v", err)
	}
	spineJSON := string(raw)

	for i := range n {
		book := sampleBook(
			fmt.Sprintf("id%04d", i),
			fmt.Sprintf("hash-%04d", i),
			fmt.Sprintf("/lib/book%04d.epub", i),
		)
		book.SpineJSON = spineJSON
		book.ChapterCount = spineLen
		if _, err := db.InsertBookContext(ctx, book); err != nil {
			tb.Fatalf("seed book %d: %v", i, err)
		}
	}
}

// BenchmarkNewBookCache measures profile-open cache construction over a
// realistic library (books carry full spines). Because spine parsing is now
// lazy, this should track ListBookSummariesContext cost and NOT the former
// per-book unmarshal that dominated cold start.
// BenchmarkOpenExistingLibrary measures startup against a library that already
// holds rows. Schema creation, additive column migrations, index creation, and
// the path-key reconcile all run inside Open, so this is what a user waits for
// before the first request is served. The reconcile reads every stored path,
// which makes it the part of startup that grows with library size.
func BenchmarkOpenExistingLibrary(b *testing.B) {
	dir := b.TempDir()
	seed, err := Open(dir)
	if err != nil {
		b.Fatalf("seed open: %v", err)
	}
	seedBooks(b, seed, 200)
	if err := seed.Close(); err != nil {
		b.Fatalf("seed close: %v", err)
	}

	for b.Loop() {
		db, err := Open(dir)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		if err := db.Close(); err != nil {
			b.Fatalf("close: %v", err)
		}
	}
}

// BenchmarkInsertBook covers the import write path: one row plus every index it
// maintains. A single import is rare, but the first scan of a large library runs
// this thousands of times back to back. Paths deliberately contain uppercase so
// the key derivation does the work it would do on a real library.
func BenchmarkInsertBook(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Errorf("close: %v", err)
		}
	})
	ctx := b.Context()

	i := 0
	for b.Loop() {
		book := sampleBook(
			fmt.Sprintf("bench%06d", i),
			fmt.Sprintf("bench-hash-%06d", i),
			fmt.Sprintf("/lib/Bench Books/Book%06d.epub", i),
		)
		if _, err := db.InsertBookContext(ctx, book); err != nil {
			b.Fatalf("insert: %v", err)
		}
		i++
	}
}

// BenchmarkBookCacheAddRetitle measures the ordered reinsert Add performs when a
// known book's title changes: the O(N) order-slice delete, the binary search that
// picks the new slot, and the O(N) insert. Retitling one seeded book keeps the
// cache size fixed so runs stay comparable, and isolates comparison cost from map
// growth. Alternating between a first-position and a last-position title keeps
// every iteration a real title change rather than Add's unchanged-title fast path.
func BenchmarkBookCacheAddRetitle(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Errorf("close: %v", err)
		}
	})
	seedBooks(b, db, 2000)

	cache, err := NewBookCache(b.Context(), db)
	if err != nil {
		b.Fatalf("new book cache: %v", err)
	}
	book, ok := cache.Get("id1000")
	if !ok {
		b.Fatal("seeded book id1000 missing from cache")
	}

	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if i%2 == 0 {
			book.Title = "Zzz Retitled"
		} else {
			book.Title = "Aaa Retitled"
		}
		cache.Add(book)
		i++
	}
}

func BenchmarkNewBookCache(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooksWithSpines(b, db, 200, 120)
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewBookCache(ctx, db); err != nil {
			b.Fatalf("new book cache: %v", err)
		}
	}
}

const spineBurstCallers = 16

// BenchmarkGetSpineColdBurst compares the old independent-load behavior with
// the singleflight path when many requests open the same uncached book at once.
func BenchmarkGetSpineColdBurst(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooksWithSpines(b, db, 1, 120)
	ctx := b.Context()
	cache, err := NewBookCache(ctx, db)
	if err != nil {
		b.Fatalf("new book cache: %v", err)
	}
	const bookID = "id0000"

	b.Run("before/no_dedupe", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(spineBurstCallers, "loads/op")
		for b.Loop() {
			if err := runSpineBurst(spineBurstCallers, func() error {
				raw, _, err := db.GetBookContentContext(ctx, bookID)
				if err != nil {
					return err
				}
				_, err = parseSpine(bookID, raw)
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("after/singleflight", func(b *testing.B) {
		originalLoad := cache.loadBookContent
		var loads atomic.Uint64
		cache.loadBookContent = func(ctx context.Context, id string) (string, string, error) {
			loads.Add(1)
			return originalLoad(ctx, id)
		}

		b.ReportAllocs()
		for b.Loop() {
			cache.mu.Lock()
			delete(cache.spines, bookID)
			cache.mu.Unlock()

			if err := runSpineBurst(spineBurstCallers, func() error {
				_, found, err := cache.GetSpine(ctx, bookID)
				if err != nil {
					return err
				}
				if !found {
					return errors.New("book disappeared during benchmark")
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(loads.Load())/float64(b.N), "loads/op")
	})
}

func runSpineBurst(callers int, fn func() error) error {
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			errs <- fn()
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

func BenchmarkListBookSummariesUnsortedTitles(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooksShuffledTitles(b, db, 200)
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.ListBookSummariesContext(ctx); err != nil {
			b.Fatalf("list book summaries: %v", err)
		}
	}
}

func BenchmarkListBookSummaries(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooks(b, db, 200)
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.ListBookSummariesContext(ctx); err != nil {
			b.Fatalf("list book summaries: %v", err)
		}
	}
}

func BenchmarkGetAllProgress(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	ctx := b.Context()
	seedBooks(b, db, 200)
	for i := range 200 {
		if err := db.SaveProgressContext(ctx, ProgressRecord{
			BookID:  fmt.Sprintf("id%04d", i),
			UserID:  "default",
			Chapter: i % 30,
			Percent: 0.5,
		}); err != nil {
			b.Fatalf("seed progress %d: %v", i, err)
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.GetAllProgressContext(ctx, "default"); err != nil {
			b.Fatalf("get all progress: %v", err)
		}
	}
}

func BenchmarkSaveProgress(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	ctx := b.Context()
	if _, err := db.InsertBookContext(ctx, sampleBook("id1", "hash-a", "/lib/a.epub")); err != nil {
		b.Fatalf("insert book: %v", err)
	}

	b.ReportAllocs()
	chapter := 0
	for b.Loop() {
		chapter = (chapter + 1) % 50
		if err := db.SaveProgressContext(ctx, ProgressRecord{
			BookID:  "id1",
			UserID:  "default",
			Chapter: chapter,
			Percent: 0.5,
		}); err != nil {
			b.Fatalf("save progress: %v", err)
		}
	}
}

// BenchmarkConcurrentReads compares steady-state read throughput under
// different SQLite connection-pool sizes. Production uses four pre-warmed
// connections; conns=1/2 model constrained pools, while conns=8 measures
// whether a larger pool adds useful throughput. Every variant uses the exact
// production DSN/pragmas and is pre-warmed before timing so startup work does
// not distort the comparison.
func BenchmarkConcurrentReads(b *testing.B) {
	for _, conns := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("conns=%d", conns), func(b *testing.B) {
			db := openRawForBench(b, conns)
			seedRawBooks(b, db, 200)
			warmRawPool(b, db, conns)
			ctx := b.Context()

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := benchScanBooks(ctx, db); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}

// benchScanBooks runs the read query and fully drains the rows, closing them
// via defer so sqlclosecheck is satisfied even though the call site is a loop.
func benchScanBooks(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT id, title, author FROM books")
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, title, author string
		if err := rows.Scan(&id, &title, &author); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}
	return nil
}

func openRawForBench(b *testing.B, maxConns int) *sql.DB {
	b.Helper()
	dir := filepath.Join(b.TempDir(), ".sayumi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dir, "sayumi.db")
	sqlDB, err := sql.Open("sqlite", dataSourceName(dbPath))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns)
	if _, err := sqlDB.Exec(schema); err != nil {
		b.Fatalf("schema: %v", err)
	}
	b.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func warmRawPool(b *testing.B, db *sql.DB, n int) {
	b.Helper()
	ctx := b.Context()
	conns := make([]*sql.Conn, 0, n)
	for i := range n {
		conn, err := db.Conn(ctx)
		if err != nil {
			for _, opened := range conns {
				_ = opened.Close()
			}
			b.Fatalf("warm connection %d: %v", i, err)
		}
		conns = append(conns, conn)
	}

	var closeErr error
	for _, conn := range conns {
		if err := conn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if closeErr != nil {
		b.Fatalf("release warm connections: %v", closeErr)
	}
}

func seedRawBooks(b *testing.B, db *sql.DB, n int) {
	b.Helper()
	for i := range n {
		_, err := db.Exec(
			"INSERT INTO books (id, title, author, file_path) VALUES (?, ?, ?, ?)",
			fmt.Sprintf("id%04d", i),
			fmt.Sprintf("Title %04d", i),
			"Author",
			fmt.Sprintf("/lib/book%04d.epub", i),
		)
		if err != nil {
			b.Fatalf("seed book %d: %v", i, err)
		}
	}
}
