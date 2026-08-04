package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sayumi/internal/library"
	"sayumi/internal/storage"
)

const enrichBookID = "book-enrich"

// newEnrichDeps builds the smallest profile a single-book response needs. The
// DB is real because the progress and flair lookups under test are queries, and
// the coalescer's flush interval is set far past the test's lifetime so a
// staged position stays staged and the read-through path is what gets exercised.
func newEnrichDeps(t *testing.T) *profileDeps {
	t.Helper()

	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test DB: %v", err)
		}
	})

	book := storage.BookRecord{
		BookSummary: storage.BookSummary{
			ID:           enrichBookID,
			Title:        "Enriched",
			FilePath:     "/missing/book.epub",
			FileHash:     "enrich-hash",
			FileSize:     1,
			Direction:    "ltr",
			ChapterCount: 10,
		},
		SpineJSON: "[]",
		TocJSON:   "[]",
	}
	if _, err := db.InsertBookContext(t.Context(), book); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}

	coalescer := newProgressCoalescer(db, time.Hour, 16)
	t.Cleanup(coalescer.stop)

	return &profileDeps{DB: db, Books: books, Progress: coalescer}
}

func enrichRequest(pd *profileDeps) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/books/upload", nil)
	return withProfileDeps(req, pd)
}

func TestEnrichBookResponseUsesStoredProgressAndFlair(t *testing.T) {
	t.Parallel()

	pd := newEnrichDeps(t)
	ctx := t.Context()
	if err := pd.DB.SaveProgressContext(ctx, storage.ProgressRecord{
		BookID:    enrichBookID,
		UserID:    "default",
		Chapter:   4,
		Percent:   0.5,
		UpdatedAt: "2026-01-02 03:04:05",
	}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := pd.DB.SetBookFlairCheckedContext(ctx, enrichBookID, "default", "reading",
		map[string]struct{}{"reading": {}}); err != nil {
		t.Fatalf("set book flair: %v", err)
	}

	br := BookResponse{ID: enrichBookID, ChapterCount: 10}
	enrichBookResponse(enrichRequest(pd), pd, &br)

	if br.Progress != 0.45 {
		t.Errorf("progress = %v, want 0.45", br.Progress)
	}
	// SaveProgressContext stamps updated_at server-side, so the value to expect
	// is whatever landed in the row rather than the one handed to it.
	stored, err := pd.DB.GetProgressContext(ctx, enrichBookID, "default")
	if err != nil {
		t.Fatalf("read back progress: %v", err)
	}
	if br.LastReadAt == "" || br.LastReadAt != stored.UpdatedAt {
		t.Errorf("lastReadAt = %q, want the stored %q", br.LastReadAt, stored.UpdatedAt)
	}
	if br.FlairID != "reading" {
		t.Errorf("flairId = %q, want reading", br.FlairID)
	}
}

// A position staged in the coalescer is newer than the persisted row by
// construction, so it must win for the response to be read-after-write
// consistent during the flush window.
func TestEnrichBookResponsePrefersStagedProgress(t *testing.T) {
	t.Parallel()

	pd := newEnrichDeps(t)
	if err := pd.DB.SaveProgressContext(t.Context(), storage.ProgressRecord{
		BookID:    enrichBookID,
		UserID:    "default",
		Chapter:   1,
		UpdatedAt: "2026-01-01 00:00:00",
	}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	pd.Progress.stage(storage.ProgressRecord{
		BookID:    enrichBookID,
		UserID:    "default",
		Chapter:   9,
		UpdatedAt: "2026-02-02 00:00:00",
	})

	br := BookResponse{ID: enrichBookID, ChapterCount: 10}
	enrichBookResponse(enrichRequest(pd), pd, &br)

	if br.Progress != 0.9 {
		t.Errorf("progress = %v, want the staged 0.9", br.Progress)
	}
	if br.LastReadAt != "2026-02-02 00:00:00" {
		t.Errorf("lastReadAt = %q, want the staged timestamp", br.LastReadAt)
	}
}

// An unread book with no flair has nothing to enrich: the missing progress row
// and missing assignment are both normal states, not errors, so the fields stay
// zero and the response is still written.
func TestEnrichBookResponseLeavesUnreadBookAtZero(t *testing.T) {
	t.Parallel()

	pd := newEnrichDeps(t)
	br := BookResponse{ID: enrichBookID, ChapterCount: 10}
	enrichBookResponse(enrichRequest(pd), pd, &br)

	if br.Progress != 0 {
		t.Errorf("progress = %v, want 0", br.Progress)
	}
	if br.LastReadAt != "" {
		t.Errorf("lastReadAt = %q, want empty", br.LastReadAt)
	}
	if br.FlairID != "" {
		t.Errorf("flairId = %q, want empty", br.FlairID)
	}
}

func TestRescanResponse(t *testing.T) {
	t.Parallel()

	scanErr := errors.New("scan stopped")
	tests := []struct {
		name        string
		result      library.ScanResult
		scanErr     error
		wantOK      bool
		imported    int
		refreshed   int
		wantPartial bool
	}{
		{
			name: "reports imports and refreshes",
			result: library.ScanResult{
				ImportedIDs:  []string{"a", "b"},
				RefreshedIDs: []string{"c"},
			},
			wantOK:    true,
			imported:  2,
			refreshed: 1,
		},
		{
			name:   "clean scan with nothing to do",
			wantOK: true,
		},
		{
			// The imported row is committed and the next scan will not report
			// it again, so this has to reach the client as a success.
			name:        "error after committed work is partial",
			result:      library.ScanResult{ImportedIDs: []string{"a"}},
			scanErr:     scanErr,
			wantOK:      true,
			imported:    1,
			wantPartial: true,
		},
		{
			name:    "error with nothing committed",
			scanErr: scanErr,
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp, ok := rescanResponse(tc.result, tc.scanErr)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if resp != nil {
					t.Fatalf("resp = %v, want nil", resp)
				}
				return
			}
			if got := resp["imported"]; got != tc.imported {
				t.Errorf("imported = %v, want %d", got, tc.imported)
			}
			if got := resp["refreshed"]; got != tc.refreshed {
				t.Errorf("refreshed = %v, want %d", got, tc.refreshed)
			}
			partial, hasPartial := resp["partial"]
			if hasPartial != tc.wantPartial {
				t.Errorf("partial present = %v, want %v", hasPartial, tc.wantPartial)
			}
			if tc.wantPartial && partial != true {
				t.Errorf("partial = %v, want true", partial)
			}
		})
	}
}
