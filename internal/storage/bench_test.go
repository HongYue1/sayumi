package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"sayumi/internal/epub"
)

// seedBooks inserts n books through the production insert path so read
// benchmarks operate on a realistic row count.
func seedBooks(tb testing.TB, db *DB, n int) {
	tb.Helper()
	ctx := context.Background()
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

// seedBooksWithSpines inserts n books each carrying a realistic spine
// (spineLen entries) so cache-construction benchmarks reflect production-sized
// JSON rather than the empty "[]" spines of seedBooks.
func seedBooksWithSpines(tb testing.TB, db *DB, n, spineLen int) {
	tb.Helper()
	ctx := context.Background()

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
// lazy, this should track ListBooksContext cost and NOT the former per-book
// unmarshal that dominated cold start.
func BenchmarkNewBookCache(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooksWithSpines(b, db, 200, 120)
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewBookCache(ctx, db); err != nil {
			b.Fatalf("new book cache: %v", err)
		}
	}
}

func BenchmarkListBooks(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedBooks(b, db, 200)
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.ListBooksContext(ctx); err != nil {
			b.Fatalf("list books: %v", err)
		}
	}
}

func BenchmarkGetAllProgress(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
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
	ctx := context.Background()
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

// BenchmarkConcurrentReads compares read throughput under different SQLite
// connection-pool sizes. conns=1 is the current production setting
// (SetMaxOpenConns(1)); the larger sizes model the read-pool option under
// discussion. Every variant uses the exact production DSN/pragmas via
// dataSourceName, so the comparison is apples-to-apples. This benchmark touches
// no production code: it constructs raw *sql.DB handles purely to measure the
// trade-off before any architecture change is committed.
func BenchmarkConcurrentReads(b *testing.B) {
	for _, conns := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("conns=%d", conns), func(b *testing.B) {
			db := openRawForBench(b, conns)
			seedRawBooks(b, db, 200)
			ctx := context.Background()

			b.ReportAllocs()
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
