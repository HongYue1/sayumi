package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
		BookSummary: storage.BookSummary{
			ID:           downloadTestBookID,
			Title:        "كتاب",
			FilePath:     filePath,
			FileHash:     fileHash,
			FileSize:     int64(len(content)),
			Direction:    "rtl",
			ChapterCount: 0,
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
	return &profileDeps{DB: db, Books: books}
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

	<-writer.started
	if pd.bookReplaceMu.TryLock() {
		pd.bookReplaceMu.Unlock()
		close(writer.release)
		<-done
		t.Fatal("replacement write lock acquired during active download")
	}

	close(writer.release)
	<-done
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("replacement write lock remained held after download completed")
	}
	pd.bookReplaceMu.Unlock()
}
