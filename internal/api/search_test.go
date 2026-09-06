package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/api/middleware"
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
		{name: "missing query", rawQuery: "", wantLimit: 200},
		{name: "Unicode whitespace", rawQuery: "q=%E3%80%80alpha%C2%A0", wantQuery: "alpha", wantLimit: 200},
		{name: "whitespace only", rawQuery: "q=%E3%80%80%C2%A0", wantLimit: 200},
		{name: "minimum limit", rawQuery: "q=alpha&limit=1", wantQuery: "alpha", wantLimit: 1},
		{name: "maximum limit", rawQuery: "q=alpha&limit=200", wantQuery: "alpha", wantLimit: 200},
		{name: "negative limit", rawQuery: "q=alpha&limit=-1", wantQuery: "alpha", wantLimit: 200},
		{name: "overflowing limit", rawQuery: "q=alpha&limit=999999999999999999999999", wantQuery: "alpha", wantLimit: 200},
		{name: "first repeated values", rawQuery: "q=alpha&q=beta&limit=1&limit=2", wantQuery: "alpha", wantLimit: 1},
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

func newSearchTestDeps(t *testing.T, book storage.BookRecord) *profileDeps {
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

func searchTestBook(t *testing.T, texts ...string) storage.BookRecord {
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
	spine := make([]epub.SpineEntry, 0, len(texts))
	for i, text := range texts {
		name := "chapter" + strconv.Itoa(i) + ".xhtml"
		chapter, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create chapter: %v", err)
		}
		if _, err := chapter.Write([]byte(`<html><body><p>` + text + `</p></body></html>`)); err != nil {
			t.Fatalf("write chapter: %v", err)
		}
		spine = append(spine, epub.SpineEntry{Href: name + "#start", ID: strconv.Itoa(i), Linear: true})
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close EPUB zip: %v", err)
	}
	spineJSON, err := json.Marshal(spine)
	if err != nil {
		t.Fatalf("encode spine: %v", err)
	}
	return storage.BookRecord{
		ID:           searchTestBookID,
		Title:        "Search Book",
		FilePath:     filePath,
		FileHash:     "search-hash",
		FileSize:     1,
		Direction:    "ltr",
		ChapterCount: len(texts),
		SpineJSON:    string(spineJSON),
		TocJSON:      "[]",
	}
}

func newSearchRequest(t *testing.T, pd *profileDeps) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/books/search-book/search?q=alpha", nil)
	req.SetPathValue("id", searchTestBookID)
	return withProfileDeps(req, pd)
}

func TestSearchHandlerReturnsResults(t *testing.T) {
	t.Parallel()

	pd := newSearchTestDeps(t, searchTestBook(t, "alpha beta"))
	recorder := httptest.NewRecorder()
	searchHandler(nil)(recorder, newSearchRequest(t, pd))

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

	pd := newSearchTestDeps(t, searchTestBook(t, "alpha beta"))
	writer := &blockingSearchResponseWriter{
		header:  make(http.Header),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	req := newSearchRequest(t, pd)
	done := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(writer.release) })
	t.Cleanup(func() {
		unblock()
		waitSearchSignal(t, done, "search completion")
	})
	go func() {
		defer close(done)
		searchHandler(nil)(writer, req)
	}()

	waitSearchSignal(t, writer.started, "search response")
	if pd.bookReplaceMu.TryLock() {
		pd.bookReplaceMu.Unlock()
		t.Fatal("replacement write lock acquired during active search response")
	}

	unblock()
	waitSearchSignal(t, done, "search completion")
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("replacement write lock remained held after search completed")
	}
	pd.bookReplaceMu.Unlock()
}

type searchDeadlineResponseWriter struct {
	*httptest.ResponseRecorder
	cleared       chan struct{}
	once          sync.Once
	wroteResponse bool
}

func (w *searchDeadlineResponseWriter) WriteHeader(status int) {
	w.wroteResponse = true
	w.ResponseRecorder.WriteHeader(status)
}

func (w *searchDeadlineResponseWriter) Write(p []byte) (int, error) {
	w.wroteResponse = true
	return w.ResponseRecorder.Write(p)
}

func (w *searchDeadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		w.once.Do(func() { close(w.cleared) })
	}
	return nil
}

func waitSearchSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestSearchHandlerClearsDeadlineBeforeReplacementWait(t *testing.T) {
	t.Parallel()

	for _, useGzip := range []bool{false, true} {
		t.Run("gzip="+strconv.FormatBool(useGzip), func(t *testing.T) {
			t.Parallel()

			pd := newSearchTestDeps(t, searchTestBook(t, "alpha beta"))
			req := newSearchRequest(t, pd)
			if profileDepsFromCtx(req) != pd {
				t.Fatal("search request lost its profile context")
			}
			var handler http.Handler = searchHandler(nil)
			if useGzip {
				handler = middleware.Gzip(handler)
				req.Header.Set("Accept-Encoding", "gzip")
			}
			pd.bookReplaceMu.Lock()
			unlock := sync.OnceFunc(pd.bookReplaceMu.Unlock)
			writer := &searchDeadlineResponseWriter{
				ResponseRecorder: httptest.NewRecorder(),
				cleared:          make(chan struct{}),
			}
			done := make(chan struct{})
			t.Cleanup(func() {
				unlock()
				waitSearchSignal(t, done, "search completion")
			})
			go func() {
				defer close(done)
				handler.ServeHTTP(writer, req)
			}()

			// The server arms WriteTimeout before dispatch. Clearing it after
			// replacement waits is too late; gzip must forward the controller too.
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-writer.cleared:
			case <-done:
				t.Fatalf("search returned before clearing its deadline: status = %d; body = %s", writer.Code, writer.Body.String())
			case <-timer.C:
				t.Fatal("write deadline was not cleared before waiting for replacement")
			}
			unlock()
			waitSearchSignal(t, done, "search completion")
			if !writer.wroteResponse || writer.Code != http.StatusOK {
				t.Fatalf("status = %d, want a written 200 response; body = %s", writer.Code, writer.Body.String())
			}
		})
	}
}

func TestParseSearchRequestParamsOpaqueCursor(t *testing.T) {
	t.Parallel()

	const cursor = " +/=😀 "
	values := url.Values{"q": {" alpha "}, "cursor": {cursor, "ignored"}}
	req := httptest.NewRequest(http.MethodGet, "/?"+values.Encode(), nil)
	want := searchRequestParams{query: "alpha", cursor: cursor, limit: 200}
	if got := parseSearchRequestParams(req); got != want {
		t.Fatalf("params = %+v, want %+v", got, want)
	}
}

func TestSearchHandlerPaginationAndUnicode(t *testing.T) {
	t.Parallel()

	pd := newSearchTestDeps(t, searchTestBook(t, "😀 İ ALPHA alpha", "beta alpha"))
	spine, ok, err := pd.Books.GetSpine(t.Context(), searchTestBookID)
	if err != nil || !ok {
		t.Fatalf("load shared spine: found = %v, err = %v", ok, err)
	}
	before := slices.Clone(spine)
	want := []epub.SearchResult{
		{ChapterIndex: 0, CharOffset: 4, MatchLen: 5, Snippet: "😀 İ ALPHA alpha", SnippetStart: 4, SnippetLen: 5},
		{ChapterIndex: 0, CharOffset: 10, MatchLen: 5, Snippet: "😀 İ ALPHA alpha", SnippetStart: 10, SnippetLen: 5},
		{ChapterIndex: 1, CharOffset: 5, MatchLen: 5, Snippet: "beta alpha", SnippetStart: 5, SnippetLen: 5},
	}
	cursor := ""
	for i, match := range want {
		req := newSearchRequest(t, pd)
		req.URL.RawQuery = url.Values{"q": {" alpha "}, "limit": {"1"}, "cursor": {cursor}}.Encode()
		w := httptest.NewRecorder()
		searchHandler(nil)(w, req)
		got := readSearchResponse(t, w)
		if len(got.Results) != 1 || got.Results[0] != match {
			t.Fatalf("page %d results = %+v, want %+v", i, got.Results, match)
		}
		if got.HasMore != (i < len(want)-1) {
			t.Fatalf("page %d hasMore = %v", i, got.HasMore)
		}
		if got.HasMore && (got.NextCursor == "" || got.NextCursor == cursor) {
			t.Fatalf("page %d did not advance its cursor", i)
		}
		cursor = got.NextCursor // Opaque: return it to the API without decoding it.
	}

	// Folding İ must not insert a second code point or shift later matches.
	req := newSearchRequest(t, pd)
	req.URL.RawQuery = "q=i"
	w := httptest.NewRecorder()
	searchHandler(nil)(w, req)
	got := readSearchResponse(t, w)
	folded := epub.SearchResult{ChapterIndex: 0, CharOffset: 2, MatchLen: 1, Snippet: "😀 İ ALPHA alpha", SnippetStart: 2, SnippetLen: 1}
	if len(got.Results) != 1 || got.Results[0] != folded || got.HasMore {
		t.Fatalf("folded results = %+v, want %+v", got, folded)
	}
	if !slices.Equal(spine, before) {
		t.Fatalf("search mutated shared spine: got %+v, want %+v", spine, before)
	}
}

func TestSearchHandlerEmptyQuerySkipsBookIO(t *testing.T) {
	t.Parallel()

	book := searchTestBook(t, "alpha")
	book.SpineJSON = "not JSON"
	book.FilePath = filepath.Join(t.TempDir(), "missing.epub")
	pd := newSearchTestDeps(t, book)
	req := newSearchRequest(t, pd)
	req.URL.RawQuery = url.Values{"q": {" \t\u3000\u00a0 "}}.Encode()
	w := httptest.NewRecorder()
	pd.bookReplaceMu.Lock()
	unlock := sync.OnceFunc(pd.bookReplaceMu.Unlock)
	done := make(chan struct{})
	t.Cleanup(func() {
		unlock()
		waitSearchSignal(t, done, "empty-query completion")
	})
	go func() {
		defer close(done)
		searchHandler(nil)(w, req)
	}()
	// This summary-only fast path must not load the invalid spine, open the
	// missing archive, or wait for a file-generation replacement.
	waitSearchSignal(t, done, "empty-query response during replacement")
	unlock()
	got := readSearchResponse(t, w)
	if len(got.Results) != 0 || got.HasMore {
		t.Fatalf("empty query returned %+v", got)
	}
}

func TestSearchHandlerResultEdgeCases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		rawQuery  string
		spineJSON string
		matches   int
	}{
		{name: "no matches", rawQuery: "q=absent"},
		{name: "bad cursor starts from beginning", rawQuery: "q=alpha&cursor=not-a-cursor", matches: 1},
		{name: "missing chapter is skipped", rawQuery: "q=alpha", spineJSON: `[{"href":"missing.xhtml","id":"missing","linear":true}]`},
		{name: "null spine is empty", rawQuery: "q=alpha", spineJSON: "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			book := searchTestBook(t, "alpha beta")
			if tc.spineJSON != "" {
				book.SpineJSON = tc.spineJSON
			}
			pd := newSearchTestDeps(t, book)
			req := newSearchRequest(t, pd)
			req.URL.RawQuery = tc.rawQuery
			w := httptest.NewRecorder()
			searchHandler(nil)(w, req)
			got := readSearchResponse(t, w)
			if len(got.Results) != tc.matches || got.HasMore {
				t.Fatalf("response = %+v, want %d terminal results", got, tc.matches)
			}
		})
	}
}

func TestSearchHandlerErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		want   apiError
	}{
		{"missing profile", http.StatusInternalServerError, apiError{Code: "server_error", Error: "profile not available"}},
		{"missing book", http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"}},
		{"empty query missing book", http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"}},
		{"deleted row", http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"}},
		{"invalid spine", http.StatusInternalServerError, apiError{Code: "parse_error", Error: "failed to get spine"}},
		{"missing archive", http.StatusInternalServerError, apiError{Code: "search_error", Error: "search failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			book := searchTestBook(t, "alpha beta")
			switch tc.name {
			case "invalid spine":
				book.SpineJSON = "not JSON"
			case "missing archive":
				book.FilePath = filepath.Join(t.TempDir(), "missing.epub")
			}
			pd := newSearchTestDeps(t, book)
			req := newSearchRequest(t, pd)
			switch tc.name {
			case "missing profile":
				req = newSearchRequest(t, nil)
			case "missing book", "empty query missing book":
				req.SetPathValue("id", "missing")
				if tc.name == "empty query missing book" {
					req.URL.RawQuery = ""
				}
			case "deleted row":
				// A summary can outlive the DB row until the cache refresh.
				if err := pd.DB.DeleteBookContext(t.Context(), book.ID); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			searchHandler(nil)(w, req)
			assertSearchError(t, w, tc.status, tc.want)
		})
	}
}

func TestSearchHandlerCanceled(t *testing.T) {
	t.Parallel()

	for _, warm := range []bool{false, true} {
		t.Run("warm="+strconv.FormatBool(warm), func(t *testing.T) {
			t.Parallel()
			pd := newSearchTestDeps(t, searchTestBook(t, "alpha beta"))
			if warm {
				w := httptest.NewRecorder()
				searchHandler(nil)(w, newSearchRequest(t, pd))
				readSearchResponse(t, w)
			}
			req := newSearchRequest(t, pd)
			ctx, cancel := context.WithCancel(req.Context())
			cancel()
			w := &searchDeadlineResponseWriter{ResponseRecorder: httptest.NewRecorder(), cleared: make(chan struct{})}
			searchHandler(nil)(w, req.WithContext(ctx))
			if w.wroteResponse || w.Body.Len() != 0 {
				t.Fatalf("canceled request wrote a response: headers = %v; body = %s", w.Header(), w.Body.String())
			}
		})
	}
}

func TestSearchHandlerProfileIsolation(t *testing.T) {
	t.Parallel()

	profiles := []*profileDeps{
		newSearchTestDeps(t, searchTestBook(t, "alpha profile one")),
		newSearchTestDeps(t, searchTestBook(t, "beta profile two")),
	}
	// Identical book IDs and hashes must not cross profile boundaries, even
	// when both profiles have warmed their extracted-text and spine caches.
	for range 2 {
		for i, pd := range profiles {
			w := httptest.NewRecorder()
			searchHandler(nil)(w, newSearchRequest(t, pd))
			got := readSearchResponse(t, w)
			if i == 0 {
				if len(got.Results) != 1 || got.Results[0].Snippet != "alpha profile one" {
					t.Fatalf("first profile results = %+v", got.Results)
				}
			} else if len(got.Results) != 0 {
				t.Fatalf("second profile leaked first profile results: %+v", got.Results)
			}
		}
	}
}

func TestSearchRouteRequiresSession(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	RegisterRoutes(mux, &Dependencies{sessions: newSessionStore(nil)})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, cookie := range []string{"", "unknown-session"} {
			t.Run(method+"/"+cookie, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequestWithContext(t.Context(), method, "/api/books/search-book/search?q=alpha", nil)
				if cookie != "" {
					req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, withProfileDeps(req, &profileDeps{}))
				assertSearchError(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
			})
		}
	}
}

func assertSearchJSON(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

func readSearchResponse(t *testing.T, w *httptest.ResponseRecorder) epub.SearchResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	assertSearchJSON(t, w)
	var response epub.SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if response.Results == nil {
		t.Fatal("results must be an array, not null")
	}
	if !response.HasMore {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["nextCursor"]; ok {
			t.Fatalf("terminal response includes nextCursor: %s", w.Body.String())
		}
	}
	return response
}

func assertSearchError(t *testing.T, w *httptest.ResponseRecorder, status int, want apiError) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, status, w.Body.String())
	}
	assertSearchJSON(t, w)
	var got apiError
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if got != want {
		t.Fatalf("error = %+v, want %+v", got, want)
	}
}
