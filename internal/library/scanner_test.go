package library

import (
	"context"
	"sync"
	"testing"

	"sayumi/internal/storage"
)

// TestScanNowSingleFlight runs many concurrent ScanNow calls against an empty
// library and asserts they all succeed with consistent results. It exercises
// the single-flight guard: overlapping callers must coalesce onto one in-flight
// scan without data races or errors, and the guard must reset so a later scan
// still runs.
func TestScanNowSingleFlight(t *testing.T) {
	dir := t.TempDir()

	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	scanner := NewScanner(dir, db)
	ctx := context.Background()

	const goroutines = 16

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	counts := make([]int, goroutines)

	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			ids, scanErr := scanner.ScanNow(ctx)
			errs[i] = scanErr
			counts[i] = len(ids)
		}()
	}
	wg.Wait()

	for i := range goroutines {
		if errs[i] != nil {
			t.Errorf("concurrent ScanNow[%d] failed: %v", i, errs[i])
		}
		if counts[i] != 0 {
			t.Errorf("concurrent ScanNow[%d] imported %d books from an empty library", i, counts[i])
		}
	}

	// The single-flight guard must reset after the burst so subsequent scans
	// still execute rather than block forever.
	if _, err := scanner.ScanNow(ctx); err != nil {
		t.Fatalf("ScanNow after concurrent burst failed: %v", err)
	}
}
