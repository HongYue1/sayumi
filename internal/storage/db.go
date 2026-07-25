package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	writeMu sync.Mutex

	// saveProgressStmt is a long-lived prepared statement for the hottest write
	// path: progress saves fire on roughly every page turn. database/sql lazily
	// (re)prepares it on each pooled connection and caches it there, so the
	// modernc SQL parser runs about once per connection instead of on every
	// save. Prepared in Open after migrate; released in Close.
	saveProgressStmt *sql.Stmt

	// foldPaths records whether the volume holding this library treats paths
	// case-insensitively. It is probed once in Open rather than derived from
	// runtime.GOOS, because case sensitivity is a property of the filesystem, not
	// the operating system: an exFAT/NTFS/SMB mount on Linux folds case, while a
	// case-sensitive APFS volume on macOS does not. Path identity keys are folded
	// only when this is true, so a genuinely case-sensitive volume can still hold
	// Book.epub and book.epub as two distinct books.
	foldPaths bool
}

// PathKey returns the identity key for a file path: the value stored in
// books.file_path_key / ignored_files.file_path_key, and the key the library
// scanner uses for its in-memory dedup maps. The exact path is stored and
// returned separately for I/O, display, and rewriting -- only the key is folded,
// so nothing here changes which bytes are opened on disk.
func (db *DB) PathKey(path string) string {
	return pathKey(path, db.foldPaths)
}

// pathKey is the pure form of PathKey. Separator style and redundant path
// elements are always normalized; case is folded only on a volume that ignores
// it. strings.ToLower is used rather than SQLite's NOCASE collation because
// NOCASE folds ASCII A-Z only, which would silently miss the Arabic, CJK, and
// accented filenames a book library is expected to hold.
func pathKey(path string, fold bool) string {
	key := filepath.ToSlash(filepath.Clean(path))
	if fold {
		key = strings.ToLower(key)
	}
	return key
}

// detectPathFolding reports whether dir's filesystem is case-insensitive, using
// the same autodetection git performs for core.ignorecase: create a file, then
// ask for it back under a different case. On any I/O failure it falls back to
// the per-OS default instead of assuming case sensitivity, because assuming
// sensitivity is the direction that loses data -- it is what lets a deleted
// book's tombstone be missed and the book resurrected on the next scan.
func detectPathFolding(dir string) bool {
	probe, err := os.CreateTemp(dir, ".sayumi-case-probe-*")
	if err != nil {
		return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	}
	name := probe.Name()
	_ = probe.Close()
	defer func() { _ = os.Remove(name) }()

	flipped := filepath.Join(filepath.Dir(name), strings.ToUpper(filepath.Base(name)))
	if flipped == name {
		return false
	}
	_, err = os.Lstat(flipped)
	return err == nil
}

func Open(libraryPath string) (*DB, error) {
	sayumiDir := filepath.Join(libraryPath, ".sayumi")
	if err := os.MkdirAll(sayumiDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sayumi dir: %w", err)
	}

	dbPath := filepath.Join(sayumiDir, "sayumi.db")

	sqlDB, err := sql.Open("sqlite", dataSourceName(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Read pool: WAL mode permits many concurrent readers alongside a single
	// writer, and every write method serializes through DB.writeMu, so pooled
	// connections only ever execute concurrent *reads* -- two writes can never
	// overlap and race for the file. The post-login page load fans out into
	// several independent read queries (books, progress, settings, flairs,
	// bookmarks), which a single connection forces to run strictly serially.
	//
	// The cap of 4 is the measured sweet spot (BenchmarkConcurrentReads):
	// 1->4 connections is ~3.1x read throughput, while 8 adds <10% more for
	// double the idle connections and mmap address space. Sequential
	// single-reader use is unchanged. busy_timeout(5000) stays as a safety net
	// for any external process touching the database file.
	const maxReadPoolConns = 4
	sqlDB.SetMaxOpenConns(maxReadPoolConns)
	sqlDB.SetMaxIdleConns(maxReadPoolConns)

	// SetConnMaxLifetime / SetConnMaxIdleTime are deliberately left unset. Those
	// knobs exist to recycle connections to a networked database server that can
	// drop or stale them mid-process; this pool only ever talks to a local,
	// embedded SQLite file that never disappears under us. Capping connection
	// lifetime would instead periodically discard the connections pre-warmed just
	// below and force the one-time _pragma DSN setup (WAL, 256 MB mmap, 32 MB
	// cache) to run again on the next interactive request -- a pure regression
	// for an always-local DB -- so they stay at their no-expiry default.

	// Probed against the library's own .sayumi directory, before migrate, because
	// the migration backfill needs to know how to key existing rows.
	db := &DB{DB: sqlDB, foldPaths: detectPathFolding(sayumiDir)}
	if err := db.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	// Prepare the hot-path progress upsert once. The modernc profile of
	// BenchmarkSaveProgress shows ~29% of the save path is SQL parse/compile;
	// a long-lived statement amortizes that to about once per pooled connection.
	saveProgressStmt, err := db.PrepareContext(context.Background(), saveProgressUpsert)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("prepare save-progress statement: %w", err)
	}
	db.saveProgressStmt = saveProgressStmt

	// Pre-warm the read pool. Each pooled connection runs the full _pragma DSN
	// (WAL, 256 MB mmap, 32 MB cache) on first use; left lazy, that one-time
	// per-connection setup lands as latency on whichever interactive request
	// first opens each connection -- observed as a slow first /api/settings,
	// /api/books and first progress write after login. Establishing the
	// connections now folds that cost into profile-open, which already runs in a
	// background goroutine on login, so the first real requests hit warm
	// connections. Best-effort: see warmPool.
	db.warmPool(maxReadPoolConns)

	return db, nil
}

// Close releases the long-lived prepared statements before closing the
// underlying connection pool. It shadows the *sql.DB.Close promoted through
// embedding so saveProgressStmt is torn down together with the pool.
func (db *DB) Close() error {
	var stmtErr error
	if db.saveProgressStmt != nil {
		stmtErr = db.saveProgressStmt.Close()
		db.saveProgressStmt = nil
	}
	var dbErr error
	if err := db.DB.Close(); err != nil {
		dbErr = fmt.Errorf("close database: %w", err)
	}
	if stmtErr != nil {
		stmtErr = fmt.Errorf("close save-progress statement: %w", stmtErr)
	}
	return errors.Join(dbErr, stmtErr)
}

// warmPool eagerly establishes up to n pooled connections so each connection's
// one-time _pragma DSN setup runs now rather than on the first interactive
// request that happens to open it. The connections are held simultaneously --
// released back to the pool only after all are open -- so the pool creates n
// distinct warm connections instead of reusing a single one. It is best-effort:
// a connection that fails to open is skipped and will surface its error on real
// use rather than failing startup.
func (db *DB) warmPool(n int) {
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, n)
	for range n {
		conn, err := db.Conn(ctx)
		if err != nil {
			break
		}
		if err := conn.PingContext(ctx); err != nil {
			_ = conn.Close()
			continue
		}
		conns = append(conns, conn)
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// escapeDSNPath renders a filesystem path for use as the filename part of a
// modernc.org/sqlite DSN, which has the form "<path>?_pragma=...".
//
// The driver splits that string at the FIRST '?' and parses everything after it
// as query parameters. '?' is a legal directory-name character on Linux/macOS
// and -library accepts an arbitrary path, so a library at "/books?vol2" opened
// its database at the truncated path "/books" -- outside the library, and
// shared by any two libraries with the same prefix -- while the leading
// "_pragma=journal_mode(WAL)" was absorbed into the bogus query key
// "vol2/.sayumi/sayumi.db?_pragma" and silently dropped, leaving
// journal_mode=delete under a read pool that is sized for WAL.
//
// A path with no '?' is returned unchanged, so the DSN for every ordinary
// library stays byte-for-byte what it has always been on every platform. That
// matters most for Windows drive and UNC paths: they cannot contain '?', and
// SQLite's URI parser would misread them ("//nas/share" parses as a URI
// authority, which SQLite rejects). Only an ambiguous path switches to the URI
// form, which this driver opens with SQLITE_OPEN_URI; '%', '?' and '#' are
// percent-encoded so SQLite decodes exactly the path we were handed.
func escapeDSNPath(dbPath string) string {
	if !strings.Contains(dbPath, "?") {
		return dbPath
	}
	esc := filepath.ToSlash(dbPath)
	esc = strings.ReplaceAll(esc, "%", "%25")
	esc = strings.ReplaceAll(esc, "?", "%3F")
	esc = strings.ReplaceAll(esc, "#", "%23")
	if strings.HasPrefix(esc, "//") {
		// Keep a leading "//" out of the URI authority position.
		esc = "/%2F" + esc[2:]
	}
	return "file:" + esc
}

// dataSourceName builds the modernc.org/sqlite DSN for a profile database.
//
// modernc.org/sqlite does NOT understand mattn/go-sqlite3 "_param=" DSN keys;
// it silently ignores unknown query parameters. The previous
// "_journal_mode=WAL&_foreign_keys=on&..." form therefore left every pragma at
// its default (journal_mode=delete, foreign_keys=OFF, busy_timeout=0). modernc
// instead executes any "_pragma=" directive on every connection it opens, which
// also keeps the per-connection pragmas (foreign_keys, busy_timeout) correct
// should the pool ever be widened past one conn.
func dataSourceName(dbPath string) string {
	return escapeDSNPath(dbPath) +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-32000)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		// 256 MB mmap window per profile DB. On a typical desktop this is
		// virtual address space only (no RSS until pages are read).
		// On 32-bit hosts or constrained environments this should be lowered.
		"&_pragma=mmap_size(268435456)"
}

func (db *DB) migrate() error {
	// Migrations run once at startup and must complete atomically, so they use
	// context.Background() rather than a cancelable request context.
	//
	// Order matters for upgrades:
	//  1) CREATE TABLE IF NOT EXISTS (idempotent base shape)
	//  2) additive column migrations (old DBs gain new columns)
	//  3) CREATE INDEX IF NOT EXISTS (may reference columns added in step 2)
	//  4) one-shot data reconciles
	// Running indexes before column migrations breaks upgrades when a new index
	// references a column that only exists via ADD COLUMN (e.g. cover_checked).
	if _, err := db.ExecContext(context.Background(), schemaTables); err != nil {
		return fmt.Errorf("execute schema tables: %w", err)
	}

	// Additive column migrations for databases created before the column existed.
	// CREATE TABLE IF NOT EXISTS above is a no-op on such DBs, so new columns
	// must be added explicitly. ADD COLUMN is idempotent here via the guard.
	// Read each table's existing columns once rather than re-querying
	// pragma_table_info for every column migration (12 of the 13 migrations
	// target the same `settings` table).
	tableCols := make(map[string]map[string]bool)
	for _, mig := range columnMigrations {
		cols, ok := tableCols[mig.table]
		if !ok {
			var err error
			cols, err = db.tableColumns(mig.table)
			if err != nil {
				return fmt.Errorf("inspect %s columns: %w", mig.table, err)
			}
			tableCols[mig.table] = cols
		}
		if cols[mig.column] {
			continue // already present
		}
		if err := db.addColumn(mig.table, mig.column, mig.definition); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", mig.table, mig.column, err)
		}
		cols[mig.column] = true
	}

	if _, err := db.ExecContext(context.Background(), schemaIndexes); err != nil {
		return fmt.Errorf("execute schema indexes: %w", err)
	}

	// Books that already have a cover are by definition cover-resolved. Mark them
	// so the first scan after cover_checked is added does not re-parse the entire
	// library just to rediscover covers it already has. Idempotent: once converged
	// this matches no rows.
	if _, err := db.ExecContext(context.Background(),
		"UPDATE books SET cover_checked = 1 WHERE has_cover = 1 AND cover_checked = 0"); err != nil {
		return fmt.Errorf("reconcile cover_checked: %w", err)
	}

	// Path identity keys cannot be derived in SQL: LOWER() folds ASCII only and
	// there is no SQL equivalent of filepath.Clean. Rows written before the column
	// existed -- and every row of a library that moved between a case-folding and a
	// case-sensitive volume -- are re-keyed here with the same Go function that
	// writes keys on insert. Idempotent: once converged it writes nothing.
	if err := db.backfillPathKeys(); err != nil {
		return err
	}
	return nil
}

// rekey pairs an exact stored path with the identity key it should carry.
type rekey struct {
	path string
	key  string
}

// backfillPathKeys brings books.file_path_key and ignored_files.file_path_key in
// line with the current folding decision. Running on every Open is what makes
// moving a library between volumes safe: the keys follow the new volume's rules
// instead of staying frozen at import time.
func (db *DB) backfillPathKeys() error {
	const (
		selectBooks   = "SELECT file_path, file_path_key FROM books"
		updateBooks   = "UPDATE books SET file_path_key = ? WHERE file_path = ?"
		selectIgnored = "SELECT file_path, file_path_key FROM ignored_files"
		updateIgnored = "UPDATE ignored_files SET file_path_key = ? WHERE file_path = ?"
	)
	if err := db.rekeyPaths(selectBooks, updateBooks); err != nil {
		return fmt.Errorf("backfill books path keys: %w", err)
	}
	if err := db.rekeyPaths(selectIgnored, updateIgnored); err != nil {
		return fmt.Errorf("backfill ignored_files path keys: %w", err)
	}
	return nil
}

func (db *DB) rekeyPaths(selectSQL, updateSQL string) error {
	pending, err := db.pendingRekeys(selectSQL)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	return db.applyRekeys(updateSQL, pending)
}

// pendingRekeys reads every stored path and returns only the rows whose key is
// stale, so a converged library performs no writes and takes no write lock.
func (db *DB) pendingRekeys(selectSQL string) ([]rekey, error) {
	rows, err := db.QueryContext(context.Background(), selectSQL)
	if err != nil {
		return nil, fmt.Errorf("read paths: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pending []rekey
	for rows.Next() {
		var path, key string
		if err := rows.Scan(&path, &key); err != nil {
			return nil, fmt.Errorf("scan path: %w", err)
		}
		if want := db.PathKey(path); want != key {
			pending = append(pending, rekey{path: path, key: want})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paths: %w", err)
	}
	return pending, nil
}

// applyRekeys writes the stale keys in one transaction. Per-row commits would
// cost an fsync per book on a large library, and a partially keyed table would
// leave path lookups silently missing rows until the next Open.
func (db *DB) applyRekeys(updateSQL string, pending []rekey) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, updateSQL)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range pending {
		if _, err := stmt.ExecContext(ctx, r.key, r.path); err != nil {
			return fmt.Errorf("key %s: %w", r.path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

type columnMigration struct {
	table      string
	column     string
	definition string
}

var columnMigrations = []columnMigration{
	{table: "settings", column: "font_roles", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "settings", column: "header_sizes_enabled", definition: "INTEGER"},
	{table: "settings", column: "h1_size", definition: "INTEGER"},
	{table: "settings", column: "h2_size", definition: "INTEGER"},
	{table: "settings", column: "h3_size", definition: "INTEGER"},
	{table: "settings", column: "h4_size", definition: "INTEGER"},
	{table: "settings", column: "h5_size", definition: "INTEGER"},
	{table: "settings", column: "h6_size", definition: "INTEGER"},
	{table: "settings", column: "header_weight", definition: "INTEGER"},
	{table: "settings", column: "text_weight", definition: "INTEGER"},
	{table: "settings", column: "letter_spacing", definition: "REAL"},
	{table: "settings", column: "heading_letter_spacing", definition: "REAL"},
	{table: "settings", column: "chapter_title_font_family", definition: "TEXT"},
	// cover_checked records whether the library scanner has already resolved this
	// book's cover (extracted one, or determined none is available). The post-walk
	// cover backfill only revisits rows where it is 0, so a cover-less or skipped
	// book is parsed at most once rather than re-parsed on every scan.
	{table: "books", column: "cover_checked", definition: "INTEGER NOT NULL DEFAULT 0"},

	// Case-folded, separator-normalized copy of file_path. Every path identity
	// lookup goes through it -- scan dedup, delete tombstones, upload un-ignore --
	// while file_path keeps the exact bytes needed to open, hard-link, rewrite, and
	// display the file. Existing rows are keyed by the backfill at the end of
	// migrate, so the default empty string is never observed by a lookup.
	{table: "books", column: "file_path_key", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "ignored_files", column: "file_path_key", definition: "TEXT NOT NULL DEFAULT ''"},
}

// tableColumns returns the set of existing column names for a table, read in a
// single pragma_table_info query so callers can check many columns without
// re-querying per column.
func (db *DB) tableColumns(table string) (map[string]bool, error) {
	rows, err := db.QueryContext(context.Background(), "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, fmt.Errorf("inspect columns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cols := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan column: %w", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

// addColumn runs ALTER TABLE ... ADD COLUMN. Callers must first confirm the
// column is absent (see tableColumns); ADD COLUMN is not idempotent.
func (db *DB) addColumn(table, column, definition string) error {
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add column: %w", err)
	}
	return nil
}

// schema is the full DDL used by benchmarks that open a raw sql.DB without
// going through migrate(). Keep it as tables+indexes so those helpers stay
// self-contained.
const schema = schemaTables + "\n" + schemaIndexes

const schemaTables = `
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
	-- Normalized identity form of file_path (see DB.PathKey). Every path lookup
	-- goes through it; file_path keeps the exact bytes used to open the file.
	file_path_key TEXT NOT NULL DEFAULT '',
	file_hash     TEXT NOT NULL DEFAULT '',
	file_size     INTEGER NOT NULL DEFAULT 0,
	cover_path    TEXT NOT NULL DEFAULT '',
	has_cover     INTEGER NOT NULL DEFAULT 0,
	cover_checked INTEGER NOT NULL DEFAULT 0,
	spine_json    TEXT NOT NULL DEFAULT '[]',
	toc_json      TEXT NOT NULL DEFAULT '[]',
	direction     TEXT NOT NULL DEFAULT 'ltr',
	chapter_count INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

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

CREATE TABLE IF NOT EXISTS settings (
	user_id               TEXT PRIMARY KEY DEFAULT 'default',
	font_size             INTEGER,
	font_family           TEXT,
	line_height           REAL,
	paragraph_spacing     REAL,
	text_indent           REAL,
	letter_spacing        REAL,
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
	chapter_title_font_family TEXT,
	heading_letter_spacing REAL,
	header_sizes_enabled  INTEGER,
	h1_size               INTEGER,
	h2_size               INTEGER,
	h3_size               INTEGER,
	h4_size               INTEGER,
	h5_size               INTEGER,
	h6_size               INTEGER,
	header_weight         INTEGER,
	text_weight           INTEGER,
	font_roles            TEXT NOT NULL DEFAULT '',
	updated_at            TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS ignored_files (
	file_path     TEXT PRIMARY KEY,
	file_path_key TEXT NOT NULL DEFAULT '',
	ignored_at    TEXT NOT NULL DEFAULT (datetime('now'))
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

-- Saved reader-settings presets. settings_json holds the full settings payload
-- (identical to the PUT /api/settings body) so applying a preset round-trips
-- through the same validator. Starts empty; users save their own.
CREATE TABLE IF NOT EXISTS presets (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL DEFAULT 'default',
	name          TEXT NOT NULL DEFAULT '',
	settings_json TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- User-defined custom themes: a small palette (bg/fg/accent) plus a name and
-- light/dark grouping. accent may be empty, which the client treats as "auto"
-- (derived from bg/fg). Built-in themes live on the client; only custom ones
-- are stored here. Starts empty; users save their own.
CREATE TABLE IF NOT EXISTS custom_themes (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL DEFAULT 'default',
	name        TEXT NOT NULL DEFAULT '',
	theme_group TEXT NOT NULL DEFAULT 'light',
	bg          TEXT NOT NULL DEFAULT '',
	fg          TEXT NOT NULL DEFAULT '',
	accent      TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`

const schemaIndexes = `
CREATE INDEX IF NOT EXISTS idx_books_file_hash ON books(file_hash);

-- Cover backfill runs after every completed scan and steady state should find no
-- rows. A partial covering index keeps that query proportional to unresolved
-- covers instead of scanning the whole library. Resolved rows (cover_checked = 1)
-- are excluded, so the common case does not pay ongoing index bloat.
CREATE INDEX IF NOT EXISTS idx_books_cover_unchecked ON books(id, file_path)
  WHERE cover_checked = 0;

-- Partial unique index prevents concurrent imports from inserting duplicate
-- rows for the same content. The WHERE clause excludes the empty-string default
-- so that books inserted before hashing completes don't conflict with each other.
CREATE UNIQUE INDEX IF NOT EXISTS idx_books_file_hash_uniq ON books(file_hash)
  WHERE file_hash != '';

-- Path identity lookups (scan dedup, delete tombstones, upload un-ignore) all go
-- through file_path_key. Deliberately not UNIQUE: a library imported before this
-- column existed can already hold two rows whose paths differ only by case, and a
-- unique index would fail the migration outright instead of letting the operator
-- resolve the duplicate.
CREATE INDEX IF NOT EXISTS idx_books_file_path_key ON books(file_path_key);
CREATE INDEX IF NOT EXISTS idx_ignored_files_path_key ON ignored_files(file_path_key);

-- The library list is the landing view and always sorts by title, so without a
-- matching index SQLite materializes every row into a temporary B-tree on every
-- load. Column order and collation must stay identical to the query's
-- "ORDER BY title COLLATE NOCASE ASC, id ASC" or the planner ignores this index
-- and silently falls back to that sort.
CREATE INDEX IF NOT EXISTS idx_books_title_sort ON books(title COLLATE NOCASE, id);

CREATE INDEX IF NOT EXISTS idx_bookmarks_book ON bookmarks(book_id, user_id);
`
