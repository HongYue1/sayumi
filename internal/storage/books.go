package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type BookSummary struct {
	ID           string
	Title        string
	Author       string
	Language     string
	Publisher    string
	Description  string
	PubDate      string
	ISBN         string
	FilePath     string
	FileHash     string
	FileSize     int64
	CoverPath    string
	HasCover     bool
	Direction    string
	ChapterCount int
	CreatedAt    string
	UpdatedAt    string
}

type BookRecord struct {
	BookSummary
	SpineJSON string
	TocJSON   string
}

// ListBookSummariesContext returns every book's summary metadata, deliberately
// omitting the heavy spine_json / toc_json columns. NewBookCache uses this to
// build the library list: a spine/toc is only needed when a book is actually
// opened for reading, yet reading those large TEXT columns for every row (they
// overflow onto separate SQLite pages) dominated profile-open time. Fetch them
// on demand with GetBookContentContext.
func (db *DB) ListBookSummariesContext(ctx context.Context) (out []BookSummary, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       direction, chapter_count,
		       created_at, updated_at
		FROM books
		ORDER BY title COLLATE NOCASE ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list book summaries: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		summary, scanErr := scanBookSummary(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan book summary: %w", scanErr)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate book summaries: %w", err)
	}
	return out, nil
}

// BookPath is the minimal (id, file_path) pair used by the library scanner's
// dedup snapshot, avoiding the cost of loading full BookSummary rows when only
// the path index is needed.
type BookPath struct {
	ID       string
	FilePath string
}

// ListBookPathsContext returns the id and file_path of every book. It backs the
// library scanner's path->id dedup map; unlike ListBookSummariesContext it
// reads only two columns and skips the title collation sort, neither of which
// the dedup map needs.
func (db *DB) ListBookPathsContext(ctx context.Context) (out []BookPath, err error) {
	rows, err := db.QueryContext(ctx, "SELECT id, file_path FROM books")
	if err != nil {
		return nil, fmt.Errorf("list book paths: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var bp BookPath
		if scanErr := rows.Scan(&bp.ID, &bp.FilePath); scanErr != nil {
			return nil, fmt.Errorf("scan book path: %w", scanErr)
		}
		out = append(out, bp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate book paths: %w", err)
	}
	return out, nil
}

// GetBookContentContext loads only the spine_json and toc_json for a book. The
// book cache no longer holds these (see ListBookSummariesContext); they are
// fetched on demand when a book is opened (chapter render, search, book detail).
func (db *DB) GetBookContentContext(ctx context.Context, id string) (spineJSON, tocJSON string, err error) {
	row := db.QueryRowContext(ctx, "SELECT spine_json, toc_json FROM books WHERE id = ?", id)
	if err := row.Scan(&spineJSON, &tocJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("get book content %s: %w", id, err)
	}
	return spineJSON, tocJSON, nil
}

// GetBookSummaryContext loads a single book's summary metadata by id, omitting
// the heavy spine_json / toc_json columns (see ListBookSummariesContext). Use it
// when only summary fields are needed -- e.g. warming the book cache after an
// import -- so the large overflow-page TEXT columns are not read. found is false
// when no row matches.
func (db *DB) GetBookSummaryContext(ctx context.Context, id string) (summary BookSummary, found bool, err error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       direction, chapter_count,
		       created_at, updated_at
		FROM books WHERE id = ?
	`, id)
	summary, err = scanBookSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BookSummary{}, false, nil
	}
	if err != nil {
		return BookSummary{}, false, fmt.Errorf("get book summary %s: %w", id, err)
	}
	return summary, true, nil
}

func (db *DB) GetBookContext(ctx context.Context, id string) (BookRecord, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       spine_json, toc_json, direction, chapter_count,
		       created_at, updated_at
		FROM books WHERE id = ?
	`, id)
	book, err := scanBook(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return book, ErrNotFound
		}
		return book, fmt.Errorf("get book %s: %w", id, err)
	}
	return book, nil
}

func (db *DB) GetBookByHashContext(ctx context.Context, hash string) (BookRecord, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       spine_json, toc_json, direction, chapter_count,
		       created_at, updated_at
		FROM books WHERE file_hash = ?
	`, hash)
	book, err := scanBook(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return book, ErrNotFound
		}
		return book, fmt.Errorf("get book by hash %s: %w", hash, err)
	}
	return book, nil
}

// GetBookIDByHashContext returns only the id and file_path for the book matching
// hash, without reading the heavy spine_json / toc_json columns. The library
// scan and duplicate-check paths only need to reconcile a path or resolve an ID,
// so this avoids pulling the large overflow-page TEXT blobs for every
// already-known book on every scan. found is false when no row matches.
func (db *DB) GetBookIDByHashContext(ctx context.Context, hash string) (id, filePath string, found bool, err error) {
	err = db.QueryRowContext(ctx, "SELECT id, file_path FROM books WHERE file_hash = ?", hash).Scan(&id, &filePath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("get book id by hash %s: %w", hash, err)
	}
	return id, filePath, true, nil
}

func (db *DB) BookExistsByPathContext(ctx context.Context, filePath string) (id string, found bool, err error) {
	err = db.QueryRowContext(ctx, "SELECT id FROM books WHERE file_path = ?", filePath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("check book by path: %w", err)
	}
	return id, true, nil
}

// InsertBookContext inserts book into the DB. If a row with the same file_hash
// already exists (e.g. two concurrent imports of the same content), the insert
// is silently ignored and the existing row's ID is returned instead. This makes
// concurrent imports idempotent even without an application-level lock.
func (db *DB) InsertBookContext(ctx context.Context, book BookRecord) (canonicalID string, err error) {
	now := time.Now().UTC().Format(time.DateTime)

	inserted, err := db.insertBookOrIgnore(ctx, book, now)
	if err != nil {
		return "", err
	}
	if inserted {
		// Our row was inserted; the proposed ID is canonical.
		return book.ID, nil
	}

	// Another goroutine inserted a row with the same file_hash first (OR IGNORE
	// suppressed our insert). Resolve the canonical ID with a plain indexed read;
	// only the id is needed, so skip the heavy spine_json / toc_json columns. The
	// write lock was released by insertBookOrIgnore, so this lookup does not
	// extend the writer critical section.
	existingID, _, found, err := db.GetBookIDByHashContext(ctx, book.FileHash)
	if err != nil {
		return "", fmt.Errorf("fetch existing book after ignored insert: %w", err)
	}
	if !found {
		return "", fmt.Errorf("insert ignored but no existing book for hash %q", book.FileHash)
	}
	return existingID, nil
}

// insertBookOrIgnore runs the INSERT OR IGNORE under the write lock and reports
// whether a row was actually inserted. inserted is false when a row with the
// same file_hash already existed (OR IGNORE suppressed the insert). The lock is
// held only for the write itself, never across the caller's follow-up hash
// lookup.
func (db *DB) insertBookOrIgnore(ctx context.Context, book BookRecord, now string) (inserted bool, err error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	res, err := db.ExecContext(
		ctx, `
		INSERT OR IGNORE INTO books (id, title, author, language, publisher, description, pub_date, isbn,
		                   file_path, file_hash, file_size, cover_path, has_cover,
		                   spine_json, toc_json, direction, chapter_count,
		                   created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		book.ID, book.Title, book.Author, book.Language, book.Publisher,
		book.Description, book.PubDate, book.ISBN, book.FilePath, book.FileHash,
		book.FileSize, book.CoverPath, book.HasCover, book.SpineJSON, book.TocJSON,
		book.Direction, book.ChapterCount, now, now,
	)
	if err != nil {
		return false, fmt.Errorf("insert book: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert book rows affected: %w", err)
	}
	return rows > 0, nil
}

// UpdateBookFilePathContext updates the on-disk path stored for a book. It is
// called when a book is found by content hash at a new path (e.g. after a
// profile clone or a file rename) so that future reads use the correct location.
func (db *DB) UpdateBookFilePathContext(ctx context.Context, id, filePath string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	_, err := db.ExecContext(
		ctx,
		"UPDATE books SET file_path = ?, updated_at = datetime('now') WHERE id = ?",
		filePath, id,
	)
	if err != nil {
		return fmt.Errorf("update file path for %s: %w", id, err)
	}
	return nil
}

func (db *DB) UpdateBookCoverContext(ctx context.Context, id, coverPath string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	_, err := db.ExecContext(ctx, `
		UPDATE books SET cover_path = ?, has_cover = 1, updated_at = datetime('now')
		WHERE id = ?
	`, coverPath, id)
	if err != nil {
		return fmt.Errorf("update cover for %s: %w", id, err)
	}
	return nil
}

func (db *DB) DeleteBookContext(ctx context.Context, id string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var filePath string
	if err := tx.QueryRowContext(ctx, "SELECT file_path FROM books WHERE id = ?", id).Scan(&filePath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("find book %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM books WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete book %s: %w", id, err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT OR IGNORE INTO ignored_files (file_path) VALUES (?)",
		filePath,
	); err != nil {
		return fmt.Errorf("ignore file %s: %w", filePath, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete book %s: %w", id, err)
	}
	committed = true
	return nil
}

func (db *DB) IsFileIgnoredContext(ctx context.Context, filePath string) (bool, error) {
	var dummy int
	err := db.QueryRowContext(
		ctx,
		"SELECT 1 FROM ignored_files WHERE file_path = ? LIMIT 1",
		filePath,
	).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check ignored: %w", err)
	}
	return true, nil
}

func (db *DB) RemoveIgnoredFileContext(ctx context.Context, filePath string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	_, err := db.ExecContext(ctx, "DELETE FROM ignored_files WHERE file_path = ?", filePath)
	if err != nil {
		return fmt.Errorf("remove ignored file: %w", err)
	}
	return nil
}

// ListIgnoredPathsContext returns every path currently in the ignored_files
// table. The library scan loads this once up front so it can skip ignored
// files with an in-memory lookup instead of one IsFileIgnoredContext query per
// file walked.
func (db *DB) ListIgnoredPathsContext(ctx context.Context) (out []string, err error) {
	rows, err := db.QueryContext(ctx, "SELECT file_path FROM ignored_files")
	if err != nil {
		return nil, fmt.Errorf("list ignored paths: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var path string
		if scanErr := rows.Scan(&path); scanErr != nil {
			return nil, fmt.Errorf("scan ignored path: %w", scanErr)
		}
		out = append(out, path)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ignored paths: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanBook(s scanner) (BookRecord, error) {
	var book BookRecord
	err := s.Scan(
		&book.ID, &book.Title, &book.Author, &book.Language, &book.Publisher,
		&book.Description, &book.PubDate, &book.ISBN, &book.FilePath, &book.FileHash,
		&book.FileSize, &book.CoverPath, &book.HasCover, &book.SpineJSON, &book.TocJSON,
		&book.Direction, &book.ChapterCount, &book.CreatedAt, &book.UpdatedAt,
	)
	return book, err
}

// scanBookSummary scans the summary columns selected by ListBookSummariesContext
// (the same column order as scanBook, minus spine_json / toc_json).
func scanBookSummary(s scanner) (BookSummary, error) {
	var b BookSummary
	err := s.Scan(
		&b.ID, &b.Title, &b.Author, &b.Language, &b.Publisher,
		&b.Description, &b.PubDate, &b.ISBN, &b.FilePath, &b.FileHash,
		&b.FileSize, &b.CoverPath, &b.HasCover,
		&b.Direction, &b.ChapterCount, &b.CreatedAt, &b.UpdatedAt,
	)
	return b, err
}
