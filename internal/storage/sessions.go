package storage

import (
	"context"
	"fmt"
	"time"
)

// PersistedSession is a "remember me" session stored in profiles.db so it can
// survive a server restart. Expiry is stored as RFC3339 text.
type PersistedSession struct {
	Token   string
	Profile string
	Expiry  time.Time
}

// sessionTimeFormat is the canonical on-disk encoding for session timestamps.
// RFC3339 sorts lexicographically in UTC, so string comparisons in the expiry
// WHERE clauses below are equivalent to time comparisons.
const sessionTimeFormat = time.RFC3339

// SaveSession upserts a persisted session. Only "remember me" sessions are
// written; non-remember sessions live in memory only and are gone after a
// restart by design.
func (p *ProfilesDB) SaveSession(token, profile string, expiry time.Time) error {
	_, err := p.db.ExecContext(
		context.Background(),
		`INSERT INTO sessions (token, profile, expiry) VALUES (?, ?, ?)
		 ON CONFLICT(token) DO UPDATE SET profile = excluded.profile, expiry = excluded.expiry`,
		token, profile, expiry.UTC().Format(sessionTimeFormat),
	)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// DeleteSession removes a single persisted session by token (logout / eviction).
func (p *ProfilesDB) DeleteSession(token string) error {
	if _, err := p.db.ExecContext(
		context.Background(),
		"DELETE FROM sessions WHERE token = ?",
		token,
	); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteSessionsForProfile removes every persisted session for a profile.
// The api session store calls this on profile delete so in-memory tokens go
// away immediately; the sessions.profile ON DELETE CASCADE FK is the DB-level
// backstop if a profile row is removed without this call.
func (p *ProfilesDB) DeleteSessionsForProfile(profile string) error {
	if _, err := p.db.ExecContext(
		context.Background(),
		"DELETE FROM sessions WHERE profile = ?",
		profile,
	); err != nil {
		return fmt.Errorf("delete sessions for profile %q: %w", profile, err)
	}
	return nil
}

// DeleteExpiredSessions prunes persisted sessions whose expiry has passed.
// Uses expiry < now (not <=) so the cutoff matches sessionStore's
// time.Now().After(expiry): a session is still valid at the exact expiry instant.
func (p *ProfilesDB) DeleteExpiredSessions(now time.Time) error {
	if _, err := p.db.ExecContext(
		context.Background(),
		"DELETE FROM sessions WHERE expiry < ?",
		now.UTC().Format(sessionTimeFormat),
	); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

// LoadSessions returns all persisted sessions. Callers should still skip any
// whose expiry has passed; rows with an unparseable timestamp are dropped
// rather than failing the whole load (a corrupt row shouldn't lock everyone
// out at startup).
func (p *ProfilesDB) LoadSessions() (out []PersistedSession, err error) {
	rows, err := p.db.QueryContext(
		context.Background(),
		"SELECT token, profile, expiry FROM sessions",
	)
	if err != nil {
		return nil, fmt.Errorf("load sessions: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close rows: %w", cerr)
		}
	}()
	for rows.Next() {
		var s PersistedSession
		var expiry string
		if err := rows.Scan(&s.Token, &s.Profile, &expiry); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		parsed, perr := time.Parse(sessionTimeFormat, expiry)
		if perr != nil {
			continue
		}
		s.Expiry = parsed
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return out, nil
}
