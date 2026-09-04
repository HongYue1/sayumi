package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionsCRUDAndExpiry(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := t.Context()

	// FK requires the profile row first.
	if err := pdb.CreateProfileContext(ctx, "alice", ""); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := pdb.CreateProfileContext(ctx, "bob", ""); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	expiry := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := pdb.SaveSession(ctx, "tok-a", "alice", expiry); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Token != "tok-a" || got[0].Profile != "alice" {
		t.Fatalf("load = %+v, want tok-a/alice", got)
	}
	if !got[0].Expiry.Equal(expiry) {
		t.Fatalf("expiry = %v, want %v", got[0].Expiry, expiry)
	}

	// Upsert same token: new expiry + profile.
	newExpiry := expiry.Add(24 * time.Hour)
	if err := pdb.SaveSession(ctx, "tok-a", "bob", newExpiry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err = pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load after upsert: %v", err)
	}
	if len(got) != 1 || got[0].Profile != "bob" || !got[0].Expiry.Equal(newExpiry) {
		t.Fatalf("after upsert = %+v, want bob @ newExpiry", got)
	}

	// Second session for alice.
	if err := pdb.SaveSession(ctx, "tok-b", "alice", expiry); err != nil {
		t.Fatalf("save tok-b: %v", err)
	}
	if err := pdb.DeleteSession(ctx, "tok-a"); err != nil {
		t.Fatalf("delete tok-a: %v", err)
	}
	// Idempotent delete of missing token.
	if err := pdb.DeleteSession(ctx, "tok-missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}

	got, err = pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load after delete: %v", err)
	}
	if len(got) != 1 || got[0].Token != "tok-b" {
		t.Fatalf("after delete = %+v, want only tok-b", got)
	}

	if err := pdb.DeleteSessionsForProfile(ctx, "alice"); err != nil {
		t.Fatalf("delete for alice: %v", err)
	}
	got, err = pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load after profile purge: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after profile purge = %+v, want empty", got)
	}
}

func TestDeleteExpiredSessionsBoundary(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := t.Context()

	if err := pdb.CreateProfileContext(ctx, "alice", ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Fixed "now" for deterministic boundary checks.
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	exact := now
	future := now.Add(time.Hour)

	for _, s := range []struct {
		token  string
		expiry time.Time
	}{
		{"tok-past", past},
		{"tok-exact", exact},
		{"tok-future", future},
	} {
		if err := pdb.SaveSession(ctx, s.token, "alice", s.expiry); err != nil {
			t.Fatalf("save %s: %v", s.token, err)
		}
	}

	if err := pdb.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatalf("prune: %v", err)
	}

	got, err := pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// past gone; exact kept (matches time.After); future kept.
	want := map[string]bool{"tok-exact": true, "tok-future": true}
	if len(got) != len(want) {
		t.Fatalf("after prune = %+v, want exact+future", got)
	}
	for _, s := range got {
		if !want[s.Token] {
			t.Errorf("unexpected token %q still present", s.Token)
		}
	}
}

func TestLoadSessionsSkipsCorruptExpiry(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := t.Context()

	if err := pdb.CreateProfileContext(ctx, "alice", ""); err != nil {
		t.Fatalf("create: %v", err)
	}

	goodExpiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := pdb.SaveSession(ctx, "tok-good", "alice", goodExpiry); err != nil {
		t.Fatalf("save good: %v", err)
	}
	// Bypass SaveSession to plant an unparseable expiry.
	if _, err := pdb.db.ExecContext(
		ctx,
		`INSERT INTO sessions (token, profile, expiry) VALUES (?, 'alice', ?)`,
		"tok-bad", "not-a-timestamp",
	); err != nil {
		t.Fatalf("insert corrupt: %v", err)
	}

	got, err := pdb.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Token != "tok-good" {
		t.Fatalf("load = %+v, want only tok-good (corrupt skipped)", got)
	}
}

// TestSessionsHonorCancellation pins the ctx threading: every session method
// must fail fast on a canceled context instead of running on Background.
func TestSessionsHonorCancellation(t *testing.T) {
	t.Parallel()
	pdb := newTestProfilesDB(t)
	ctx := t.Context()

	if err := pdb.CreateProfileContext(ctx, "alice", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	expiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := pdb.SaveSession(ctx, "tok-a", "alice", expiry); err != nil {
		t.Fatalf("seed: %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if err := pdb.SaveSession(canceled, "tok-b", "alice", expiry); !errors.Is(err, context.Canceled) {
		t.Errorf("SaveSession err = %v, want context.Canceled", err)
	}
	if err := pdb.DeleteSession(canceled, "tok-a"); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteSession err = %v, want context.Canceled", err)
	}
	if err := pdb.DeleteSessionsForProfile(canceled, "alice"); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteSessionsForProfile err = %v, want context.Canceled", err)
	}
	if err := pdb.DeleteExpiredSessions(canceled, time.Now()); !errors.Is(err, context.Canceled) {
		t.Errorf("DeleteExpiredSessions err = %v, want context.Canceled", err)
	}
	if _, err := pdb.LoadSessions(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("LoadSessions err = %v, want context.Canceled", err)
	}
}
