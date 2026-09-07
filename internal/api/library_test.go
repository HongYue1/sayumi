package api

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/library"
	"sayumi/internal/storage"
)

func TestFilterAndSortBooksNormalizationAndTies(t *testing.T) {
	t.Parallel()
	input := []BookResponse{
		{ID: "2", Title: "Écho", Author: "ADAMS", Progress: 0.5, AddedAt: "2026-01-02 00:00:00"},
		{ID: "1", Title: "éCHO", Author: "adams", Progress: 0.5, AddedAt: "2026-01-02 00:00:00"},
		{ID: "3", Title: "Zebra", Author: "Adams", Progress: 0.5, AddedAt: "2026-01-01 00:00:00", LastReadAt: "2026-03-01 00:00:00"},
		{ID: "4", Title: "Alpha", Author: "Baker", Progress: 0.1, AddedAt: "2026-01-01 00:00:00", LastReadAt: "2026-02-01 00:00:00"},
	}
	for _, tc := range []struct {
		name, query, sort, order string
		want                     []string
	}{
		{"normalized title descending", "", " TITLE ", " DeSc ", []string{"2", "1", "3", "4"}},
		{"unicode query", "\tÉCHo ", "", "", []string{"2", "1"}},
		{"normalized author query", " ADAms ", "", "", []string{"3", "2", "1"}},
		{"author title tie break", "", "author", "", []string{"3", "2", "1", "4"}},
		{"descending author ties", "", "author", "desc", []string{"4", "2", "1", "3"}},
		{"stable added ties", "", "added", "", []string{"3", "4", "2", "1"}},
		{"unread first ascending", "", "read", "", []string{"2", "1", "4", "3"}},
		{"unread last descending", "", "read", "desc", []string{"3", "4", "2", "1"}},
		{"progress title tie break", "", "progress", "desc", []string{"2", "1", "3", "4"}},
		{"unknown order defaults ascending", "", "title", "sideways", []string{"4", "3", "2", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterAndSortBooks(slices.Clone(input), tc.query, tc.sort, tc.order)
			if !slices.Equal(ids(got), tc.want) {
				t.Fatalf("ids = %v, want %v", ids(got), tc.want)
			}
		})
	}
}

func TestFilterAndSortBooksSliceOwnership(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"", " \t "} {
		input := books()
		got := filterAndSortBooks(input, query, "title", "")
		if &got[0] != &input[0] {
			t.Errorf("query %q did not sort caller-owned storage", query)
		}
	}
	input := books()
	before := slices.Clone(input)
	got := filterAndSortBooks(input, "a", "title", "desc")
	if !slices.Equal(input, before) {
		t.Fatal("filter sorted the input slice")
	}
	got[0].Title = "changed response"
	if !slices.Equal(input, before) {
		t.Fatal("filtered result aliases the input slice")
	}
	if got := filterAndSortBooks(nil, "", "author", "desc"); len(got) != 0 {
		t.Fatalf("nil input = %v", got)
	}
}

func TestRescanResponseCountsDistinctBooks(t *testing.T) {
	t.Parallel()
	for _, scanErr := range []error{nil, context.Canceled} {
		result := library.ScanResult{
			ImportedIDs:  []string{"new", "new"},
			RefreshedIDs: []string{"existing", "new", "existing"},
		}
		imported, refreshed := slices.Clone(result.ImportedIDs), slices.Clone(result.RefreshedIDs)
		resp, ok := rescanResponse(result, scanErr)
		if !ok || resp["imported"] != 1 || resp["refreshed"] != 1 {
			t.Errorf("response = %v, %v; want one new and one existing book", resp, ok)
		}
		if scanErr != nil && resp["partial"] != true {
			t.Errorf("partial response = %v", resp)
		}
		if !slices.Equal(result.ImportedIDs, imported) || !slices.Equal(result.RefreshedIDs, refreshed) {
			t.Fatal("response mutated shared scanner results")
		}
	}
}

type libraryScanRecorder struct {
	*httptest.ResponseRecorder
	deadlineCleared bool
	onHeader        func()
}

func (w *libraryScanRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlineCleared = deadline.IsZero()
	return nil
}

func (w *libraryScanRecorder) WriteHeader(status int) {
	if w.onHeader != nil {
		w.onHeader()
	}
	w.ResponseRecorder.WriteHeader(status)
}

func assertLibraryScanGateReleased(t *testing.T, pd *profileDeps) {
	t.Helper()
	if !pd.libraryScanMu.TryLock() {
		t.Fatal("library scan gate leaked after handler returned")
	}
	pd.libraryScanMu.Unlock()
}

func TestRescanGuardsScanAndPublication(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"scan", "publication"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			pd := newUploadTestDeps(t)
			pd.refs = 1
			pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
			if err := os.WriteFile(filepath.Join(pd.LibPath, "book.epub"), minimalEPUBBytes(t, "Scanned"), 0o644); err != nil {
				t.Fatal(err)
			}
			w := &libraryScanRecorder{ResponseRecorder: httptest.NewRecorder()}
			reached := false
			failureDB := bookReadDB(t, func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				reached = true
				if !w.deadlineCleared {
					t.Error("response deadline not cleared before scan work")
				}
				if pd.libraryScanMu.TryRLock() {
					pd.libraryScanMu.RUnlock()
					t.Errorf("%s can overlap an upload/edit/delete mutation", phase)
				}
				if phase == "scan" {
					if pd.bookReplaceMu.TryLock() {
						pd.bookReplaceMu.Unlock()
					} else {
						t.Error("whole scan blocks generation-sensitive readers")
					}
				} else if strings.Contains(query, "spine_json") || strings.Contains(query, "toc_json") {
					t.Error("rescan reload fetched heavy detail columns")
				}
				return nil, errors.New("private storage failure")
			})
			wantStatus := http.StatusInternalServerError
			if phase == "scan" {
				pd.Scanner = library.NewScanner(pd.LibPath, failureDB)
			} else {
				// Keep the real scanner DB; fail only the API's post-commit reload.
				pd.DB = failureDB
				wantStatus = http.StatusOK
			}
			rescanLibraryHandler(nil)(w, bookTestRequest(t, pd, http.MethodPost, "/api/library/rescan"))
			if !reached || w.Code != wantStatus || strings.Contains(w.Body.String(), "private storage") {
				t.Errorf("%s = %d %s, reached=%v", phase, w.Code, w.Body.String(), reached)
			}
			if pd.refs != 1 {
				t.Errorf("handler released the middleware-owned profile reference: %d", pd.refs)
			}
			assertLibraryScanGateReleased(t, pd)
		})
	}
}

func TestLibraryMutationsExcludeRescan(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"upload", "duplicate", "metadata", "cover", "fileless cover", "delete"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			var pd *profileDeps
			var req *http.Request
			var handler http.HandlerFunc
			wantStatus := http.StatusOK
			switch operation {
			case "upload", "duplicate":
				pd = newUploadTestDeps(t)
				content := minimalEPUBBytes(t, "Mutation")
				if operation == "duplicate" {
					if w := uploadEPUB(t, pd, "book.epub", content); w.Code != http.StatusCreated {
						t.Fatalf("seed upload = %d %s", w.Code, w.Body.String())
					}
				} else {
					wantStatus = http.StatusCreated
				}
				body, contentType := multipartUploadBody(t, "book.epub", content)
				req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/books/upload", bytes.NewReader(body))
				req.Header.Set("Content-Type", contentType)
				req = withProfileDeps(req, pd)
				handler = uploadBookHandler(nil)
			case "metadata", "cover", "fileless cover":
				pd = bookEditProfile(t, operation == "fileless cover")
				cover := operation != "metadata"
				req = bookEditRequest(t, pd, cover)
				handler = func(w http.ResponseWriter, r *http.Request) { serveBookEdit(w, r, cover) }
			case "delete":
				pd = bookEditProfile(t, false)
				req = bookTestRequest(t, pd, http.MethodDelete, "/api/books/"+enrichBookID)
				handler = deleteBookHandler(nil)
				wantStatus = http.StatusNoContent
			}
			checked := false
			w := &libraryScanRecorder{ResponseRecorder: httptest.NewRecorder(), onHeader: func() {
				checked = true
				if pd.libraryScanMu.TryLock() {
					pd.libraryScanMu.Unlock()
					t.Error("rescan can enter before mutation reconciliation finishes")
				}
				if pd.libraryScanMu.TryRLock() {
					pd.libraryScanMu.RUnlock()
				} else {
					t.Error("ordinary mutations unnecessarily exclude each other")
				}
			}}
			handler(w, req)
			if !checked || w.Code != wantStatus {
				t.Errorf("mutation = %d %s, gate checked=%v", w.Code, w.Body.String(), checked)
			}
			assertLibraryScanGateReleased(t, pd)
		})
	}
}

func TestRescanImportsAndReconcilesDuplicatePaths(t *testing.T) {
	t.Parallel()
	pd := newUploadTestDeps(t)
	content := minimalEPUBBytes(t, "Scanned")
	original := filepath.Join(pd.LibPath, "book.epub")
	if err := os.WriteFile(original, content, 0o644); err != nil {
		t.Fatal(err)
	}
	scan := func(imported, refreshed int) {
		t.Helper()
		w := httptest.NewRecorder() // A writer without deadline support is valid.
		rescanLibraryHandler(nil)(w, bookTestRequest(t, pd, http.MethodPost, "/api/library/rescan"))
		var resp struct {
			Imported  int  `json:"imported"`
			Refreshed int  `json:"refreshed"`
			Partial   bool `json:"partial"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || resp.Imported != imported || resp.Refreshed != refreshed || resp.Partial {
			t.Fatalf("rescan = %d %+v, want imported=%d refreshed=%d", w.Code, resp, imported, refreshed)
		}
		assertLibraryScanGateReleased(t, pd)
	}
	scan(1, 0)
	summaries := pd.Books.ListSummaries()
	if len(summaries) != 1 || summaries[0].Title != "Scanned" {
		t.Fatalf("import not published: %+v", summaries)
	}
	id := summaries[0].ID
	scan(0, 0)
	if err := os.Rename(original, filepath.Join(pd.LibPath, "moved.epub")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pd.LibPath, "copy.epub"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	// Both previously unknown paths report the same canonical ID. The public
	// count is books refreshed, not the number of path reconciliation events.
	scan(0, 1)
	stored, found, err := pd.DB.GetBookSummaryContext(t.Context(), id)
	cached, cachedOK := pd.Books.Get(id)
	if err != nil || !found || !cachedOK || cached.BookSummary != stored || stored.FilePath == original {
		t.Fatalf("refreshed cache = %+v, DB = %+v, found=%v cached=%v err=%v", cached, stored, found, cachedOK, err)
	}
}

func TestRescanWarmsCommittedBooksAfterCancellation(t *testing.T) {
	t.Parallel()
	pd := newUploadTestDeps(t)
	pd.refs = 1
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	if err := os.WriteFile(filepath.Join(pd.LibPath, "book.epub"), minimalEPUBBytes(t, "Committed"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := withProfileDeps(httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/library/rescan", nil), pd)
	w := httptest.NewRecorder()
	pd.bookReplaceMu.Lock()
	unlock := sync.OnceFunc(pd.bookReplaceMu.Unlock)
	done := make(chan struct{})
	go func() {
		defer close(done)
		rescanLibraryHandler(nil)(w, req)
	}()
	defer func() {
		unlock()
		waitUploadDone(t, done)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	var id string
	for id == "" {
		rows, err := pd.DB.ListBookSummariesContext(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			id = rows[0].ID
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("scan did not commit before cache publication")
		case <-tick.C:
		}
	}
	cancel()
	unlock()
	waitUploadDone(t, done)
	cached, found := pd.Books.Get(id)
	if !found || cached.Title != "Committed" || pd.refs != 1 {
		t.Fatalf("committed book = %+v, found=%v refs=%d", cached, found, pd.refs)
	}
	assertLibraryScanGateReleased(t, pd)
}

func TestRescanBackfillsMovedBookOnce(t *testing.T) {
	t.Parallel()
	pd := bookEditProfile(t, false)
	pd.Scanner = library.NewScanner(pd.LibPath, pd.DB)
	before, ok := pd.Books.Get(enrichBookID)
	if !ok {
		t.Fatal("fixture book missing")
	}
	moved := filepath.Join(pd.LibPath, "moved.epub")
	if err := os.Rename(before.FilePath, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := pd.DB.ExecContext(t.Context(), `UPDATE books SET cover_path = '', has_cover = 0, cover_checked = 0 WHERE id = ?`, enrichBookID); err != nil {
		t.Fatal(err)
	}
	before.HasCover, before.CoverPath = false, ""
	pd.Books.Add(before)
	w := httptest.NewRecorder()
	rescanLibraryHandler(nil)(w, bookTestRequest(t, pd, http.MethodPost, "/api/library/rescan"))
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || resp["imported"] != float64(0) || resp["refreshed"] != float64(1) {
		t.Fatalf("path plus cover refresh = %d %v", w.Code, resp)
	}
	cached, ok := pd.Books.Get(enrichBookID)
	stored, found, err := pd.DB.GetBookSummaryContext(t.Context(), enrichBookID)
	if err != nil || !found || !ok || cached.BookSummary != stored || !cached.HasCover || cached.FilePath != moved || cached.UpdatedAt == before.UpdatedAt {
		t.Fatalf("backfill cache = %+v, DB = %+v, err=%v", cached, stored, err)
	}
	if cached.SpineJSON != "" || cached.TocJSON != "" {
		t.Fatal("rescan eagerly retained heavy book details")
	}
	assertLibraryScanGateReleased(t, pd)
}

func TestListBooksSortsEnrichedFullList(t *testing.T) {
	t.Parallel()
	pd := newUploadTestDeps(t)
	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		if err := os.WriteFile(filepath.Join(pd.LibPath, title+".epub"), minimalEPUBBytes(t, title), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan := httptest.NewRecorder()
	rescanLibraryHandler(nil)(scan, bookTestRequest(t, pd, http.MethodPost, "/api/library/rescan"))
	if scan.Code != http.StatusOK {
		t.Fatalf("seed scan = %d %s", scan.Code, scan.Body.String())
	}
	before := pd.Books.ListSummaries()
	byTitle := make(map[string]string)
	for _, summary := range before {
		byTitle[summary.Title] = summary.ID
	}
	if len(byTitle) != 3 {
		t.Fatalf("seed books = %v", byTitle)
	}
	if err := pd.DB.SaveProgressContext(t.Context(), storage.ProgressRecord{BookID: byTitle["Alpha"], UserID: "default", Percent: 0.4}); err != nil {
		t.Fatal(err)
	}
	pd.Progress.stage(storage.ProgressRecord{BookID: byTitle["Beta"], UserID: "default", Percent: 0.8, UpdatedAt: "2026-01-02 00:00:00"})
	if err := pd.DB.SetBookFlairCheckedContext(t.Context(), byTitle["Beta"], "default", "reading", map[string]struct{}{"reading": {}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	listBooksHandler(nil)(w, bookTestRequest(t, pd, http.MethodGet, "/api/books?q=a&sort=progress&order=desc"))
	var got []BookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{byTitle["Beta"], byTitle["Alpha"], byTitle["Gamma"]}
	if w.Code != http.StatusOK || !slices.Equal(ids(got), want) {
		t.Fatalf("full enriched list = %d %+v, want IDs %v", w.Code, got, want)
	}
	if got[0].Progress != 0.8 || got[0].LastReadAt != "2026-01-02 00:00:00" || got[0].FlairID != "reading" || got[1].Progress != 0.4 {
		t.Fatalf("enrichment was not preserved through sorting: %+v", got)
	}
	if !slices.Equal(pd.Books.ListSummaries(), before) {
		t.Fatal("list filtering/sorting changed cache order or metadata")
	}
}

// Pin the cancellation/INSERT acknowledgement boundary without timing sleeps.
// A driver may return cancellation after its autocommit row is already visible.
func TestRescanAndUploadReconcileCanceledInsert(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		upload, commit bool
	}{
		{"rescan before commit", false, false},
		{"rescan after commit", false, true},
		{"upload before commit", true, false},
		{"upload after commit", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := newUploadTestDeps(t)
			original := pd.DB.DB
			var sequence int
			var name, dbPath string
			if err := original.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &dbPath); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pd.DB.DB = sql.OpenDB(libraryInsertConnector{base: original.Driver(), dsn: dbPath, commit: tc.commit, cancel: cancel})
			// The fixture closes the replacement pool after stopping progress.
			t.Cleanup(func() {
				if err := original.Close(); err != nil {
					t.Error(err)
				}
			})
			content := minimalEPUBBytes(t, "Ambiguous")
			w := httptest.NewRecorder()
			if tc.upload {
				body, contentType := multipartUploadBody(t, "Ambiguous.epub", content)
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/books/upload", bytes.NewReader(body))
				r.Header.Set("Content-Type", contentType)
				uploadBookHandler(nil)(w, withProfileDeps(r, pd))
			} else {
				if err := os.WriteFile(filepath.Join(pd.LibPath, "Ambiguous.epub"), content, 0o644); err != nil {
					t.Fatal(err)
				}
				r := bookTestRequest(t, pd, http.MethodPost, "/api/library/rescan")
				rescanLibraryHandler(nil)(w, withProfileDeps(r.WithContext(ctx), pd))
			}
			if ctx.Err() == nil {
				t.Fatal("cancellation boundary was not reached")
			}
			stored, err := pd.DB.ListBookSummariesContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.commit {
				want = 1
			}
			if len(stored) != want || len(pd.Books.ListSummaries()) != want {
				t.Fatalf("after canceled insert: stored=%d cached=%d want=%d", len(stored), len(pd.Books.ListSummaries()), want)
			}
			if tc.commit {
				cached, ok := pd.Books.Get(stored[0].ID)
				if !ok || cached.BookSummary != stored[0] {
					t.Fatalf("committed cache mismatch: %+v, DB=%+v", cached, stored[0])
				}
				if data, err := os.ReadFile(stored[0].FilePath); err != nil || !bytes.Equal(data, content) {
					t.Fatalf("committed file lost: %v", err)
				}
			}
			if !tc.upload {
				var resp map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				if tc.commit && (w.Code != http.StatusOK || resp["imported"] != float64(1) || resp["partial"] != true) {
					t.Fatalf("committed partial scan = %d %v", w.Code, resp)
				}
				if !tc.commit && w.Code != http.StatusInternalServerError {
					t.Fatalf("uncommitted scan = %d %v", w.Code, resp)
				}
			} else if w.Body.Len() != 0 {
				t.Fatalf("canceled upload wrote a response: %s", w.Body.String())
			}
			assertLibraryScanGateReleased(t, pd)
		})
	}
}

type libraryInsertConnector struct {
	base   driver.Driver
	dsn    string
	commit bool
	cancel context.CancelFunc
}

func (c libraryInsertConnector) Driver() driver.Driver { return c.base }
func (c libraryInsertConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return libraryInsertConn{Conn: conn, commit: c.commit, cancel: c.cancel}, nil
}

type libraryInsertConn struct {
	driver.Conn
	commit bool
	cancel context.CancelFunc
}

func (c libraryInsertConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	insert := strings.Contains(query, "INSERT INTO books (")
	if insert && !c.commit {
		c.cancel()
		return nil, context.Canceled
	}
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, errors.New("test driver lacks contextual execution")
	}
	result, err := execer.ExecContext(ctx, query, args)
	if insert && err == nil {
		c.cancel()
		return nil, context.Canceled
	}
	return result, err
}

func books() []BookResponse {
	return []BookResponse{
		{ID: "1", Title: "Beta", Author: "Zane", Progress: 0.5, AddedAt: "2026-01-02", LastReadAt: "2026-03-01"},
		{ID: "2", Title: "alpha", Author: "Adams", Progress: 0.9, AddedAt: "2026-01-03", LastReadAt: "2026-02-01"},
		{ID: "3", Title: "Gamma", Author: "Moore", Progress: 0.1, AddedAt: "2026-01-01", LastReadAt: "2026-04-01"},
	}
}

func ids(bs []BookResponse) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterAndSortBooks(t *testing.T) {
	tests := []struct {
		name           string
		q, sort, order string
		want           []string
	}{
		{"default is title asc, case-insensitive", "", "", "", []string{"2", "1", "3"}},
		{"title desc", "", "title", "desc", []string{"3", "1", "2"}},
		{"author asc", "", "author", "asc", []string{"2", "3", "1"}},
		{"added desc (newest first)", "", "added", "desc", []string{"2", "1", "3"}},
		{"read desc (most recent first)", "", "read", "desc", []string{"3", "1", "2"}},
		{"progress desc", "", "progress", "desc", []string{"2", "1", "3"}},
		{"query filters title", "alpha", "", "", []string{"2"}},
		{"query filters author, case-insensitive", "moore", "", "", []string{"3"}},
		{"query with no match", "zzz", "", "", []string{}},
		{"unknown sort falls back to title", "", "bogus", "", []string{"2", "1", "3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(filterAndSortBooks(books(), tc.q, tc.sort, tc.order))
			if !eq(got, tc.want) {
				t.Errorf("filterAndSortBooks(q=%q sort=%q order=%q) = %v, want %v",
					tc.q, tc.sort, tc.order, got, tc.want)
			}
		})
	}
}

func TestFilterAndSortBooksSortsInPlaceOnEmptyQuery(t *testing.T) {
	in := books()
	got := filterAndSortBooks(in, "", "title", "asc")
	// No-query path sorts the caller's slice in place and returns it. The list
	// handler owns the slice it passes, so this avoided clone is intentional.
	if got[0].ID != "2" || in[0].ID != "2" {
		t.Errorf("expected in-place title-asc sort (alpha first), got %v / in %v",
			ids(got), ids(in))
	}
}
