package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

func TestChapterResponseETag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hash  string
		index int
		want  string
	}{
		{name: "unknown hash", index: 0, want: ""},
		{name: "first chapter", hash: "hash", index: 0, want: `"hash:0:` + epub.ChapterRenderVersion + `"`},
		{name: "later chapter", hash: "hash", index: 12, want: `"hash:12:` + epub.ChapterRenderVersion + `"`},
		{name: "new generation", hash: "new", index: 0, want: `"new:0:` + epub.ChapterRenderVersion + `"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := chapterResponseETag(tc.hash, tc.index); got != tc.want {
				t.Errorf("ETag = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIfNoneMatchMatches(t *testing.T) {
	t.Parallel()

	const etag = `"hash:0:render"`
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{
			name:   "strong tag",
			values: []string{etag},
			want:   true,
		},
		{
			name:   "weak tag",
			values: []string{"W/" + etag},
			want:   true,
		},
		{
			name:   "wildcard",
			values: []string{"*"},
			want:   true,
		},
		{
			name:   "comma list",
			values: []string{`"other", ` + etag},
			want:   true,
		},
		{
			name:   "repeated fields",
			values: []string{`"other"`, etag},
			want:   true,
		},
		{
			name:   "no match",
			values: []string{`"other"`, `W/"another"`},
			want:   false,
		},
		{
			name:   "quoted comma and star are opaque",
			values: []string{`"other,*,value"`},
			want:   false,
		},
		{
			name:   "weak quoted comma and star are opaque",
			values: []string{`W/"other,*,value"`},
			want:   false,
		},
		{
			name:   "match after quoted comma",
			values: []string{`"other,*,value", ` + etag},
			want:   true,
		},
		{
			name:   "unterminated tag cannot expose a match",
			values: []string{`"unterminated, ` + etag},
			want:   false,
		},
		{
			name:   "invalid prefix cannot expose a match",
			values: []string{`invalid, ` + etag},
			want:   false,
		},
		{
			name:   "unclosed tag",
			values: []string{`"unclosed`},
			want:   false,
		},
		{
			name:   "suffix after matching tag",
			values: []string{etag + "suffix"},
			want:   false,
		},
		{
			name:   "wildcard suffix",
			values: []string{"*suffix"},
			want:   false,
		},
		{
			name:   "whitespace and empty list elements",
			values: []string{" \t, , " + etag + "\t, "},
			want:   true,
		},
		{
			name: "missing header",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range tc.values {
				req.Header.Add("If-None-Match", value)
			}
			if got := ifNoneMatchMatches(req, etag); got != tc.want {
				t.Errorf("ifNoneMatchMatches() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIfNoneMatchMatchesEmptyETag(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", "*")
	if ifNoneMatchMatches(req, "") {
		t.Fatal("empty server ETag matched")
	}
}

func TestRequestContextDone(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if !requestContextDone(req, errors.Join(errors.New("render"), context.Canceled)) {
		t.Fatal("wrapped context cancellation was not detected")
	}
	if requestContextDone(req, errors.New("render failed")) {
		t.Fatal("unrelated error reported as request cancellation")
	}

	timeoutCtx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	<-timeoutCtx.Done()
	timedOutReq := req.WithContext(timeoutCtx)
	if !requestContextDone(timedOutReq, errors.New("render interrupted")) {
		t.Fatal("request deadline was not detected")
	}
}

func TestGetChapterHandlerMatchingETagSkipsSpineLoad(t *testing.T) {
	t.Parallel()

	const (
		bookID   = "book-1"
		fileHash = "hash-1"
	)
	book := storage.BookRecord{
		ID:           bookID,
		Title:        "Book",
		FilePath:     "/missing/book.epub",
		FileHash:     fileHash,
		FileSize:     1,
		Direction:    "ltr",
		ChapterCount: 1,
		// A spine load would fail parsing and turn this request into a 500.
		// The matching validator must return before touching this value.
		SpineJSON: "not-json",
		TocJSON:   "[]",
	}
	pd := newChapterTestDeps(t, book)

	req := httptest.NewRequest(http.MethodGet, "/api/books/book-1/chapters/0", nil)
	req.SetPathValue("id", bookID)
	req.SetPathValue("index", "0")
	etag := chapterResponseETag(fileHash, 0)
	req.Header.Set("If-None-Match", etag)
	req = withProfileDeps(req, pd)
	recorder := httptest.NewRecorder()

	getChapterHandler(nil)(recorder, req)

	if recorder.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNotModified, recorder.Body.String())
	}
	if got := recorder.Header().Get("ETag"); got != etag {
		t.Errorf("ETag = %q, want %q", got, etag)
	}
	if got := recorder.Header().Get("Cache-Control"); got != chapterCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, chapterCacheControl)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("304 body = %q, want empty", recorder.Body.String())
	}
}

func TestIfNoneMatchMatchesQuotedETags(t *testing.T) {
	t.Parallel()

	for _, etag := range []string{
		`"hash,0:render"`,
		coverResponseETag("hash", "2026-09-06 12:00:00"),
		bookDetailETag("hash", "2026-09-06 12:00:00", "2026-09-06 12:01:00"),
	} {
		t.Run(etag, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			req.Header.Set("If-None-Match", `"other", W/`+etag)
			if !ifNoneMatchMatches(req, etag) {
				t.Fatalf("quoted validator %q did not match", etag)
			}
		})
	}
}

func TestGetChapterHandlerPreconditions(t *testing.T) {
	t.Parallel()

	const bookID = "chapter-book"
	const fileHash = "chapter-hash"
	etag := chapterResponseETag(fileHash, 0)
	pd := newChapterTestDeps(t, storage.BookRecord{
		ID:           bookID,
		Title:        "Book",
		FilePath:     "/missing/book.epub",
		FileHash:     fileHash,
		FileSize:     1,
		ChapterCount: 1,
		SpineJSON:    "not-json",
		TocJSON:      "[]",
	})
	tests := []struct {
		name    string
		ifMatch []string
		status  int
	}{
		{name: "absent", status: http.StatusNotModified},
		{name: "strong", ifMatch: []string{etag}, status: http.StatusNotModified},
		{name: "wildcard", ifMatch: []string{"*"}, status: http.StatusNotModified},
		{name: "later field", ifMatch: []string{`"old"`, etag}, status: http.StatusNotModified},
		{name: "empty", ifMatch: []string{""}, status: http.StatusPreconditionFailed},
		{name: "empty then strong", ifMatch: []string{"", etag}, status: http.StatusNotModified},
		{name: "malformed", ifMatch: []string{`"unclosed`}, status: http.StatusPreconditionFailed},
		{name: "strong after weak", ifMatch: []string{"W/" + etag + ", " + etag}, status: http.StatusNotModified},
		{name: "quoted comma before match", ifMatch: []string{`"other,*,value", ` + etag}, status: http.StatusNotModified},
		{name: "stale", ifMatch: []string{`"old"`}, status: http.StatusPreconditionFailed},
		{name: "weak", ifMatch: []string{"W/" + etag}, status: http.StatusPreconditionFailed},
		{name: "quoted star", ifMatch: []string{`"other,*,value"`}, status: http.StatusPreconditionFailed},
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, tc := range tests {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(t.Context(), method, "/api/books/"+bookID+"/chapters/0", nil)
				req.SetPathValue("id", bookID)
				req.SetPathValue("index", "0")
				req.Header.Set("If-None-Match", etag)
				for _, value := range tc.ifMatch {
					req.Header.Add("If-Match", value)
				}
				w := httptest.NewRecorder()
				getChapterHandler(nil)(w, withProfileDeps(req, pd))
				if w.Code != tc.status {
					t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.status, w.Body.String())
				}
				if tc.status == http.StatusNotModified {
					if w.Header().Get("ETag") != etag || w.Body.Len() != 0 {
						t.Fatalf("304 ETag = %q, body = %q", w.Header().Get("ETag"), w.Body.String())
					}
					return
				}
				if got := w.Header().Get("ETag"); got != "" {
					t.Errorf("failed precondition exposed ETag %q", got)
				}
				var body apiError
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Code != "precondition_failed" {
					t.Errorf("error = %#v, want precondition_failed", body)
				}
			})
		}
	}
}

func TestGetChapterHandlerResponses(t *testing.T) {
	t.Parallel()

	book := chapterTestBook(t, "alpha", "hash-alpha")
	pd := newChapterTestDeps(t, book)
	etag := chapterResponseETag(book.FileHash, 0)
	tests := []struct {
		name        string
		ifMatch     []string
		ifNoneMatch []string
		status      int
	}{
		{name: "ordinary", status: http.StatusOK},
		{name: "stale none-match", ifNoneMatch: []string{`"old"`}, status: http.StatusOK},
		{name: "quoted star is not a wildcard", ifNoneMatch: []string{`"other,*,value"`}, status: http.StatusOK},
		{name: "different chapter", ifNoneMatch: []string{chapterResponseETag(book.FileHash, 1)}, status: http.StatusOK},
		{name: "different renderer", ifNoneMatch: []string{`"hash-alpha:0:old-renderer"`}, status: http.StatusOK},
		{name: "weak none-match", ifNoneMatch: []string{"W/" + etag}, status: http.StatusNotModified},
		{name: "later none-match field", ifNoneMatch: []string{`"other,*,value"`, etag}, status: http.StatusNotModified},
		{name: "strong if-match", ifMatch: []string{etag}, status: http.StatusOK},
		{name: "later if-match field", ifMatch: []string{`"old"`, etag}, status: http.StatusOK},
		{name: "wildcard if-match", ifMatch: []string{"*"}, status: http.StatusOK},
		{name: "stale if-match", ifMatch: []string{`"old"`}, status: http.StatusPreconditionFailed},
		{name: "weak if-match", ifMatch: []string{"W/" + etag}, status: http.StatusPreconditionFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := newChapterRequest(t, pd)
			for _, value := range tc.ifMatch {
				req.Header.Add("If-Match", value)
			}
			for _, value := range tc.ifNoneMatch {
				req.Header.Add("If-None-Match", value)
			}
			w := httptest.NewRecorder()
			getChapterHandler(nil)(w, req)
			if tc.status == http.StatusPreconditionFailed {
				assertChapterError(t, w, tc.status, apiError{Code: "precondition_failed", Error: "chapter precondition failed"})
				return
			}
			if w.Code != tc.status || w.Header().Get("ETag") != etag {
				t.Fatalf("status = %d, ETag = %q, body = %s", w.Code, w.Header().Get("ETag"), w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != chapterCacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, chapterCacheControl)
			}
			if tc.status == http.StatusNotModified {
				if w.Body.Len() != 0 {
					t.Fatalf("304 body = %q, want empty", w.Body.String())
				}
				return
			}
			var body epub.ChapterResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.HTML, "alpha") || strings.Contains(body.HTML, "<script") {
				t.Errorf("unexpected rendered HTML: %s", body.HTML)
			}
			if w.Header().Get("Content-Type") != "application/json; charset=utf-8" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("unexpected response headers: %v", w.Header())
			}
		})
	}
}

func TestGetChapterHandlerInvalidRequests(t *testing.T) {
	t.Parallel()

	pd := newChapterTestDeps(t, chapterTestBook(t, "alpha", "hash-alpha"))
	tests := []struct {
		name      string
		index     string
		bookID    string
		noProfile bool
		status    int
		body      apiError
	}{
		{name: "missing profile", index: "0", noProfile: true, status: 500, body: apiError{Code: "server_error", Error: "profile not available"}},
		{name: "missing book", index: "0", bookID: "absent", status: 404, body: apiError{Code: "not_found", Error: "book not found"}},
		{name: "not an integer", index: "abc", status: 400, body: apiError{Code: "invalid", Error: "invalid chapter index"}},
		{name: "integer overflow", index: "999999999999999999999999", status: 400, body: apiError{Code: "invalid", Error: "invalid chapter index"}},
		{name: "negative index", index: "-1", status: 400, body: apiError{Code: "invalid", Error: "chapter index out of range"}},
		{name: "past last chapter", index: "1", status: 400, body: apiError{Code: "invalid", Error: "chapter index out of range"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requestDeps := pd
			if tc.noProfile {
				requestDeps = nil
			}
			req := newChapterRequest(t, requestDeps)
			req.SetPathValue("index", tc.index)
			if tc.bookID != "" {
				req.SetPathValue("id", tc.bookID)
			}
			// Invalid targets must win over either conditional response.
			req.Header.Set("If-Match", `"old"`)
			req.Header.Set("If-None-Match", "*")
			w := httptest.NewRecorder()
			getChapterHandler(nil)(w, req)
			assertChapterError(t, w, tc.status, tc.body)
		})
	}
}

func TestGetChapterHandlerFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		change    func(*storage.BookRecord)
		index     string
		deleteRow bool
		status    int
		body      apiError
	}{
		{name: "invalid spine", change: func(b *storage.BookRecord) { b.SpineJSON = "not-json" }, status: 500, body: apiError{Code: "parse_error", Error: "failed to get spine"}},
		{name: "missing file", change: func(b *storage.BookRecord) { b.FilePath += ".missing" }, status: 500, body: apiError{Code: "process_error", Error: "failed to process chapter"}},
		{name: "missing chapter", change: func(b *storage.BookRecord) { b.SpineJSON = `[{"href":"absent.xhtml"}]` }, status: 500, body: apiError{Code: "process_error", Error: "failed to process chapter"}},
		{name: "short spine", change: func(b *storage.BookRecord) { b.ChapterCount = 2 }, index: "1", status: 400, body: apiError{Code: "invalid", Error: "chapter index out of range"}},
		{name: "row removed after cache snapshot", deleteRow: true, status: 404, body: apiError{Code: "not_found", Error: "book not found"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			book := chapterTestBook(t, "alpha", "hash-alpha")
			if tc.change != nil {
				tc.change(&book)
			}
			pd := newChapterTestDeps(t, book)
			if tc.deleteRow {
				if err := pd.DB.DeleteBookContext(t.Context(), book.ID); err != nil {
					t.Fatal(err)
				}
			}
			req := newChapterRequest(t, pd)
			if tc.index != "" {
				req.SetPathValue("index", tc.index)
			}
			req.Header.Set("If-None-Match", `"old"`)
			w := httptest.NewRecorder()
			getChapterHandler(nil)(w, req)
			assertChapterError(t, w, tc.status, tc.body)
		})
	}
}

func TestGetChapterHandlerUnknownHash(t *testing.T) {
	t.Parallel()

	pd := newChapterTestDeps(t, chapterTestBook(t, "alpha", ""))
	req := newChapterRequest(t, pd)
	req.Header.Set("If-Match", "*")
	req.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	getChapterHandler(nil)(w, req)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != "" {
		t.Fatalf("unknown hash: status = %d, ETag = %q, body = %s", w.Code, w.Header().Get("ETag"), w.Body.String())
	}
}

func TestGetChapterHandlerProfilesAndGenerationLock(t *testing.T) {
	t.Parallel()

	alpha := chapterTestBook(t, "alpha-marker", "hash-alpha")
	beta := chapterTestBook(t, "beta-marker", "hash-beta")
	profiles := []struct {
		book  storage.BookRecord
		pd    *profileDeps
		own   string
		other string
	}{
		{book: alpha, pd: newChapterTestDeps(t, alpha), own: "alpha-marker", other: "beta-marker"},
		{book: beta, pd: newChapterTestDeps(t, beta), own: "beta-marker", other: "alpha-marker"},
	}
	handler := getChapterHandler(nil)
	// Alternate profiles with identical book IDs, then repeat on warm caches.
	for range 2 {
		for _, profile := range profiles {
			w := &chapterGateWriter{ResponseRecorder: httptest.NewRecorder(), t: t, pd: profile.pd}
			handler(w, newChapterRequest(t, profile.pd))
			if w.Code != http.StatusOK || !w.deadlineCleared {
				t.Fatalf("status = %d, deadline cleared = %v; body = %s", w.Code, w.deadlineCleared, w.Body.String())
			}
			var body epub.ChapterResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.HTML, profile.own) || strings.Contains(body.HTML, profile.other) {
				t.Fatalf("profile content mixed: %s", body.HTML)
			}
			if body.ChapterIndex != 0 || body.Direction != "rtl" || body.ResourceBase != "/api/books/chapter-book/resources" {
				t.Errorf("chapter metadata = %#v", body)
			}
			if !strings.Contains(body.HTML, "token="+profile.book.FileHash) {
				t.Errorf("resource token missing from chapter HTML: %s", body.HTML)
			}
			if got := w.Header().Get("ETag"); got != chapterResponseETag(profile.book.FileHash, 0) {
				t.Errorf("profile ETag = %q", got)
			}
			spine, found, err := profile.pd.Books.GetSpine(t.Context(), profile.book.ID)
			if err != nil || !found || len(spine) != 1 || spine[0].Href != "OEBPS/one.xhtml#start" {
				t.Fatalf("shared spine changed: %#v, found = %v, err = %v", spine, found, err)
			}
		}
	}
}

func TestGetChapterHandlerCanceled(t *testing.T) {
	t.Parallel()

	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := newChapterTestDeps(t, chapterTestBook(t, "alpha", "hash-alpha"))
			if warm {
				w := httptest.NewRecorder()
				getChapterHandler(nil)(w, newChapterRequest(t, pd))
				if w.Code != http.StatusOK {
					t.Fatalf("warm chapter: status = %d; body = %s", w.Code, w.Body.String())
				}
			}
			req := newChapterRequest(t, pd)
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			w := &chapterGateWriter{ResponseRecorder: httptest.NewRecorder(), t: t, pd: pd}
			getChapterHandler(nil)(w, req.WithContext(ctx))
			if w.wroteResponse || w.Header().Get("ETag") != "" {
				t.Fatalf("canceled request wrote a response: headers = %v, body = %s", w.Header(), w.Body.String())
			}
		})
	}
}

func TestGetChapterRouteRequiresSession(t *testing.T) {
	t.Parallel()

	deps := &Dependencies{sessions: newSessionStore(nil)}
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, cookie := range []string{"", "unknown-session"} {
			t.Run(method+"/"+cookie, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(t.Context(), method, "/api/books/chapter-book/chapters/0", nil)
				req.Header.Set("If-None-Match", "*")
				if cookie != "" {
					req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, withProfileDeps(req, &profileDeps{}))
				assertChapterError(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
			})
		}
	}
}

func newChapterTestDeps(t *testing.T, book storage.BookRecord) *profileDeps {
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
	if _, err := db.InsertBookContext(t.Context(), book); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}
	pd := &profileDeps{DB: db, Books: books, Store: epub.NewStore(1), refs: 1}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	t.Cleanup(func() {
		pd.lifetimeMu.Lock()
		refs := pd.refs
		pd.lifetimeMu.Unlock()
		if refs != 1 {
			t.Errorf("handler changed middleware-owned profile reference: refs = %d", refs)
		}
		if pd.bookReplaceMu.TryLock() {
			pd.bookReplaceMu.Unlock()
		} else {
			t.Error("handler retained the replacement read lock")
		}
		pd.Store.Close()
	})
	return pd
}

func chapterTestBook(t *testing.T, marker, fileHash string) storage.BookRecord {
	t.Helper()

	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	entry, err := zw.Create("OEBPS/one.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	html := `<html><body><p>` + marker + `</p><img src="pic.png"/><script>bad()</script></body></html>`
	if _, err := io.WriteString(entry, html); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(filePath, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return storage.BookRecord{
		ID:           "chapter-book",
		Title:        marker,
		FilePath:     filePath,
		FileHash:     fileHash,
		FileSize:     int64(data.Len()),
		Direction:    "rtl",
		ChapterCount: 1,
		SpineJSON:    `[{"id":"one","href":"OEBPS/one.xhtml#start","mediaType":"application/xhtml+xml","linear":true}]`,
		TocJSON:      "[]",
	}
}

func newChapterRequest(t *testing.T, pd *profileDeps) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/books/chapter-book/chapters/0", nil)
	req.SetPathValue("id", "chapter-book")
	req.SetPathValue("index", "0")
	return withProfileDeps(req, pd)
}

func assertChapterError(t *testing.T, w *httptest.ResponseRecorder, status int, want apiError) {
	t.Helper()

	if w.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, status, w.Body.String())
	}
	var got apiError
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("error = %#v, want %#v", got, want)
	}
	if w.Header().Get("ETag") != "" {
		t.Errorf("error exposed a success validator: %v", w.Header())
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("unexpected error headers: %v", w.Header())
	}
}

type chapterGateWriter struct {
	*httptest.ResponseRecorder
	t               *testing.T
	pd              *profileDeps
	deadlineCleared bool
	wroteResponse   bool
}

func (w *chapterGateWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlineCleared = deadline.IsZero()
	return nil
}

func (w *chapterGateWriter) checkGate() {
	w.t.Helper()
	w.wroteResponse = true
	if w.pd.bookReplaceMu.TryLock() {
		w.pd.bookReplaceMu.Unlock()
		w.t.Error("replacement gate released before chapter response completed")
	}
}

func (w *chapterGateWriter) WriteHeader(status int) {
	w.checkGate()
	w.ResponseRecorder.WriteHeader(status)
}

func (w *chapterGateWriter) Write(body []byte) (int, error) {
	w.checkGate()
	return w.ResponseRecorder.Write(body)
}
