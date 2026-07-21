package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// PresetRecord is a saved snapshot of a user's reader settings. SettingsJSON is
// the exact settings payload (identical to the PUT /api/settings body)
// serialized as JSON, so applying a preset round-trips through the settings
// validator.
type PresetRecord struct {
	ID           string
	UserID       string
	Name         string
	SettingsJSON string
	CreatedAt    string
	UpdatedAt    string
}

// GeneratePresetID returns a random, collision-resistant preset id.
func GeneratePresetID() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return "preset_" + hex.EncodeToString(b)
}

func (db *DB) ListPresetsContext(ctx context.Context, userID string) (out []PresetRecord, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, user_id, name, settings_json, created_at, updated_at
		FROM presets
		WHERE user_id = ?
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()

	for rows.Next() {
		var p PresetRecord
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.SettingsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan preset: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate presets: %w", err)
	}
	return out, nil
}

func (db *DB) InsertPresetContext(ctx context.Context, p PresetRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	now := time.Now().UTC().Format(time.DateTime)
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = now
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO presets (id, user_id, name, settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, p.ID, p.UserID, p.Name, p.SettingsJSON, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert preset: %w", err)
	}
	return nil
}

// DeletePresetContext removes a saved preset. It returns ErrNotFound when no
// row matches so the handler can surface a 404.
func (db *DB) DeletePresetContext(ctx context.Context, id, userID string) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	res, err := db.ExecContext(ctx, "DELETE FROM presets WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return fmt.Errorf("delete preset: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete preset rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
