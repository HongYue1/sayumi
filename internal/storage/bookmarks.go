package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type BookmarkRecord struct {
	ID        string
	BookID    string
	UserID    string
	Chapter   int
	Percent   float64
	CFI       sql.NullString
	Label     string
	Comment   string
	CreatedAt string
}

func (db *DB) ListBookmarksContext(ctx context.Context, bookID, userID string) (out []BookmarkRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, book_id, user_id, chapter, percent, cfi, label, comment, created_at
		FROM bookmarks
		WHERE book_id = ? AND user_id = ?
		ORDER BY chapter ASC, percent ASC
	`, bookID, userID)
	if err != nil {
		return nil, fmt.Errorf("list bookmarks: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var bookmark BookmarkRecord
		if err := rows.Scan(&bookmark.ID, &bookmark.BookID, &bookmark.UserID, &bookmark.Chapter, &bookmark.Percent,
			&bookmark.CFI, &bookmark.Label, &bookmark.Comment, &bookmark.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		out = append(out, bookmark)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bookmarks: %w", err)
	}
	return out, nil
}

func (db *DB) GetBookmarkContext(ctx context.Context, id, userID string) (BookmarkRecord, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, book_id, user_id, chapter, percent, cfi, label, comment, created_at
		FROM bookmarks
		WHERE id = ? AND user_id = ?
	`, id, userID)
	var bookmark BookmarkRecord
	err := row.Scan(&bookmark.ID, &bookmark.BookID, &bookmark.UserID, &bookmark.Chapter, &bookmark.Percent,
		&bookmark.CFI, &bookmark.Label, &bookmark.Comment, &bookmark.CreatedAt)
	if err != nil {
		return bookmark, fmt.Errorf("get bookmark %s: %w", id, err)
	}
	return bookmark, nil
}

func (db *DB) InsertBookmarkContext(ctx context.Context, bookmark BookmarkRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	createdAt := bookmark.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.DateTime)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO bookmarks (id, book_id, user_id, chapter, percent, cfi, label, comment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, bookmark.ID, bookmark.BookID, bookmark.UserID, bookmark.Chapter, bookmark.Percent, bookmark.CFI, bookmark.Label, bookmark.Comment, createdAt)
	if err != nil {
		return fmt.Errorf("insert bookmark: %w", err)
	}
	return nil
}

func (db *DB) UpdateBookmarkContext(ctx context.Context, id, userID, label, comment string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	res, err := db.ExecContext(ctx, `
		UPDATE bookmarks SET label = ?, comment = ? WHERE id = ? AND user_id = ?
	`, label, comment, id, userID)
	if err != nil {
		return fmt.Errorf("update bookmark: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update bookmark rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) DeleteBookmarkContext(ctx context.Context, id, userID string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	res, err := db.ExecContext(ctx, "DELETE FROM bookmarks WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return fmt.Errorf("delete bookmark: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete bookmark rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func GenerateBookmarkID() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
