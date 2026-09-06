package api

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/storage"
)

const downloadTestBookID = "download-book"

func newDownloadTestDeps(t *testing.T, content []byte, fileHash string) *profileDeps {
	t.Helper()

	filePath := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write book file: %v", err)
	}

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
		ID:           downloadTestBookID,
		Title:        "كتاب",
		FilePath:     filePath,
		FileHash:     fileHash,
		FileSize:     int64(len(content)),
		Direction:    "rtl",
		ChapterCount: 0,
		SpineJSON:    "[]",
		TocJSON:      "[]",
	}
	if _, err := db.InsertBookContext(t.Context(), book); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}
	// The middleware owns this reference; the download handler only borrows it.
	pd := &profileDeps{DB: db, Books: books, refs: 1}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	t.Cleanup(func() {
		pd.lifetimeMu.Lock()
		defer pd.lifetimeMu.Unlock()
		if pd.refs != 1 {
			t.Errorf("profile refs = %d, want middleware-owned reference intact", pd.refs)
		}
		if pd.bookReplaceMu.TryLock() {
			pd.bookReplaceMu.Unlock()
		} else {
			t.Error("replacement read lock remained held after download")
		}
	})
	return pd
}

func newDownloadRequest(pd *profileDeps) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/books/download-book/file", nil)
	req.SetPathValue("id", downloadTestBookID)
	return withProfileDeps(req, pd)
}

func TestDownloadResponseETag(t *testing.T) {
	t.Parallel()

	if got := downloadResponseETag(""); got != "" {
		t.Fatalf("empty hash ETag = %q, want empty", got)
	}
	if got := downloadResponseETag("abc"); got != `"abc:file"` {
		t.Fatalf("ETag = %q, want %q", got, `"abc:file"`)
	}
	if downloadResponseETag("abc") == downloadResponseETag("def") {
		t.Fatal("different file hashes produced the same ETag")
	}
}

func TestDownloadBookHandlerValidators(t *testing.T) {
	t.Parallel()

	const fileHash = "current-hash"
	tests := []struct {
		name       string
		validator  string
		wantStatus int
		wantBody   bool
	}{
		{
			name:       "strong match",
			validator:  downloadResponseETag(fileHash),
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "weak match",
			validator:  "W/" + downloadResponseETag(fileHash),
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "different hash",
			validator:  downloadResponseETag("previous-hash"),
			wantStatus: http.StatusOK,
			wantBody:   true,
		},
		{
			name:       "wildcard",
			validator:  "*",
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "list",
			validator:  `"old-hash:file", W/"current-hash:file"`,
			wantStatus: http.StatusNotModified,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const content = "epub-content"
			pd := newDownloadTestDeps(t, []byte(content), fileHash)
			req := newDownloadRequest(pd)
			req.Header.Set("If-None-Match", tc.validator)
			recorder := httptest.NewRecorder()

			downloadBookHandler(nil)(recorder, req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if got := recorder.Header().Get("ETag"); got != downloadResponseETag(fileHash) {
				t.Errorf("ETag = %q, want %q", got, downloadResponseETag(fileHash))
			}
			if got := recorder.Header().Get("Last-Modified"); got != "" {
				t.Errorf("Last-Modified = %q, want empty", got)
			}
			if tc.wantBody && recorder.Body.String() != content {
				t.Errorf("body = %q, want %q", recorder.Body.String(), content)
			}
			if !tc.wantBody && recorder.Body.Len() != 0 {
				t.Errorf("body = %q, want empty", recorder.Body.String())
			}
		})
	}
}

func TestDownloadBookHandlerPreconditionOrder(t *testing.T) {
	t.Parallel()

	const fileHash = "current-hash"
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name    string
				ifMatch string
			}{
				{name: "stale", ifMatch: downloadResponseETag("old-hash")},
				{name: "weak", ifMatch: "W/" + downloadResponseETag(fileHash)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					pd := newDownloadTestDeps(t, []byte("epub-content"), fileHash)
					req := newDownloadRequest(pd)
					req.Method = method
					req.Header.Set("If-Match", tc.ifMatch)
					req.Header.Set("If-None-Match", downloadResponseETag(fileHash))
					recorder := httptest.NewRecorder()

					downloadBookHandler(nil)(recorder, req)

					// A cache hit cannot override a failed If-Match precondition,
					// even when both validators name the same current representation.
					if recorder.Code != http.StatusPreconditionFailed {
						t.Fatalf("status = %d, want %d; body = %s",
							recorder.Code, http.StatusPreconditionFailed, recorder.Body.String())
					}
					if recorder.Body.Len() != 0 {
						t.Errorf("body = %q, want empty", recorder.Body.String())
					}
				})
			}
		})
	}
}

func TestDownloadBookHandlerRepeatedValidators(t *testing.T) {
	t.Parallel()

	const current = `"current-hash:file"`
	const stale = `"old-hash:file"`
	for _, tc := range []struct {
		name       string
		headers    http.Header
		wantStatus int
	}{
		{
			name:       "if-none-match later field",
			headers:    http.Header{"If-None-Match": {stale, "W/" + current}},
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "if-match later field",
			headers:    http.Header{"If-Match": {stale, current}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "both lists match",
			headers:    http.Header{"If-Match": {stale, current}, "If-None-Match": {stale, current}},
			wantStatus: http.StatusNotModified,
		},
		{
			name:       "failed if-match still wins",
			headers:    http.Header{"If-Match": {stale, "W/" + current}, "If-None-Match": {stale, current}},
			wantStatus: http.StatusPreconditionFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := newDownloadTestDeps(t, []byte("epub-content"), "current-hash")
			req := newDownloadRequest(pd)
			req.Header = tc.headers.Clone()
			w := httptest.NewRecorder()
			downloadBookHandler(nil)(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			wantBody := ""
			if tc.wantStatus == http.StatusOK {
				wantBody = "epub-content"
			}
			if got := w.Body.String(); got != wantBody {
				t.Errorf("body = %q, want %q", got, wantBody)
			}
			for key, values := range tc.headers {
				if !slices.Equal(req.Header.Values(key), values) {
					t.Errorf("caller header %s changed: %q", key, req.Header.Values(key))
				}
			}
		})
	}
}

func TestDownloadBookHandlerTransfers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		method      string
		rangeHeader string
		ifRange     string
		wantStatus  int
		wantBody    string
		wantRange   string
		wantLength  string
	}{
		{name: "get", method: http.MethodGet, wantStatus: http.StatusOK, wantBody: "abcdef", wantLength: "6"},
		{name: "head", method: http.MethodHead, wantStatus: http.StatusOK, wantLength: "6"},
		{
			name: "matching resume", method: http.MethodGet, rangeHeader: "bytes=1-3", ifRange: `"range-hash:file"`,
			wantStatus: http.StatusPartialContent, wantBody: "bcd", wantRange: "bytes 1-3/6", wantLength: "3",
		},
		{
			name: "stale resume", method: http.MethodGet, rangeHeader: "bytes=1-3", ifRange: `"old-hash:file"`,
			wantStatus: http.StatusOK, wantBody: "abcdef", wantLength: "6",
		},
		{
			name: "weak resume", method: http.MethodGet, rangeHeader: "bytes=1-3", ifRange: `W/"range-hash:file"`,
			wantStatus: http.StatusOK, wantBody: "abcdef", wantLength: "6",
		},
		{
			name: "date cannot validate rewrite", method: http.MethodGet, rangeHeader: "bytes=1-3",
			ifRange: "Wed, 21 Oct 2099 07:28:00 GMT", wantStatus: http.StatusOK, wantBody: "abcdef", wantLength: "6",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := newDownloadTestDeps(t, []byte("abcdef"), "range-hash")
			req := newDownloadRequest(pd)
			req.Method = tc.method
			req.Header.Set("Range", tc.rangeHeader)
			req.Header.Set("If-Range", tc.ifRange)
			// File mtime must never validate an in-app same-second rewrite.
			req.Header.Set("If-Modified-Since", "Wed, 21 Oct 2099 07:28:00 GMT")
			w := httptest.NewRecorder()
			downloadBookHandler(nil)(w, req)
			if w.Code != tc.wantStatus || w.Body.String() != tc.wantBody {
				t.Fatalf("response = %d %q, want %d %q", w.Code, w.Body.String(), tc.wantStatus, tc.wantBody)
			}
			for key, want := range map[string]string{
				"Content-Type": "application/epub+zip", "X-Content-Type-Options": "nosniff",
				"Cache-Control": "private, no-cache", "ETag": `"range-hash:file"`,
				"Content-Range": tc.wantRange, "Content-Length": tc.wantLength,
				"Accept-Ranges": "bytes", "Last-Modified": "",
			} {
				if got := w.Header().Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			kind, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if err != nil || kind != "attachment" || params["filename"] != "كتاب.epub" {
				t.Errorf("Content-Disposition = %q; parse error = %v", w.Header().Get("Content-Disposition"), err)
			}
		})
	}
}

func TestDownloadBookHandlerErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		wantStatus int
		want       apiError
	}{
		{name: "missing profile", wantStatus: http.StatusInternalServerError, want: apiError{Code: "server_error", Error: "profile not available"}},
		{name: "missing book", wantStatus: http.StatusNotFound, want: apiError{Code: "not_found", Error: "book not found"}},
		{name: "empty path", wantStatus: http.StatusNotFound, want: apiError{Code: "no_file", Error: "book has no file on disk"}},
		{name: "missing file", wantStatus: http.StatusNotFound, want: apiError{Code: "no_file", Error: "book file not found"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := newDownloadTestDeps(t, []byte("private-content"), "error-hash")
			req := newDownloadRequest(pd)
			switch tc.name {
			case "missing profile":
				req = withProfileDeps(req, nil)
			case "missing book":
				req.SetPathValue("id", "unknown")
			case "empty path":
				book, _ := pd.Books.Get(downloadTestBookID)
				book.FilePath = ""
				pd.Books.Add(book)
			case "missing file":
				book, _ := pd.Books.Get(downloadTestBookID)
				if err := os.Remove(book.FilePath); err != nil {
					t.Fatal(err)
				}
			}
			// Normal lookup errors must take precedence over cache validators.
			req.Header.Set("If-None-Match", `"error-hash:file"`)
			w := httptest.NewRecorder()
			downloadBookHandler(nil)(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			var got apiError
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got != tc.want {
				t.Errorf("error = %s, want %+v; decode error = %v", w.Body.String(), tc.want, err)
			}
			if w.Header().Get("ETag") != "" || w.Header().Get("Content-Disposition") != "" {
				t.Errorf("download headers leaked into lookup error: %v", w.Header())
			}
		})
	}
}

func TestDownloadBookHandlerProfileIsolation(t *testing.T) {
	t.Parallel()

	profiles := []struct {
		content string
		pd      *profileDeps
	}{
		{content: "first-profile", pd: newDownloadTestDeps(t, []byte("first-profile"), "first-profile")},
		{content: "second-profile", pd: newDownloadTestDeps(t, []byte("second-profile"), "second-profile")},
	}
	handler := downloadBookHandler(nil)
	for _, profile := range profiles {
		content := profile.content
		w := httptest.NewRecorder()
		handler(w, newDownloadRequest(profile.pd))
		if w.Code != http.StatusOK || w.Body.String() != content {
			t.Errorf("profile %s response = %d %q", content, w.Code, w.Body.String())
		}
		if got := w.Header().Get("ETag"); got != `"`+content+`:file"` {
			t.Errorf("profile %s ETag = %q", content, got)
		}
	}
}

func TestDownloadRouteRequiresSession(t *testing.T) {
	t.Parallel()

	deps := &Dependencies{sessions: newSessionStore(nil)}
	handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, cookie := range []string{"", "unknown"} {
			t.Run(method+"/cookie="+cookie, func(t *testing.T) {
				req := httptest.NewRequestWithContext(t.Context(), method, "/api/books/book/file", nil)
				if cookie != "" {
					req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
				}
				req.Header.Set("If-None-Match", "*")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, withProfileDeps(req, &profileDeps{}))
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
				}
				if w.Header().Get("ETag") != "" || w.Header().Get("Content-Disposition") != "" {
					t.Errorf("download headers exposed without a session: %v", w.Header())
				}
			})
		}
	}
}

func TestDownloadContentDispositionAttachment(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Book.epub", "كتاب", "كتاب + café.epub", "a\"b\\c\r\n\t\x7f.epub"} {
		t.Run(name, func(t *testing.T) {
			got := contentDispositionAttachment(name)
			for _, b := range []byte(got) {
				if b < 0x20 || b >= 0x7f {
					t.Fatalf("non-ASCII or control byte in header %q", got)
				}
			}
			kind, params, err := mime.ParseMediaType(got)
			if err != nil || kind != "attachment" || params["filename"] != name {
				t.Errorf("header = %q, decoded = %q; error = %v", got, params["filename"], err)
			}
			fallback, _, _ := strings.Cut(got, "; filename*=")
			_, params, err = mime.ParseMediaType(fallback)
			if err != nil || params["filename"] == "" || strings.ContainsAny(params["filename"], "\"\\\r\n\t\x7f") {
				t.Errorf("unsafe ASCII fallback %q; error = %v", fallback, err)
			}
		})
	}
}

func TestDownloadRFC5987AttrChar(t *testing.T) {
	t.Parallel()

	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$&+-.^_`|~"
	for value := range 256 {
		want := strings.IndexByte(allowed, byte(value)) >= 0
		if got := isRFC5987AttrChar(byte(value)); got != want {
			t.Errorf("isRFC5987AttrChar(%#x) = %t, want %t", value, got, want)
		}
	}
}

func TestDownloadBookHandlerRange(t *testing.T) {
	t.Parallel()

	pd := newDownloadTestDeps(t, []byte("abcdef"), "range-hash")
	req := newDownloadRequest(pd)
	req.Header.Set("Range", "bytes=1-3")
	recorder := httptest.NewRecorder()

	downloadBookHandler(nil)(recorder, req)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusPartialContent, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != "bcd" {
		t.Errorf("body = %q, want %q", got, "bcd")
	}
	if got := recorder.Header().Get("Content-Range"); got != "bytes 1-3/6" {
		t.Errorf("Content-Range = %q, want %q", got, "bytes 1-3/6")
	}
}

type blockingDownloadResponseWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingDownloadResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingDownloadResponseWriter) WriteHeader(_ int) {}

func (w *blockingDownloadResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func TestDownloadBookHandlerHoldsReplacementReadLock(t *testing.T) {
	t.Parallel()

	pd := newDownloadTestDeps(t, []byte("epub-content"), "lock-hash")
	req := newDownloadRequest(pd)
	writer := &blockingDownloadResponseWriter{
		header:  make(http.Header),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		downloadBookHandler(nil)(writer, req)
		close(done)
	}()

	unblock := sync.OnceFunc(func() { close(writer.release) })
	defer func() {
		unblock()
		waitDownloadSignal(t, done)
	}()

	waitDownloadSignal(t, writer.started)
	if pd.bookReplaceMu.TryLock() {
		pd.bookReplaceMu.Unlock()
		t.Fatal("replacement write lock acquired during active download")
	}

	unblock()
	waitDownloadSignal(t, done)
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("replacement write lock remained held after download completed")
	}
	pd.bookReplaceMu.Unlock()
}

func waitDownloadSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatal("timed out waiting for download goroutine")
	}
}
