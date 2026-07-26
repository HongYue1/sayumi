package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
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

// TestOpenLibraryPathWithQuestionMark pins that a library path containing '?'
// still opens its database inside that library, with WAL on. '?' is a legal
// directory-name character on Linux/macOS and -library takes an arbitrary path,
// but the DSN is "<path>?_pragma=..." and the driver splits it at the FIRST
// '?'. An unescaped path therefore truncates the filename (the database lands
// outside the library, and any two libraries sharing a prefix collide on one
// file) and shifts the leading _pragma into a bogus query key, silently leaving
// journal_mode=delete under a read pool that is sized for WAL.
func TestOpenLibraryPathWithQuestionMark(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("'?' is not a legal path character on Windows")
	}

	// Canonicalize the temp root first: SQLite resolves symlinks when it opens
	// a database (xFullPathname), so pragma_database_list reports the resolved
	// path. On macOS t.TempDir() lives under /var -> /private/var, which made
	// the byte-for-byte comparison below fail even though the '?' escaping was
	// correct. With a symlink-free base, got == want iff the DSN escaping
	// preserved the full path.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	lib := filepath.Join(base, "Books?vol2")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}

	db, err := Open(lib)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	ctx := context.Background()

	var gotPath string
	if err := db.QueryRowContext(ctx,
		"SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&gotPath); err != nil {
		t.Fatalf("pragma_database_list: %v", err)
	}
	if want := filepath.Join(lib, ".sayumi", "sayumi.db"); gotPath != want {
		t.Errorf("database file = %q, want %q (library path not escaped into the DSN)", gotPath, want)
	}

	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("journal_mode = %q, want wal (leading _pragma lost to the unescaped '?')", journalMode)
	}
}

func TestEscapeDSNPathOnlyRewritesAmbiguousPaths(t *testing.T) {
	t.Parallel()
	// A path that cannot confuse the DSN split must come back byte-for-byte, so
	// every existing library keeps the DSN it has always had -- including
	// Windows drive and UNC paths, which cannot contain '?' and which SQLite's
	// URI parser would mangle ("//nas/..." reads as a URI authority).
	for _, unchanged := range []string{
		filepath.Join("lib", ".sayumi", "sayumi.db"),
		`C:\lib\.sayumi\sayumi.db`,
		`\\nas\books\.sayumi\sayumi.db`,
		"/lib/100% books/.sayumi/sayumi.db",
		"/lib/hash#tag/.sayumi/sayumi.db",
	} {
		if got := escapeDSNPath(unchanged); got != unchanged {
			t.Errorf("escapeDSNPath(%q) = %q, want it unchanged", unchanged, got)
		}
	}

	// With the URI form in play, every character SQLite's URI parser reads must
	// be encoded, or it decodes a different path than the caller asked for.
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "/lib/Books?vol2/.sayumi/sayumi.db", want: "file:/lib/Books%3Fvol2/.sayumi/sayumi.db"},
		{in: "/lib/100%?#x/sayumi.db", want: "file:/lib/100%25%3F%23x/sayumi.db"},
	} {
		if got := escapeDSNPath(tc.in); got != tc.want {
			t.Errorf("escapeDSNPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
