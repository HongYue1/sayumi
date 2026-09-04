package api

import (
	"testing"
	"time"
)

// lockProfiles must observe every name free in one uninterrupted critical
// section before marking any of them. cond.Wait releases pm.mu, so a version
// that checked the names in sequence and never revisited the earlier ones let a
// second caller claim a name the first had already passed: both then believed
// they held it exclusively, and whichever finished first deleted the other's
// block. Clone is the multi-name caller (src + dst), so it is the one that can
// collide with a concurrent delete/evict of one of its names.
func TestLockProfilesWaitsForNamesClaimedWhileBlocked(t *testing.T) {
	pm := NewProfileManager(t.TempDir())
	ctx := t.Context()

	// "alpha" is free; "beta" is mid-open, so a multi-name lock must park.
	pm.mu.Lock()
	pm.opening["beta"] = true
	pm.mu.Unlock()

	started := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(started)
		unlock, ok := pm.lockProfiles(ctx, "alpha", "beta")
		if ok {
			close(acquired)
			unlock()
		}
	}()
	<-started

	// Let the multi-name caller reach its wait, then have a single-name caller
	// (a delete or evict) claim "alpha" out from under it.
	waitForCond(t, func() bool { return numWaitersParked(pm) })

	unlockAlpha, ok := pm.lockProfiles(ctx, "alpha")
	if !ok {
		t.Fatal("single-name lock on a free profile should succeed")
	}

	// Releasing "beta" wakes the parked caller. It must NOT proceed: "alpha" is
	// now held by someone else. Before the fix it re-checked only "beta" and
	// silently overwrote the alpha block.
	pm.mu.Lock()
	delete(pm.opening, "beta")
	pm.cond.Broadcast()
	pm.mu.Unlock()

	select {
	case <-acquired:
		t.Fatal("lockProfiles granted a profile already blocked by another caller")
	case <-time.After(150 * time.Millisecond):
	}

	// Once alpha is genuinely released, the waiter proceeds.
	unlockAlpha()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("lockProfiles did not acquire after both names were released")
	}
}

// numWaitersParked reports whether the manager's state is consistent with a
// caller sitting in cond.Wait: the blocking marker is still set and no name has
// been claimed yet. It is a best-effort observation used only for sequencing.
func numWaitersParked(pm *ProfileManager) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.opening["beta"] && !pm.blocked["alpha"] && !pm.blocked["beta"]
}

func waitForCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			// Give the goroutine a moment to actually reach cond.Wait after the
			// observable state settles.
			time.Sleep(20 * time.Millisecond)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
