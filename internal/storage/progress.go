package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type ProgressRecord struct {
	BookID    string
	UserID    string
	Chapter   int
	Percent   float64
	CFI       sql.NullString
	UpdatedAt string
}

func (db *DB) GetProgressContext(ctx context.Context, bookID, userID string) (ProgressRecord, error) {
	row := db.QueryRowContext(ctx, `
		SELECT book_id, user_id, chapter, percent, cfi, updated_at
		FROM progress
		WHERE book_id = ? AND user_id = ?
	`, bookID, userID)

	var progress ProgressRecord
	err := row.Scan(&progress.BookID, &progress.UserID, &progress.Chapter, &progress.Percent, &progress.CFI, &progress.UpdatedAt)
	if err != nil {
		return progress, fmt.Errorf("get progress: %w", err)
	}
	return progress, nil
}

func (db *DB) GetAllProgressContext(ctx context.Context, userID string) (result map[string]ProgressRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT book_id, user_id, chapter, percent, cfi, updated_at
		FROM progress
		WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("get all progress: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	result = make(map[string]ProgressRecord)
	for rows.Next() {
		var progress ProgressRecord
		if err := rows.Scan(&progress.BookID, &progress.UserID, &progress.Chapter, &progress.Percent, &progress.CFI, &progress.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan progress: %w", err)
		}
		result[progress.BookID] = progress
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate progress: %w", err)
	}
	return result, nil
}

func (db *DB) SaveProgressContext(ctx context.Context, progress ProgressRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx, `
		INSERT INTO progress (book_id, user_id, chapter, percent, cfi, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(book_id, user_id)
		DO UPDATE SET chapter = excluded.chapter, percent = excluded.percent,
		             cfi = excluded.cfi, updated_at = excluded.updated_at
	`, progress.BookID, progress.UserID, progress.Chapter, progress.Percent, progress.CFI, now)
	if err != nil {
		return fmt.Errorf("save progress: %w", err)
	}
	return nil
}
