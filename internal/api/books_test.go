package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

func TestCoverResponseETag(t *testing.T) {
	t.Parallel()

	if got := coverResponseETag("", "2020-01-01"); got != "" {
		t.Fatalf("empty hash = %q", got)
	}
	got := coverResponseETag("abc", "t1")
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("not quoted: %q", got)
	}
	if !strings.Contains(got, "abc") || !strings.Contains(got, "t1") || !strings.Contains(got, "cover") {
		t.Fatalf("missing parts: %q", got)
	}
	if coverResponseETag("abc", "t1") == coverResponseETag("abc", "t2") {
		t.Fatal("updatedAt must change etag")
	}
	if coverResponseETag("abc", "t1") == coverResponseETag("xyz", "t1") {
		t.Fatal("hash must change etag")
	}
}

func TestBookDetailETag(t *testing.T) {
	t.Parallel()

	if got := bookDetailETag("", "u", "r"); got != "" {
		t.Fatalf("empty hash = %q", got)
	}
	a := bookDetailETag("h1", "u1", "r1")
	b := bookDetailETag("h1", "u1", "r1")
	if a != b || a == "" {
		t.Fatalf("stable etag failed: %q %q", a, b)
	}
	if !strings.Contains(a, bookDetailVersion) {
		t.Fatalf("missing version: %q", a)
	}
	if bookDetailETag("h1", "u1", "r1") == bookDetailETag("h1", "u2", "r1") {
		t.Fatal("bookUpdatedAt must change etag")
	}
	if bookDetailETag("h1", "u1", "r1") == bookDetailETag("h1", "u1", "r2") {
		t.Fatal("lastReadAt must change etag")
	}
	if bookDetailETag("h1", "u1", "") == bookDetailETag("h1", "u1", "r1") {
		t.Fatal("empty vs set lastReadAt must differ")
	}
}

func TestRemoveManagedLibraryFile(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	// Absolute path inside library: removed.
	inside := filepath.Join(lib, "book.epub")
	if err := os.WriteFile(inside, []byte("epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, inside, "book")
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("inside abs file should be removed: %v", err)
	}

	// Absolute path outside library: refused.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "escape.epub")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, outside, "book")
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("escape path must survive: %v", err)
	}

	// Relative path under library (cover-style): removed via os.Root.
	relDir := filepath.Join(lib, ".sayumi", "covers")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(".sayumi", "covers", "c1.jpg")
	absRel := filepath.Join(lib, rel)
	if err := os.WriteFile(absRel, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, rel, "cover")
	if _, err := os.Stat(absRel); !os.IsNotExist(err) {
		t.Fatalf("relative cover should be removed: %v", err)
	}

	// Empty path: no-op.
	removeManagedLibraryFile(lib, "", "book")

	// Missing file: no panic / no error surface.
	removeManagedLibraryFile(lib, filepath.Join(lib, "gone.epub"), "book")
	removeManagedLibraryFile(lib, filepath.Join(".sayumi", "covers", "missing.jpg"), "cover")
}

func bookTestRequest(t *testing.T, pd *profileDeps, method, target string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	r.SetPathValue("id", enrichBookID)
	return withProfileDeps(r, pd)
}

// A query-local driver pins the persisted snapshot and finishes a real flush
// before returning it. This exercises the handler's read ordering without
// timing sleeps, production hooks, or changes to the storage/coalescer APIs.
type bookQueryDriver struct {
	query func(string, []driver.NamedValue) (driver.Rows, error)
}

func (d bookQueryDriver) Connect(context.Context) (driver.Conn, error) { return bookQueryConn{d}, nil }
func (d bookQueryDriver) Driver() driver.Driver                        { return d }
func (d bookQueryDriver) Open(string) (driver.Conn, error)             { return bookQueryConn{d}, nil }

type bookQueryConn struct{ bookQueryDriver }

func (bookQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

func (bookQueryConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (bookQueryConn) Close() error { return nil }
func (c bookQueryConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(query, args)
}

type bookQueryRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *bookQueryRows) Columns() []string { return r.columns }
func (*bookQueryRows) Close() error        { return nil }
func (r *bookQueryRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func bookReadDB(t *testing.T, query func(string, []driver.NamedValue) (driver.Rows, error)) *storage.DB {
	t.Helper()
	db := sql.OpenDB(bookQueryDriver{query: query})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return &storage.DB{DB: db}
}

func TestListBooksKeepsProgressAcrossFlush(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"unread", "previously persisted"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := newEnrichDeps(t)
			realDB := pd.DB
			rows := &bookQueryRows{columns: []string{"book_id", "user_id", "chapter", "percent", "cfi", "updated_at"}}
			if name == "previously persisted" {
				if err := realDB.SaveProgressContext(t.Context(), storage.ProgressRecord{
					BookID: enrichBookID, UserID: "default", Chapter: 1,
				}); err != nil {
					t.Fatal(err)
				}
				old, err := realDB.GetProgressContext(t.Context(), enrichBookID, "default")
				if err != nil {
					t.Fatal(err)
				}
				rows.values = [][]driver.Value{{old.BookID, old.UserID, int64(old.Chapter), old.Percent, nil, old.UpdatedAt}}
			}
			pd.Progress.stage(storage.ProgressRecord{
				BookID: enrichBookID, UserID: "default", Chapter: 9, UpdatedAt: "2026-01-01 00:00:00",
			})
			readProgress := false
			pd.DB = bookReadDB(t, func(query string, args []driver.NamedValue) (driver.Rows, error) {
				if len(args) != 1 || args[0].Value != "default" {
					t.Fatalf("query user = %v", args)
				}
				if strings.Contains(query, "FROM progress") {
					readProgress = true
					// The SQL read has its old snapshot, but persistence now
					// completes and removes the staged entry before it returns.
					pd.Progress.stop()
					return rows, nil
				}
				if strings.Contains(query, "FROM book_flairs") {
					return &bookQueryRows{columns: []string{"book_id", "flair_id"}}, nil
				}
				return nil, errors.New("unexpected query")
			})

			w := httptest.NewRecorder()
			listBooksHandler(nil)(w, bookTestRequest(t, pd, http.MethodGet, "/api/books"))
			if w.Code != http.StatusOK || !readProgress {
				t.Fatalf("list = %d %s; progress read = %v", w.Code, w.Body.String(), readProgress)
			}
			var books []BookResponse
			if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
				t.Fatal(err)
			}
			if len(books) != 1 || books[0].Progress != 0.9 || books[0].LastReadAt != "2026-01-01 00:00:00" {
				t.Fatalf("list lost staged snapshot: %+v", books)
			}
			if pending := pd.Progress.getAll("default"); len(pending) != 0 {
				t.Fatalf("flush did not drain: %v", pending)
			}
			stored, err := realDB.GetProgressContext(t.Context(), enrichBookID, "default")
			if err != nil || stored.Chapter != 9 {
				t.Fatalf("flush did not persist: %+v, %v", stored, err)
			}
		})
	}
}

func TestListBooksProfileAndResponseContracts(t *testing.T) {
	t.Parallel()
	pd, other := newEnrichDeps(t), newEnrichDeps(t)
	pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "default", Chapter: 3})
	pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "another-user", Chapter: 9})
	if err := pd.DB.SetBookFlairCheckedContext(t.Context(), enrichBookID, "default", "reading", map[string]struct{}{"reading": {}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		pd       *profileDeps
		target   string
		progress float64
		flair    string
		empty    bool
	}{
		{name: "own profile", pd: pd, target: "/api/books", progress: 0.3, flair: "reading"},
		{name: "other profile", pd: other, target: "/api/books"},
		{name: "filtered empty array", pd: pd, target: "/api/books?q=unmatched", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			listBooksHandler(nil)(w, bookTestRequest(t, tc.pd, http.MethodGet, tc.target))
			if w.Code != http.StatusOK {
				t.Fatalf("list = %d %s", w.Code, w.Body.String())
			}
			var books []BookResponse
			if err := json.Unmarshal(w.Body.Bytes(), &books); err != nil {
				t.Fatal(err)
			}
			if tc.empty {
				if strings.TrimSpace(w.Body.String()) != "[]" {
					t.Fatalf("empty list = %s", w.Body.String())
				}
				return
			}
			if len(books) != 1 || books[0].ID != enrichBookID || books[0].Title != "Enriched" || books[0].Progress != tc.progress || books[0].FlairID != tc.flair || books[0].Duplicate {
				t.Fatalf("list = %+v", books)
			}
		})
	}
	if summaries := pd.Books.ListSummaries(); len(summaries) != 1 || summaries[0].Title != "Enriched" {
		t.Fatalf("filter mutated cache: %+v", summaries)
	}
}

func TestListBooksLookupFailures(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"progress", "book_flairs"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			pd := newEnrichDeps(t)
			pd.DB = bookReadDB(t, func(query string, _ []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(query, "FROM "+table) {
					return nil, errors.New("private database failure")
				}
				return &bookQueryRows{columns: []string{"book_id", "user_id", "chapter", "percent", "cfi", "updated_at"}}, nil
			})
			w := httptest.NewRecorder()
			listBooksHandler(nil)(w, bookTestRequest(t, pd, http.MethodGet, "/api/books"))
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private database") {
				t.Fatalf("lookup failure = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestGetBookProgressValidators(t *testing.T) {
	t.Parallel()
	pd := newEnrichDeps(t)
	stage := func(chapter int) {
		pd.Progress.stage(storage.ProgressRecord{
			BookID: enrichBookID, UserID: "default", Chapter: chapter, UpdatedAt: "2026-01-01 00:00:00",
		})
	}
	get := func(etag string) *httptest.ResponseRecorder {
		r := bookTestRequest(t, pd, http.MethodGet, "/api/books/"+enrichBookID)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		getBookHandler(nil)(w, r)
		return w
	}
	stage(1)
	first := get("")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" || first.Header().Get("Cache-Control") != bookDetailCacheControl {
		t.Fatalf("first detail = %d, %v", first.Code, first.Header())
	}
	unchanged := get("W/" + etag)
	if unchanged.Code != http.StatusNotModified || unchanged.Body.Len() != 0 || unchanged.Header().Get("ETag") != etag {
		t.Fatalf("unchanged detail = %d %s", unchanged.Code, unchanged.Body.String())
	}
	// Timestamps are second-resolution both before and after persistence.
	// Distinct displayed positions in that same second still need distinct tags.
	stage(9)
	changed := get(etag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
		t.Fatalf("same-second progress change = %d, %v", changed.Code, changed.Header())
	}
	var detail BookDetailResponse
	if err := json.Unmarshal(changed.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Progress != 0.9 || string(detail.Spine) != "[]" || string(detail.TOC) != "[]" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestGetBookContentErrorsHaveNoETag(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing row", "canceled lookup"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := newEnrichDeps(t)
			r := bookTestRequest(t, pd, http.MethodGet, "/api/books/"+enrichBookID)
			wantStatus := http.StatusNotFound
			if name == "missing row" {
				if err := pd.DB.DeleteBookContext(t.Context(), enrichBookID); err != nil {
					t.Fatal(err)
				}
			} else {
				// Keep progress readable so cancellation fails at the content
				// lookup, after the handler has calculated its success validator.
				pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "default", Chapter: 1})
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
				wantStatus = http.StatusInternalServerError
			}
			w := httptest.NewRecorder()
			getBookHandler(nil)(w, r)
			if w.Code != wantStatus || w.Header().Get("ETag") != "" {
				t.Fatalf("content error = %d %v %s", w.Code, w.Header(), w.Body.String())
			}
		})
	}
}

func bookFileProfile(t *testing.T) *profileDeps {
	t.Helper()
	pd := newEnrichDeps(t)
	libPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pd.LibPath = libPath
	pd.Store = epub.NewStore(1)
	bookPath := filepath.Join(libPath, "book.epub")
	coverPath := library.CoverRelPath(enrichBookID)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(libPath, coverPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{bookPath: "epub", filepath.Join(libPath, coverPath): "cover bytes"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := pd.DB.UpdateBookFilePathContext(t.Context(), enrichBookID, bookPath); err != nil {
		t.Fatal(err)
	}
	if err := pd.DB.UpdateBookCoverContext(t.Context(), enrichBookID, coverPath); err != nil {
		t.Fatal(err)
	}
	book, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
	if err != nil {
		t.Fatal(err)
	}
	pd.Books.Add(book)
	pd.coverRoot, err = os.OpenRoot(libPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pd.coverRoot.Close(); err != nil {
			t.Error(err)
		}
	})
	return pd
}

func TestDeleteBookCleanupAndIsolation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"cache hit", "cache miss", "already deleted", "already deleted cache miss"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd, other := bookFileProfile(t), bookFileProfile(t)
			for _, user := range []string{"default", "another-user"} {
				pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: user, Chapter: 5})
			}
			if strings.Contains(name, "cache miss") {
				pd.Books.Remove(enrichBookID)
			}
			alreadyDeleted := strings.HasPrefix(name, "already deleted")
			wantStatus := http.StatusNoContent
			if alreadyDeleted {
				if err := pd.DB.DeleteBookContext(t.Context(), enrichBookID); err != nil {
					t.Fatal(err)
				}
				wantStatus = http.StatusNotFound
			}
			w := httptest.NewRecorder()
			deleteBookHandler(nil)(w, bookTestRequest(t, pd, http.MethodDelete, "/api/books/"+enrichBookID))
			if w.Code != wantStatus {
				t.Fatalf("delete = %d %s", w.Code, w.Body.String())
			}
			if _, ok := pd.Books.Get(enrichBookID); ok {
				t.Fatal("deleted book retained in cache")
			}
			for _, user := range []string{"default", "another-user"} {
				if _, ok := pd.Progress.get(enrichBookID, user); ok {
					t.Errorf("deleted book retained staged progress for %s", user)
				}
			}
			if _, err := pd.DB.GetBookContext(t.Context(), enrichBookID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("deleted row still present: %v", err)
			}
			for _, path := range []string{filepath.Join(pd.LibPath, "book.epub"), filepath.Join(pd.LibPath, library.CoverRelPath(enrichBookID))} {
				_, err := os.Stat(path)
				if alreadyDeleted {
					// A missing DB row does not establish ownership of files,
					// even if a stale cache entry still has their paths.
					if err != nil {
						t.Errorf("unowned file removed: %s: %v", path, err)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("deleted file still present: %s: %v", path, err)
				}
			}
			if _, err := other.DB.GetBookContext(t.Context(), enrichBookID); err != nil {
				t.Fatalf("other profile changed: %v", err)
			}
			if data, err := os.ReadFile(filepath.Join(other.LibPath, "book.epub")); err != nil || string(data) != "epub" {
				t.Fatalf("other profile file changed: %q, %v", data, err)
			}
			if !pd.bookReplaceMu.TryLock() {
				t.Fatal("delete leaked replacement lock")
			}
			pd.bookReplaceMu.Unlock()
		})
	}
}

type bookCoverWriter struct {
	*httptest.ResponseRecorder
	beforeWrite    func()
	beforeDeadline func()
	deadline       time.Time
}

func (w *bookCoverWriter) Write(p []byte) (int, error) {
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	return w.ResponseRecorder.Write(p)
}

func (w *bookCoverWriter) SetWriteDeadline(deadline time.Time) error {
	if w.beforeDeadline != nil {
		w.beforeDeadline()
	}
	w.deadline = deadline
	return nil
}

func TestGetCoverTransferAndLock(t *testing.T) {
	t.Parallel()
	pd := bookFileProfile(t)
	w := &bookCoverWriter{ResponseRecorder: httptest.NewRecorder(), deadline: time.Now()}
	w.beforeDeadline = func() {
		if !pd.bookReplaceMu.TryLock() {
			t.Error("cover deadline cleared only after taking replacement gate")
			return
		}
		pd.bookReplaceMu.Unlock()
	}
	w.beforeWrite = func() {
		if pd.bookReplaceMu.TryLock() {
			pd.bookReplaceMu.Unlock()
			t.Error("cover stream does not exclude replacement/deletion")
		}
	}
	getCoverHandler(nil)(w, bookTestRequest(t, pd, http.MethodGet, "/api/books/"+enrichBookID+"/cover"))
	if w.Code != http.StatusOK || w.Body.String() != "cover bytes" || !w.deadline.IsZero() {
		t.Fatalf("cover = %d %q, deadline = %v", w.Code, w.Body.String(), w.deadline)
	}
	if w.Header().Get("ETag") == "" || w.Header().Get("Cache-Control") != "private, no-cache" || (w.Header().Get("Content-Type") != "image/jpeg" && w.Header().Get("Content-Type") != "image/jpg") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("cover headers = %v", w.Header())
	}
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("cover leaked replacement lock")
	}
	pd.bookReplaceMu.Unlock()

	for _, tc := range []struct {
		name, method, header, value string
		status                      int
		body                        string
	}{
		{name: "conditional", method: http.MethodGet, header: "If-None-Match", value: "W/" + w.Header().Get("ETag"), status: http.StatusNotModified},
		{name: "range", method: http.MethodGet, header: "Range", value: "bytes=0-4", status: http.StatusPartialContent, body: "cover"},
		{name: "head", method: http.MethodHead, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bookTestRequest(t, pd, tc.method, "/api/books/"+enrichBookID+"/cover")
			if tc.header != "" {
				r.Header.Set(tc.header, tc.value)
			}
			out := httptest.NewRecorder()
			getCoverHandler(nil)(out, r)
			if out.Code != tc.status || out.Body.String() != tc.body {
				t.Fatalf("cover = %d %q", out.Code, out.Body.String())
			}
		})
	}
}

func TestDeleteBookLookupFailurePreservesState(t *testing.T) {
	t.Parallel()
	for _, cacheMiss := range []bool{false, true} {
		name := "cache hit"
		if cacheMiss {
			name = "cache miss"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := bookFileProfile(t)
			pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "default", Chapter: 5})
			if cacheMiss {
				pd.Books.Remove(enrichBookID)
			}
			r := bookTestRequest(t, pd, http.MethodDelete, "/api/books/"+enrichBookID)
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			w := httptest.NewRecorder()
			deleteBookHandler(nil)(w, r.WithContext(ctx))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("canceled delete = %d %s", w.Code, w.Body.String())
			}
			if _, err := pd.DB.GetBookContext(t.Context(), enrichBookID); err != nil {
				t.Fatalf("failed delete changed stored book: %v", err)
			}
			if _, ok := pd.Progress.get(enrichBookID, "default"); !ok {
				t.Fatal("failed delete discarded pending progress")
			}
			if _, ok := pd.Books.Get(enrichBookID); ok == cacheMiss {
				t.Fatal("failed delete changed cache membership")
			}
			for path, want := range map[string]string{filepath.Join(pd.LibPath, "book.epub"): "epub", filepath.Join(pd.LibPath, library.CoverRelPath(enrichBookID)): "cover bytes"} {
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Errorf("failed delete changed file %s: %q, %v", path, data, err)
				}
			}
		})
	}
}

func TestGetTocResponseAndLookupFailures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"raw array", "missing cache", "missing row", "canceled lookup"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := newEnrichDeps(t)
			r := bookTestRequest(t, pd, http.MethodGet, "/api/books/"+enrichBookID+"/toc")
			wantStatus := http.StatusOK
			switch name {
			case "missing cache":
				pd.Books.Remove(enrichBookID)
				wantStatus = http.StatusNotFound
			case "missing row":
				if err := pd.DB.DeleteBookContext(t.Context(), enrichBookID); err != nil {
					t.Fatal(err)
				}
				wantStatus = http.StatusNotFound
			case "canceled lookup":
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
				wantStatus = http.StatusInternalServerError
			}
			w := httptest.NewRecorder()
			getTocHandler(nil)(w, r)
			if w.Code != wantStatus || (wantStatus == http.StatusOK && strings.TrimSpace(w.Body.String()) != "[]") {
				t.Fatalf("toc = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBookHandlersMissingProfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "list", handler: listBooksHandler(nil)},
		{name: "detail", handler: getBookHandler(nil)},
		{name: "toc", handler: getTocHandler(nil)},
		{name: "cover", handler: getCoverHandler(nil)},
		{name: "delete", handler: deleteBookHandler(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"code":"server_error"`) {
				t.Fatalf("missing profile = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRemoveManagedLibraryFileRejectsSymlinkParent(t *testing.T) {
	t.Parallel()
	lib, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "book.epub")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(lib, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	// Lexical containment is insufficient: a previously scanned directory
	// can be replaced by a symlink before cleanup runs.
	removeManagedLibraryFile(lib, filepath.Join(link, "book.epub"), "book")
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}
