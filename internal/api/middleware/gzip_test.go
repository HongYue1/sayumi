package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if len(got) != len(body) {
		t.Errorf("body len = %d, want %d", len(got), len(body))
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
