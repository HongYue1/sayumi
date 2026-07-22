package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// newTestProfilesDB opens a fresh profiles.db under a temp library root.
func newTestProfilesDB(t *testing.T) *ProfilesDB {
	t.Helper()
	pdb, err := OpenProfilesDB(t.TempDir())
	if err != nil {
		t.Fatalf("open profiles db: %v", err)
	}
	t.Cleanup(func() {
		if err := pdb.Close(); err != nil {
			t.Errorf("close profiles db: %v", err)
		}
	})
	return pdb
}

func TestProfilesCRUD(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := context.Background()

	const name = "alice"
	// Storage treats pin_hash as opaque text; bcrypt verification lives in api.
	const pinHash = "hash-alice"

	if err := pdb.CreateProfileContext(ctx, name, pinHash); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := pdb.GetProfileContext(ctx, name)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != name || got.PinHash != pinHash {
		t.Fatalf("get = %+v, want name=%q pinHash=%q", got, name, pinHash)
	}
	if got.CreatedAt == "" {
		t.Fatal("created_at empty")
	}

	// PIN-less profile (empty hash) is a first-class case.
	if err := pdb.CreateProfileContext(ctx, "bob", ""); err != nil {
		t.Fatalf("create open profile: %v", err)
	}
	bob, err := pdb.GetProfileContext(ctx, "bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob.PinHash != "" {
		t.Fatalf("bob pin_hash = %q, want empty", bob.PinHash)
	}

	list, err := pdb.ListProfilesContext(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	if _, err := pdb.GetProfileContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}
	if err := pdb.DeleteProfileContext(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing err = %v, want ErrNotFound", err)
	}

	if err := pdb.DeleteProfileContext(ctx, name); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pdb.GetProfileContext(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestCreateProfileDuplicateName(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := context.Background()

	if err := pdb.CreateProfileContext(ctx, "alice", "h1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := pdb.CreateProfileContext(ctx, "alice", "h2")
	if err == nil {
		t.Fatal("duplicate create: want error")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("duplicate err = %v (%T), want modernc sqlite Error in chain", err, err)
	}
	switch sqliteErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
		// ok — API maps these via isUniqueConstraint
	default:
		t.Fatalf("sqlite code = %d, want PRIMARYKEY or UNIQUE", sqliteErr.Code())
	}
}

func TestListProfilesStableTies(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := context.Background()

	// Same created_at forces the name tie-breaker (PK).
	const ts = "2026-01-02 03:04:05"
	for _, name := range []string{"zeta", "alpha", "mu"} {
		if _, err := pdb.db.ExecContext(
			ctx,
			`INSERT INTO profiles (name, pin_hash, created_at) VALUES (?, '', ?)`,
			name, ts,
		); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	got, err := pdb.ListProfilesContext(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"alpha", "mu", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("count = %d, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("order[%d] = %q, want %q", i, got[i].Name, name)
		}
		if got[i].CreatedAt != ts {
			t.Errorf("created_at[%d] = %q, want %q", i, got[i].CreatedAt, ts)
		}
	}
}

func TestOpenProfilesDBLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pdb, err := OpenProfilesDB(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := pdb.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	path := filepath.Join(root, ".sayumi", "profiles.db")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat profiles.db: %v", err)
	}
	if _, err := pdb.ListProfilesContext(context.Background()); err != nil {
		t.Fatalf("list after open: %v", err)
	}
}

func TestDeleteProfileCascadesSessions(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := context.Background()

	if err := pdb.CreateProfileContext(ctx, "alice", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	expiry := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := pdb.SaveSession("tok-alice", "alice", expiry); err != nil {
		t.Fatalf("save session: %v", err)
	}
	// Delete without DeleteSessionsForProfile — CASCADE alone must clear rows.
	if err := pdb.DeleteProfileContext(ctx, "alice"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	got, err := pdb.LoadSessions()
	if err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("sessions after cascade delete = %+v, want empty", got)
	}
}

func TestSaveSessionRejectsUnknownProfile(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)

	err := pdb.SaveSession("tok", "no-such-profile", time.Now().UTC().Add(time.Hour))
	if err == nil {
		t.Fatal("save session for missing profile: want FK error")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		t.Fatalf("err = %v (%T), want modernc sqlite Error in chain", err, err)
	}
	switch sqliteErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY, sqlite3.SQLITE_CONSTRAINT:
		// ok
	default:
		t.Fatalf("sqlite code = %d, want FOREIGNKEY/CONSTRAINT", sqliteErr.Code())
	}
}
