package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// CustomThemeRecord is a user-defined theme: a small palette (bg/fg/accent)
// plus a display name and light/dark grouping. Accent may be empty, which the
// client treats as "auto" and derives from bg/fg. Built-in themes live on the
// client and are never stored here.
type CustomThemeRecord struct {
	ID        string
	UserID    string
	Name      string
	Group     string
	Bg        string
	Fg        string
	Accent    string
	CreatedAt string
	UpdatedAt string
}

// GenerateCustomThemeID returns a random, collision-resistant custom-theme id.
func GenerateCustomThemeID() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return "theme_" + hex.EncodeToString(b)
}

func (db *DB) ListCustomThemesContext(ctx context.Context, userID string) (out []CustomThemeRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, name, theme_group, bg, fg, accent, created_at, updated_at
		FROM custom_themes
		WHERE user_id = ?
		ORDER BY created_at ASC, id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list custom themes: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var t CustomThemeRecord
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Group, &t.Bg, &t.Fg, &t.Accent, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan custom theme: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate custom themes: %w", err)
	}
	return out, nil
}

func (db *DB) InsertCustomThemeContext(ctx context.Context, t CustomThemeRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	now := time.Now().UTC().Format(time.DateTime)
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	if t.UpdatedAt == "" {
		t.UpdatedAt = now
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO custom_themes (id, user_id, name, theme_group, bg, fg, accent, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.UserID, t.Name, t.Group, t.Bg, t.Fg, t.Accent, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert custom theme: %w", err)
	}
	return nil
}

// UpdateCustomThemeContext updates an existing theme's editable fields and
// returns the stored record (with its original created_at). It returns
// ErrNotFound when no row matches so the handler can surface a 404.
func (db *DB) UpdateCustomThemeContext(ctx context.Context, t CustomThemeRecord) (CustomThemeRecord, error) {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	if t.UpdatedAt == "" {
		t.UpdatedAt = time.Now().UTC().Format(time.DateTime)
	}
	res, err := db.ExecContext(ctx, `
		UPDATE custom_themes
		SET name = ?, theme_group = ?, bg = ?, fg = ?, accent = ?, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, t.Name, t.Group, t.Bg, t.Fg, t.Accent, t.UpdatedAt, t.ID, t.UserID)
	if err != nil {
		return CustomThemeRecord{}, fmt.Errorf("update custom theme: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return CustomThemeRecord{}, fmt.Errorf("update custom theme rows affected: %w", err)
	}
	if n == 0 {
		return CustomThemeRecord{}, ErrNotFound
	}
	row := db.QueryRowContext(ctx, "SELECT created_at FROM custom_themes WHERE id = ? AND user_id = ?", t.ID, t.UserID)
	if err := row.Scan(&t.CreatedAt); err != nil {
		return CustomThemeRecord{}, fmt.Errorf("reload custom theme: %w", err)
	}
	return t, nil
}

// DeleteCustomThemeContext removes a custom theme. It returns ErrNotFound when
// no row matches so the handler can surface a 404.
func (db *DB) DeleteCustomThemeContext(ctx context.Context, id, userID string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	res, err := db.ExecContext(ctx, "DELETE FROM custom_themes WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return fmt.Errorf("delete custom theme: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete custom theme rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
