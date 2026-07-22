package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenEnablesPragmasAndSchema(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1 (DSN _pragma=foreign_keys(1) not applied)", foreignKeys)
	}

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	// Critical indexes from schema must exist after Open/migrate.
	wantIndexes := []string{
		"idx_books_file_hash",
		"idx_books_cover_unchecked",
		"idx_books_file_hash_uniq",
		"idx_bookmarks_book",
	}
	for _, name := range wantIndexes {
		var found string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&found)
		if err != nil {
			t.Fatalf("index %s missing after Open: %v", name, err)
		}
	}

	cols, err := db.tableColumns("books")
	if err != nil {
		t.Fatalf("tableColumns books: %v", err)
	}
	if !cols["cover_checked"] {
		t.Fatal("books.cover_checked missing after Open")
	}
}

func TestMigrateAddsCoverCheckedAndReconciles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sayumi", "legacy.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Realistic pre-cover_checked books shape: full current columns except
	// cover_checked. CREATE TABLE IF NOT EXISTS will not widen this table, so
	// migrate must ADD cover_checked before indexes that reference it, then
	// reconcile already-covered rows. file_hash (and other indexed columns)
	// must already exist — real upgrades never lacked those.
	raw, err := sql.Open("sqlite", dataSourceName(dbPath))
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer func() { _ = raw.Close() }()

	if _, err := raw.ExecContext(context.Background(), `
		CREATE TABLE books (
			id            TEXT PRIMARY KEY,
			title         TEXT NOT NULL DEFAULT '',
			author        TEXT NOT NULL DEFAULT '',
			language      TEXT NOT NULL DEFAULT '',
			publisher     TEXT NOT NULL DEFAULT '',
			description   TEXT NOT NULL DEFAULT '',
			pub_date      TEXT NOT NULL DEFAULT '',
			isbn          TEXT NOT NULL DEFAULT '',
			file_path     TEXT NOT NULL UNIQUE,
			file_hash     TEXT NOT NULL DEFAULT '',
			file_size     INTEGER NOT NULL DEFAULT 0,
			cover_path    TEXT NOT NULL DEFAULT '',
			has_cover     INTEGER NOT NULL DEFAULT 0,
			spine_json    TEXT NOT NULL DEFAULT '[]',
			toc_json      TEXT NOT NULL DEFAULT '[]',
			direction     TEXT NOT NULL DEFAULT 'ltr',
			chapter_count INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
		);
		INSERT INTO books (id, file_path, file_hash, has_cover) VALUES
			('with', '/lib/with.epub', 'hash-with', 1),
			('without', '/lib/without.epub', 'hash-without', 0);
	`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	db := &DB{DB: raw}
	if err := db.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cols, err := db.tableColumns("books")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if !cols["cover_checked"] {
		t.Fatal("migrate did not add books.cover_checked")
	}

	var withChecked, withoutChecked int
	if err := db.QueryRowContext(context.Background(),
		`SELECT cover_checked FROM books WHERE id = 'with'`).Scan(&withChecked); err != nil {
		t.Fatalf("read with: %v", err)
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT cover_checked FROM books WHERE id = 'without'`).Scan(&withoutChecked); err != nil {
		t.Fatalf("read without: %v", err)
	}
	if withChecked != 1 {
		t.Fatalf("has_cover book cover_checked = %d, want 1 after reconcile", withChecked)
	}
	if withoutChecked != 0 {
		t.Fatalf("no-cover book cover_checked = %d, want 0", withoutChecked)
	}
}

func TestDataSourceNameUsesModerncPragmas(t *testing.T) {
	t.Parallel()
	dsn := dataSourceName(`C:\lib\sayumi.db`)
	for _, frag := range []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=cache_size(-32000)",
		"_pragma=mmap_size(268435456)",
	} {
		if !strings.Contains(dsn, frag) {
			t.Errorf("DSN missing %s\nDSN=%s", frag, dsn)
		}
	}
	// Guard against regressing to mattn-style keys that modernc silently ignores.
	for _, bad := range []string{"_journal_mode=", "_foreign_keys=", "_busy_timeout="} {
		if strings.Contains(dsn, bad) {
			t.Errorf("DSN contains ignored mattn-style key %q\nDSN=%s", bad, dsn)
		}
	}
}
