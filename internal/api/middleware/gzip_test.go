package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
