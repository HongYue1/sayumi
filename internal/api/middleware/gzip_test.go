package middleware

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// serveGzip runs h behind the Gzip middleware for a GET / request with the given
// Accept-Encoding (omitted when empty) and returns the recorded response.
func serveGzip(t *testing.T, acceptEncoding string, h http.HandlerFunc) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	Gzip(h).ServeHTTP(rec, req)
	return rec.Result()
}

// TestGzipSkipsSmallBody verifies that a compressible body below minGzipSize is
// sent uncompressed and byte-for-byte intact.
func TestGzipSkipsSmallBody(t *testing.T) {
	body := `{"ok":true}` // well under minGzipSize
	resp := serveGzip(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	defer func() { _ = resp.Body.Close() }()

	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (small body must not be gzipped)", enc)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

// TestGzipCompressesLargeBody verifies that a compressible body at or above
// minGzipSize is gzip-encoded and round-trips back to the original bytes.
func TestGzipCompressesLargeBody(t *testing.T) {
	body := strings.Repeat("sayumi-notion ", 200) // comfortably over minGzipSize
	resp := serveGzip(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	defer func() { _ = resp.Body.Close() }()

	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	gr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gr.Close() }()
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gunzipped: %v", err)
	}
	if string(got) != body {
		t.Errorf("decompressed body does not match original (len got=%d want=%d)", len(got), len(body))
	}
}

// TestGzipSkipsWhenNotAccepted verifies that a body large enough to compress is
// still sent as-is when the client does not advertise gzip support.
func TestGzipSkipsWhenNotAccepted(t *testing.T) {
	body := strings.Repeat("x", 4096)
	resp := serveGzip(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	defer func() { _ = resp.Body.Close() }()

	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (client did not accept gzip)", enc)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != body {
		t.Errorf("body mismatch (len got=%d want=%d)", len(got), len(body))
	}
}

// plainReader wraps an io.Reader and exposes only Read, deliberately hiding any
// io.WriterTo the underlying reader implements. This forces io.Copy into the
// gzip writer to take the io.ReaderFrom path — the exact shape that previously
// drove gzipResponseWriter.ReadFrom into infinite self-recursion when no
// Content-Type was set.
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// TestGzipReadFromWithoutContentType streams a body via io.Copy (which
// dispatches to ReadFrom) with no Content-Type set. It must complete and return
// the bytes intact rather than recursing into ReadFrom forever.
func TestGzipReadFromWithoutContentType(t *testing.T) {
	body := strings.Repeat("sayumi ", 500) // over minGzipSize once sniffed

	done := make(chan *http.Response, 1)
	go func() {
		done <- serveGzip(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
			// No Content-Type set: forces the sniffing path in ReadFrom.
			_, _ = io.Copy(w, plainReader{r: strings.NewReader(body)})
		})
	}()

	var resp *http.Response
	select {
	case resp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadFrom did not complete: likely infinite-recursion regression")
	}
	defer func() { _ = resp.Body.Close() }()

	reader := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer func() { _ = gr.Close() }()
		reader = gr
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != body {
		t.Errorf("body round-trip mismatch (len got=%d want=%d)", len(got), len(body))
	}
}

// A handler that sets a compressible Content-Type and writes no body used to
// get Content-Encoding: gzip with a zero-byte body — not a valid gzip stream,
// which strict decoders reject outright. An empty body needs no encoding.
func TestGzipEmptyCompressibleBodyIsNotDeclaredGzip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
	}{
		{"empty 200", http.StatusOK},
		{"empty 404", http.StatusNotFound},
	}
	for _, tc := range cases {
		handler := Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			w.WriteHeader(tc.status)
		}))

		req := httptest.NewRequest(http.MethodGet, "/empty.css", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		res := rec.Result()
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
		if got := res.Header.Get("Content-Encoding"); got != "" {
			t.Errorf("%s: Content-Encoding = %q with a %d-byte body, want none",
				tc.name, got, rec.Body.Len())
		}
		if res.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, res.StatusCode, tc.status)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s: body = %d bytes, want 0", tc.name, rec.Body.Len())
		}
	}
}

// TestGzipHTTPWireContract uses a real HTTP/1 connection: ResponseRecorder does
// not implement informational responses, forbidden bodies, or chunked trailers.
func TestGzipHTTPWireContract(t *testing.T) {
	t.Parallel()
	for _, size := range []int{17, minGzipSize + 37} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := strings.Repeat("0123456789abcdef", (size+15)/16)[:size]
			server := httptest.NewServer(Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Link", "</app.css>; rel=preload")
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusEarlyHints)
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Trailer", "X-Done")
				w.Header().Set("X-Snapshot", "before")
				w.WriteHeader(http.StatusAccepted)
				w.Header().Set("X-Snapshot", "after")
				if _, err := io.WriteString(w, body[:3]); err != nil {
					t.Error(err)
					return
				}
				if _, err := io.Copy(w, plainReader{r: strings.NewReader(body[3:])}); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("X-Done", "complete")
				w.Header().Set(http.TrailerPrefix+"X-Late", "late")
			})))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			var mu sync.Mutex
			var informational []int
			trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
				mu.Lock()
				defer mu.Unlock()
				informational = append(informational, code)
				return nil
			}}
			ctx := httptrace.WithClientTrace(t.Context(), trace)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", "gzip") // Disable automatic client decoding.
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			var reader io.Reader = res.Body
			if res.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = gz.Close() }()
				reader = gz
			}
			got, err := io.ReadAll(reader)
			if err != nil || string(got) != body {
				t.Fatalf("wire body: bytes=%d err=%v", len(got), err)
			}
			mu.Lock()
			codes := slices.Clone(informational)
			mu.Unlock()
			if !slices.Equal(codes, []int{103, 103}) || res.StatusCode != http.StatusAccepted {
				t.Errorf("informational=%v final=%d", codes, res.StatusCode)
			}
			if res.Header.Get("X-Snapshot") != "before" {
				t.Errorf("late header escaped: %v", res.Header)
			}
			if res.Trailer.Get("X-Done") != "complete" || res.Trailer.Get("X-Late") != "late" {
				t.Errorf("trailers = %v", res.Trailer)
			}
			if got, want := res.Header.Get("Content-Encoding") == "gzip", size >= minGzipSize; got != want {
				t.Errorf("gzip = %v, want %v", got, want)
			}
		})
	}
}

func TestGzipHTTPLateTrailer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Unknown trailer names require chunking to be selected before a small
		// body is auto-sized by net/http; this is also true without middleware.
		w.Header().Set("Transfer-Encoding", "chunked")
		if _, err := io.WriteString(w, "small body"); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set(http.TrailerPrefix+"X-Late", "complete")
	})))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != "small body" || res.Header.Get("Content-Encoding") != "" {
		t.Fatalf("small response: body=%q err=%v", body, err)
	}
	if res.Trailer.Get("X-Late") != "complete" {
		t.Errorf("late trailer lost: %v", res.Trailer)
	}
}

func TestGzipHTTPForbiddenBodies(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			writeResult := make(chan error, 1)
			server := httptest.NewServer(Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, err := io.WriteString(w, strings.Repeat("x", minGzipSize))
				writeResult <- err
			})))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", "gzip")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			if err != nil || len(body) != 0 || res.StatusCode != status || res.Header.Get("Content-Encoding") != "" {
				t.Errorf("forbidden body: status=%d bytes=%d encoding=%q err=%v", res.StatusCode, len(body), res.Header.Get("Content-Encoding"), err)
			}
			select {
			case err := <-writeResult:
				if !errors.Is(err, http.ErrBodyNotAllowed) {
					t.Errorf("Write = %v, want ErrBodyNotAllowed", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not report its Write result")
			}
		})
	}
}
