package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	writeMu sync.Mutex
}

func Open(libraryPath string) (*DB, error) {
	sayumiDir := filepath.Join(libraryPath, ".sayumi")
	if err := os.MkdirAll(sayumiDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sayumi dir: %w", err)
	}

	dbPath := filepath.Join(sayumiDir, "sayumi.db")
	dsn := dbPath +
		"?_journal_mode=WAL" +
		"&_synchronous=NORMAL" +
		"&_cache_size=-32000" +
		"&_busy_timeout=5000" +
		"&_foreign_keys=on" +
		// 256 MB mmap window per profile DB. On a typical desktop this is
		// virtual address space only (no RSS until pages are read).
		// On 32-bit hosts or constrained environments this should be lowered.
		"&_mmap_size=268435456"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	db := &DB{DB: sqlDB}
	if err := db.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return db, nil
}

func (db *DB) migrate() error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("execute schema: %w", err)
	}
	// Additive column migrations for databases created before the column existed.
	// CREATE TABLE IF NOT EXISTS above is a no-op on such DBs, so new columns
	// must be added explicitly. ADD COLUMN is idempotent here via the guard.
	for _, mig := range columnMigrations {
		if err := db.addColumnIfMissing(mig.table, mig.column, mig.definition); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", mig.table, mig.column, err)
		}
	}
	return nil
}

type columnMigration struct {
	table      string
	column     string
	definition string
}

var columnMigrations = []columnMigration{
	{table: "settings", column: "font_roles", definition: "TEXT NOT NULL DEFAULT ''"},
}

// addColumnIfMissing runs ALTER TABLE ... ADD COLUMN only when the column is
// absent, so the migration is safe to run on every startup.
func (db *DB) addColumnIfMissing(table, column, definition string) error {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return fmt.Errorf("inspect columns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		if name == column {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("add column: %w", err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS books (
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

CREATE INDEX IF NOT EXISTS idx_books_file_hash ON books(file_hash);

-- Partial unique index prevents concurrent imports from inserting duplicate
-- rows for the same content. The WHERE clause excludes the empty-string default
-- so that books inserted before hashing completes don't conflict with each other.
CREATE UNIQUE INDEX IF NOT EXISTS idx_books_file_hash_uniq ON books(file_hash)
  WHERE file_hash != '';

CREATE TABLE IF NOT EXISTS progress (
	book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL DEFAULT 'default',
	chapter    INTEGER NOT NULL DEFAULT 0,
	percent    REAL NOT NULL DEFAULT 0.0,
	cfi        TEXT,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (book_id, user_id)
);

CREATE TABLE IF NOT EXISTS bookmarks (
	id         TEXT PRIMARY KEY,
	book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL DEFAULT 'default',
	chapter    INTEGER NOT NULL DEFAULT 0,
	percent    REAL NOT NULL DEFAULT 0.0,
	cfi        TEXT,
	label      TEXT NOT NULL DEFAULT '',
	comment    TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_book ON bookmarks(book_id, user_id);

CREATE TABLE IF NOT EXISTS settings (
	user_id               TEXT PRIMARY KEY DEFAULT 'default',
	font_size             INTEGER,
	font_family           TEXT,
	line_height           REAL,
	paragraph_spacing     REAL,
	text_indent           REAL,
	content_width         INTEGER,
	display_mode          TEXT,
	margin_top            INTEGER,
	margin_bottom         INTEGER,
	margin_side           INTEGER,
	preserve_styles       INTEGER,
	preserve_fonts        INTEGER,
	justify               INTEGER,
	hyphenation           INTEGER,
	theme                 TEXT,
	chapter_title_align   TEXT,
	chapter_title_size    INTEGER,
	chapter_title_spacing REAL,
	font_roles            TEXT NOT NULL DEFAULT '',
	updated_at            TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS ignored_files (
	file_path  TEXT PRIMARY KEY,
	ignored_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- User-defined (custom) flairs. Built-in flairs live on the client and are not
-- stored here; book_flairs.flair_id may reference either a built-in id or a
-- custom flair id.
CREATE TABLE IF NOT EXISTS flairs (
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL DEFAULT 'default',
	label      TEXT NOT NULL DEFAULT '',
	color      TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- One flair per book (per profile). Rows are removed with their book via
-- ON DELETE CASCADE; assignments to a deleted custom flair are cleared in code.
CREATE TABLE IF NOT EXISTS book_flairs (
	book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL DEFAULT 'default',
	flair_id   TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (book_id, user_id)
);
`
