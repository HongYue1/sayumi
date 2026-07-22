package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

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
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test DB: %v", err)
		}
	})

	const (
		bookID   = "book-1"
		fileHash = "hash-1"
	)
	book := storage.BookRecord{
		BookSummary: storage.BookSummary{
			ID:           bookID,
			Title:        "Book",
			FilePath:     "/missing/book.epub",
			FileHash:     fileHash,
			FileSize:     1,
			Direction:    "ltr",
			ChapterCount: 1,
		},
		// A spine load would fail parsing and turn this request into a 500.
		// The matching validator must return before touching this value.
		SpineJSON: "not-json",
		TocJSON:   "[]",
	}
	if _, err := db.InsertBookContext(t.Context(), book); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}
	pd := &profileDeps{
		DB:    db,
		Books: books,
		Store: epub.NewStore(1),
	}

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
}
