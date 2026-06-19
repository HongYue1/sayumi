package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// FlairRecord is a user-defined (custom) flair. Built-in flairs are defined on
// the client and never stored here.
type FlairRecord struct {
	ID        string
	UserID    string
	Label     string
	Color     string
	CreatedAt string
}

func GenerateFlairID() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return "flair_" + hex.EncodeToString(b)
}

func (db *DB) ListFlairsContext(ctx context.Context, userID string) (out []FlairRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, label, color, created_at
		FROM flairs
		WHERE user_id = ?
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list flairs: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var f FlairRecord
		if err := rows.Scan(&f.ID, &f.UserID, &f.Label, &f.Color, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan flair: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flairs: %w", err)
	}
	return out, nil
}

func (db *DB) InsertFlairContext(ctx context.Context, f FlairRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	createdAt := f.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.DateTime)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO flairs (id, user_id, label, color, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, f.ID, f.UserID, f.Label, f.Color, createdAt)
	if err != nil {
		return fmt.Errorf("insert flair: %w", err)
	}
	return nil
}

// DeleteFlairContext removes a custom flair and clears any book assignments
// that referenced it, so no book is left pointing at a non-existent flair.
func (db *DB) DeleteFlairContext(ctx context.Context, id, userID string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete flair tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, "DELETE FROM flairs WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return fmt.Errorf("delete flair: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete flair rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM book_flairs WHERE flair_id = ? AND user_id = ?", id, userID); err != nil {
		return fmt.Errorf("clear book flairs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete flair: %w", err)
	}
	return nil
}

// GetAllBookFlairsContext returns a map of book id -> assigned flair id for a
// profile, used to decorate the library listing in one query.
func (db *DB) GetAllBookFlairsContext(ctx context.Context, userID string) (out map[string]string, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT book_id, flair_id FROM book_flairs WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list book flairs: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	out = make(map[string]string)
	for rows.Next() {
		var bookID, flairID string
		if err := rows.Scan(&bookID, &flairID); err != nil {
			return nil, fmt.Errorf("scan book flair: %w", err)
		}
		out[bookID] = flairID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate book flairs: %w", err)
	}
	return out, nil
}

// FlairExistsContext reports whether a flair with the given id exists for the
// user. There is no foreign key from book_flairs.flair_id to flairs.id, so the
// caller must check this before assigning to avoid a dangling reference.
func (db *DB) FlairExistsContext(ctx context.Context, id, userID string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM flairs WHERE id = ? AND user_id = ?)
	`, id, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("flair exists: %w", err)
	}
	return exists, nil
}

// SetBookFlairContext assigns a flair to a book, or clears it when flairID is
// empty. The book must exist (enforced by the foreign key).
func (db *DB) SetBookFlairContext(ctx context.Context, bookID, userID, flairID string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	if flairID == "" {
		_, err := db.ExecContext(ctx, "DELETE FROM book_flairs WHERE book_id = ? AND user_id = ?", bookID, userID)
		if err != nil {
			return fmt.Errorf("clear book flair: %w", err)
		}
		return nil
	}

	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx, `
		INSERT INTO book_flairs (book_id, user_id, flair_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(book_id, user_id) DO UPDATE SET
			flair_id = excluded.flair_id,
			updated_at = excluded.updated_at
	`, bookID, userID, flairID, now)
	if err != nil {
		return fmt.Errorf("set book flair: %w", err)
	}
	return nil
}

// SetBookFlairCheckedContext validates the target flair and assigns it to a book
// atomically under the write lock. Holding writeMu across the existence check
// and the write closes the TOCTOU window against DeleteFlairContext (which also
// takes writeMu): a concurrent flair delete can no longer slip between the
// check and the assignment and leave book_flairs pointing at a deleted flair
// (there is no FK from book_flairs.flair_id to enforce it). An empty flairID
// clears the assignment. Flair ids present in allowedBuiltins are accepted
// without a DB lookup (built-in flairs live on the client, not in the flairs
// table); any other id must exist for the user or ErrNotFound is returned.
func (db *DB) SetBookFlairCheckedContext(ctx context.Context, bookID, userID, flairID string, allowedBuiltins map[string]struct{}) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	if flairID == "" {
		_, err := db.ExecContext(ctx, "DELETE FROM book_flairs WHERE book_id = ? AND user_id = ?", bookID, userID)
		if err != nil {
			return fmt.Errorf("clear book flair: %w", err)
		}
		return nil
	}

	if _, builtin := allowedBuiltins[flairID]; !builtin {
		var exists bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM flairs WHERE id = ? AND user_id = ?)
		`, flairID, userID).Scan(&exists); err != nil {
			return fmt.Errorf("flair exists: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}

	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx, `
		INSERT INTO book_flairs (book_id, user_id, flair_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(book_id, user_id) DO UPDATE SET
			flair_id = excluded.flair_id,
			updated_at = excluded.updated_at
	`, bookID, userID, flairID, now)
	if err != nil {
		return fmt.Errorf("set book flair: %w", err)
	}
	return nil
}
