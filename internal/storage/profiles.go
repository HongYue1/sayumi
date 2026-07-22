package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type ProfileRecord struct {
	Name      string
	PinHash   string // bcrypt hash of the profile PIN; empty string = PIN-less (open) profile
	CreatedAt string
}

// ProfilesDB stores profile credentials in a dedicated SQLite database that
// is shared across all per-profile library databases.
type ProfilesDB struct {
	db *sql.DB
}

func OpenProfilesDB(libraryRoot string) (*ProfilesDB, error) {
	dir := filepath.Join(libraryRoot, ".sayumi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create .sayumi dir: %w", err)
	}
	// See storage.Open for why "_pragma=" is required here: modernc silently
	// ignores the mattn-style "_param=" DSN keys, so the old form left WAL and
	// foreign-key enforcement off on this database too.
	dsn := filepath.Join(dir, "profiles.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open profiles db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	profiles := &ProfilesDB{db: db}
	if err := profiles.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate profiles db: %w", err)
	}
	return profiles, nil
}

func (p *ProfilesDB) migrate() error {
	// Runs once at startup; use a background context (see storage.migrate).
	_, err := p.db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS profiles (
			name       TEXT PRIMARY KEY,
			pin_hash   TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS sessions (
			token      TEXT PRIMARY KEY,
			profile    TEXT NOT NULL,
			expiry     TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS sessions_profile_idx ON sessions(profile);
	`)
	if err != nil {
		return fmt.Errorf("execute profiles schema: %w", err)
	}
	return nil
}

func (p *ProfilesDB) Close() error { return p.db.Close() }

func (p *ProfilesDB) ListProfilesContext(ctx context.Context) (out []ProfileRecord, err error) {
	rows, err := p.db.QueryContext(
		ctx,
		// name is the PK and breaks created_at ties (second-resolution datetime('now')).
		"SELECT name, pin_hash, created_at FROM profiles ORDER BY created_at ASC, name ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()
	for rows.Next() {
		var profile ProfileRecord
		if err := rows.Scan(&profile.Name, &profile.PinHash, &profile.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		out = append(out, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profiles: %w", err)
	}
	return out, nil
}

func (p *ProfilesDB) GetProfileContext(ctx context.Context, name string) (ProfileRecord, error) {
	var profile ProfileRecord
	err := p.db.QueryRowContext(
		ctx,
		"SELECT name, pin_hash, created_at FROM profiles WHERE name = ?",
		name,
	).Scan(&profile.Name, &profile.PinHash, &profile.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profile, ErrNotFound
		}
		return profile, fmt.Errorf("get profile %q: %w", name, err)
	}
	return profile, nil
}

func (p *ProfilesDB) CreateProfileContext(ctx context.Context, name, pinHash string) error {
	_, err := p.db.ExecContext(
		ctx,
		"INSERT INTO profiles (name, pin_hash) VALUES (?, ?)",
		name,
		pinHash,
	)
	if err != nil {
		return fmt.Errorf("create profile %q: %w", name, err)
	}
	return nil
}

func (p *ProfilesDB) DeleteProfileContext(ctx context.Context, name string) error {
	res, err := p.db.ExecContext(ctx, "DELETE FROM profiles WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("delete profile %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete profile %q rows affected: %w", name, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
