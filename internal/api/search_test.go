package api

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

const searchTestBookID = "search-book"

func TestParseSearchRequestParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rawQuery  string
		wantQuery string
		wantLimit int
	}{
		{name: "defaults", rawQuery: "q=%20alpha%20", wantQuery: "alpha", wantLimit: 200},
		{name: "valid limit", rawQuery: "q=alpha&limit=25", wantQuery: "alpha", wantLimit: 25},
		{name: "zero limit", rawQuery: "q=alpha&limit=0", wantQuery: "alpha", wantLimit: 200},
		{name: "oversized limit", rawQuery: "q=alpha&limit=201", wantQuery: "alpha", wantLimit: 200},
		{name: "invalid limit", rawQuery: "q=alpha&limit=nope", wantQuery: "alpha", wantLimit: 200},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/?"+tc.rawQuery+"&cursor=next", nil)
			got := parseSearchRequestParams(req)
			if got.query != tc.wantQuery {
				t.Errorf("query = %q, want %q", got.query, tc.wantQuery)
			}
			if got.cursor != "next" {
				t.Errorf("cursor = %q, want next", got.cursor)
			}
			if got.limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", got.limit, tc.wantLimit)
			}
		})
	}
}

func parseSearchRequestParamsLegacy(r *http.Request) searchRequestParams {
	params := searchRequestParams{
		query:  strings.TrimSpace(r.URL.Query().Get("q")),
		cursor: r.URL.Query().Get("cursor"),
		limit:  200,
	}
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsedLimit, err := strconv.Atoi(rawLimit); err == nil && parsedLimit > 0 && parsedLimit <= 200 {
			params.limit = parsedLimit
		}
	}
	return params
}

func BenchmarkSearchRequestParams(b *testing.B) {
	req := httptest.NewRequest(
		http.MethodGet,
		"/?q=the+quick+brown+fox&cursor=eyJjIjozLCJvIjo0Mn0&limit=100",
		nil,
	)

	bench := func(b *testing.B, parse func(*http.Request) searchRequestParams) {
		b.Helper()
		b.ReportAllocs()
		var got searchRequestParams
		for b.Loop() {
			got = parse(req)
		}
		if got.query != "the quick brown fox" || got.cursor == "" || got.limit != 100 {
			b.Fatalf("unexpected parsed params: %+v", got)
		}
	}

	b.Run("before_reparse_three_times", func(b *testing.B) {
		bench(b, parseSearchRequestParamsLegacy)
	})
	b.Run("after_parse_once", func(b *testing.B) {
		bench(b, parseSearchRequestParams)
	})
}

func newSearchTestDeps(t *testing.T) *profileDeps {
	t.Helper()

	filePath := writeSearchTestEPUB(t)
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
			ID:           searchTestBookID,
			Title:        "Search Book",
			FilePath:     filePath,
			FileHash:     "search-hash",
			FileSize:     1,
			Direction:    "ltr",
			ChapterCount: 1,
		},
		SpineJSON: `[{"href":"chapter.xhtml","id":"chapter","linear":true}]`,
		TocJSON:   "[]",
	}
	if _, err := db.InsertBookContext(t.Context(), book); err != nil {
		t.Fatalf("insert book: %v", err)
	}
	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}
	store := epub.NewStore(1)
	t.Cleanup(store.Close)
	return &profileDeps{DB: db, Books: books, Store: store}
}

func writeSearchTestEPUB(t *testing.T) string {
	t.Helper()

	filePath := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create EPUB: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close EPUB file: %v", err)
		}
	}()
	zw := zip.NewWriter(f)
	chapter, err := zw.Create("chapter.xhtml")
	if err != nil {
		t.Fatalf("create chapter: %v", err)
	}
	if _, err := chapter.Write([]byte(`<html><body><p>alpha beta</p></body></html>`)); err != nil {
		t.Fatalf("write chapter: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close EPUB zip: %v", err)
	}
	return filePath
}

func newSearchRequest(pd *profileDeps) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/books/search-book/search?q=alpha", nil)
	req.SetPathValue("id", searchTestBookID)
	return withProfileDeps(req, pd)
}

func TestSearchHandlerReturnsResults(t *testing.T) {
	t.Parallel()

	pd := newSearchTestDeps(t)
	recorder := httptest.NewRecorder()
	searchHandler(nil)(recorder, newSearchRequest(pd))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response epub.SearchResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].Snippet != "alpha beta" {
		t.Fatalf("results = %+v, want one alpha beta match", response.Results)
	}
}

type blockingSearchResponseWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingSearchResponseWriter) Header() http.Header {
	return w.header
}

func (w *blockingSearchResponseWriter) WriteHeader(_ int) {}

func (w *blockingSearchResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func TestSearchHandlerHoldsReplacementReadLock(t *testing.T) {
	t.Parallel()

	pd := newSearchTestDeps(t)
	writer := &blockingSearchResponseWriter{
		header:  make(http.Header),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		searchHandler(nil)(writer, newSearchRequest(pd))
		close(done)
	}()

	<-writer.started
	if pd.bookReplaceMu.TryLock() {
		pd.bookReplaceMu.Unlock()
		close(writer.release)
		<-done
		t.Fatal("replacement write lock acquired during active search response")
	}

	close(writer.release)
	<-done
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("replacement write lock remained held after search completed")
	}
	pd.bookReplaceMu.Unlock()
}
