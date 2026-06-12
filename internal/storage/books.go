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

func (db *DB) ListBooksContext(ctx context.Context) (out []BookRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       spine_json, toc_json, direction, chapter_count,
		       created_at, updated_at
		FROM books
		ORDER BY title COLLATE NOCASE ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		book, scanErr := scanBook(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan book: %w", scanErr)
		}
		out = append(out, book)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate books: %w", err)
	}
	return out, nil
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
		return book, fmt.Errorf("get book by hash %s: %w", hash, err)
	}
	return book, nil
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
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	now := time.Now().UTC().Format(time.DateTime)
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
		return "", fmt.Errorf("insert book: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("insert book rows affected: %w", err)
	}
	if rows > 0 {
		// Our row was inserted; the proposed ID is canonical.
		return book.ID, nil
	}

	// Another goroutine inserted a row with the same file_hash first (OR IGNORE
	// suppressed our insert). Return whichever ID is actually in the DB.
	var existing BookRecord
	row := db.QueryRowContext(ctx, `
		SELECT id, title, author, language, publisher, description, pub_date, isbn,
		       file_path, file_hash, file_size, cover_path, has_cover,
		       spine_json, toc_json, direction, chapter_count,
		       created_at, updated_at
		FROM books WHERE file_hash = ?
	`, book.FileHash)
	existing, err = scanBook(row)
	if err != nil {
		return "", fmt.Errorf("fetch existing book after ignored insert: %w", err)
	}
	return existing.ID, nil
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
