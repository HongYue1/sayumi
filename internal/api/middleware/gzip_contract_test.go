package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func gzipContractRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	return r
}

func gzipContractBody(t *testing.T, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	var reader io.Reader = res.Body
	if res.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			t.Fatalf("invalid gzip header: %v", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("incomplete response body: %v", err)
	}
	return body
}

func TestGzipNegotiation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"br, identity", false},
		{"gzip", true},
		{"GZİP", false},
		{"gzıp", false},
		{" GZIP ; Q = 0.125 ", true},
		{"gzip;Q=0", false},
		{"gzip;q=0", false},
		{"gzip;q=0.000", false},
		{"gzip;q=0.001", true},
		{"gzip;q=1.000", true},
		{"gzip;q=1.", true},
		{"gzip;q=0.", false},
		{"*;q=1", true},
		{"gzip;q=0, *;q=1", false},
		{"*;q=0, gzip;q=0.5", true},
		{"gzip;q=0, gzip;q=1", false},
		{"gzip;q=1, gzip;q=0", false},
		{"*;q=0, *;q=1", false},
		{"gzip;q=bad", false},
		{"gzip;q=", false},
		{"gzip;q=-1", false},
		{"gzip;q=2", false},
		{"gzip;q=1.001", false},
		{"gzip;q=0.0001", false},
		{"gzip;q=+0.5", false},
		{"gzip;q=1e0", false},
		{"gzip;q=+Inf", false},
		{"gzip;q=NaN", false},
		{"gzip;q=01", false},
		{"gzip;q=0;q=1", false},
		{"gzip;broken", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := acceptsGzip(tc.value); got != tc.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestGzipRepeatedHeadersAndBypasses(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("a"), minGzipSize+1)
	cases := []struct {
		name     string
		request  http.Header
		response http.Header
		wantGzip bool
	}{
		{name: "later acceptance", request: http.Header{"Accept-Encoding": {"br", "gzip"}}, wantGzip: true},
		{name: "later refusal", request: http.Header{"Accept-Encoding": {"*", "gzip;q=0"}}},
		{name: "earlier refusal", request: http.Header{"Accept-Encoding": {"gzip;q=0", "gzip"}}},
		{name: "later no transform", response: http.Header{"Cache-Control": {"private", "no-transform"}}},
		{name: "later connection upgrade", request: http.Header{"Connection": {"keep-alive", "Upgrade"}}},
		{name: "later range", request: http.Header{"Range": {"", "bytes=0-10"}}},
		{name: "later upgrade", request: http.Header{"Upgrade": {"", "websocket"}}},
		{name: "response range", response: http.Header{"Content-Range": {"bytes 0-1400/2000"}}},
		{name: "existing encoding", response: http.Header{"Content-Encoding": {"br"}}},
		{name: "event stream", response: http.Header{"Content-Type": {"text/Event-Stream; charset=utf-8"}}},
		{name: "mixed case json", response: http.Header{"Content-Type": {"Application/JSON"}}, wantGzip: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := gzipContractRequest()
			for k, v := range tc.request {
				req.Header[k] = slices.Clone(v)
			}
			rec := httptest.NewRecorder()
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for k, v := range tc.response {
					w.Header()[k] = slices.Clone(v)
				}
				if _, err := w.Write(body); err != nil {
					t.Error(err)
				}
			})).ServeHTTP(rec, req)
			if got := rec.Result().Header.Get("Content-Encoding") == "gzip"; got != tc.wantGzip {
				t.Errorf("gzip = %v, want %v", got, tc.wantGzip)
			}
			if got := gzipContractBody(t, rec); !bytes.Equal(got, body) {
				t.Fatalf("body changed: %d bytes, want %d", len(got), len(body))
			}
		})
	}
}

func TestGzipReadFromSizeFloor(t *testing.T) {
	t.Parallel()
	for _, prefix := range []int{0, 17} {
		for _, total := range []int{0, 17, minGzipSize - 1, minGzipSize, minGzipSize + 1} {
			if prefix > total {
				continue
			}
			t.Run(fmt.Sprintf("prefix%d/total%d", prefix, total), func(t *testing.T) {
				body := bytes.Repeat([]byte("0123456789abcdef"), (total+15)/16)[:total]
				rec := httptest.NewRecorder()
				Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if prefix != 0 {
						if n, err := w.Write(body[:prefix]); n != prefix || err != nil {
							t.Fatalf("prefix write = %d, %v", n, err)
						}
					}
					n, err := io.Copy(w, plainReader{r: bytes.NewReader(body[prefix:])})
					if n != int64(total-prefix) || err != nil {
						t.Fatalf("copy = %d, %v", n, err)
					}
				})).ServeHTTP(rec, gzipContractRequest())
				if got, want := rec.Result().Header.Get("Content-Encoding") == "gzip", total >= minGzipSize; got != want {
					t.Errorf("gzip = %v, want %v", got, want)
				}
				if got := gzipContractBody(t, rec); !bytes.Equal(got, body) {
					t.Fatalf("round trip: %d bytes, want %d", len(got), total)
				}
			})
		}
	}
}

func TestGzipFlushRoundTrip(t *testing.T) {
	t.Parallel()
	for _, prefix := range []int{0, 5, minGzipSize} {
		for _, suffix := range []int{0, 5, minGzipSize} {
			t.Run(fmt.Sprintf("prefix%d/suffix%d", prefix, suffix), func(t *testing.T) {
				body := bytes.Repeat([]byte("f"), prefix+suffix)
				rec := httptest.NewRecorder()
				Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					if prefix > 0 {
						if _, err := w.Write(body[:prefix]); err != nil {
							t.Fatal(err)
						}
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Fatal(err)
					}
					if !rec.Flushed {
						t.Error("underlying response was not flushed")
					}
					if suffix > 0 {
						if _, err := w.Write(body[prefix:]); err != nil {
							t.Fatal(err)
						}
					}
				})).ServeHTTP(rec, gzipContractRequest())
				if got := gzipContractBody(t, rec); !bytes.Equal(got, body) {
					t.Fatalf("round trip: %d bytes, want %d", len(got), len(body))
				}
			})
		}
	}
}

func TestGzipFirstWriteLocksStatus(t *testing.T) {
	t.Parallel()
	for _, first := range []int{0, 3, minGzipSize} {
		for _, late := range []int{http.StatusCreated, http.StatusNoContent, http.StatusNotModified, http.StatusPartialContent} {
			t.Run(fmt.Sprintf("first%d/late%d", first, late), func(t *testing.T) {
				body := bytes.Repeat([]byte("s"), first+7)
				rec := httptest.NewRecorder()
				Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					if _, err := w.Write(body[:first]); err != nil {
						t.Fatal(err)
					}
					w.WriteHeader(late)
					if _, err := w.Write(body[first:]); err != nil {
						t.Fatal(err)
					}
				})).ServeHTTP(rec, gzipContractRequest())
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want 200", rec.Code)
				}
				if got := gzipContractBody(t, rec); !bytes.Equal(got, body) {
					t.Fatalf("round trip: %d bytes, want %d", len(got), len(body))
				}
			})
		}
	}
}

// ResponseRecorder itself treats 1xx as final. Model net/http's informational
// status contract here; a separate loopback test checks actual wire behavior.
type informationalRecorder struct {
	*httptest.ResponseRecorder
	informational []int
}

func (w *informationalRecorder) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.informational = append(w.informational, code)
		return
	}
	w.ResponseRecorder.WriteHeader(code)
}

func TestGzipInformationalStatus(t *testing.T) {
	t.Parallel()
	rec := &informationalRecorder{ResponseRecorder: httptest.NewRecorder()}
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusEarlyHints)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("accepted")); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(rec, gzipContractRequest())
	if !slices.Equal(rec.informational, []int{103, 103}) || rec.Code != http.StatusAccepted {
		t.Errorf("informational=%v final=%d", rec.informational, rec.Code)
	}
	if got := gzipContractBody(t, rec.ResponseRecorder); string(got) != "accepted" {
		t.Errorf("body = %q", got)
	}
}

func TestGzipInvalidStatusPanicsAtCall(t *testing.T) {
	t.Parallel()
	for _, code := range []int{99, 1000} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			calledAfter := false
			func() {
				defer func() {
					if recover() == nil {
						t.Error("invalid status did not panic")
					}
				}()
				Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(code)
					calledAfter = true
				})).ServeHTTP(httptest.NewRecorder(), gzipContractRequest())
			}()
			if calledAfter {
				t.Error("invalid WriteHeader returned to the handler")
			}
		})
	}
}

func TestGzipHeaderSnapshot(t *testing.T) {
	t.Parallel()
	for _, implicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(implicit), func(t *testing.T) {
			body := bytes.Repeat([]byte("h"), minGzipSize+1)
			rec := httptest.NewRecorder()
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Snapshot", "before")
				if implicit {
					if _, err := w.Write(body[:1]); err != nil {
						t.Fatal(err)
					}
				} else {
					w.WriteHeader(http.StatusOK)
				}
				w.Header().Set("Content-Type", "image/png")
				w.Header()["X-Snapshot"][0] = "after"
				if implicit {
					body = body[1:]
				}
				if _, err := w.Write(body); err != nil {
					t.Fatal(err)
				}
			})).ServeHTTP(rec, gzipContractRequest())
			res := rec.Result()
			defer func() { _ = res.Body.Close() }()
			if res.Header.Get("X-Snapshot") != "before" || res.Header.Get("Content-Type") != "application/json" {
				t.Errorf("late header mutation escaped: %v", res.Header)
			}
			if res.Header.Get("Content-Encoding") != "gzip" {
				t.Error("late header mutation changed compression eligibility")
			}
		})
	}
}

func TestGzipVaryAndValidators(t *testing.T) {
	t.Parallel()
	for _, accept := range []string{"", "gzip"} {
		for _, tag := range []string{`"version"`, `W/"version"`} {
			t.Run(accept+tag, func(t *testing.T) {
				req := gzipContractRequest()
				req.Header.Set("Accept-Encoding", accept)
				rec := httptest.NewRecorder()
				Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Vary", "Origin")
					w.Header().Set("ETag", tag)
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Content-Length", strconv.Itoa(minGzipSize))
					if _, err := w.Write(bytes.Repeat([]byte("v"), minGzipSize)); err != nil {
						t.Error(err)
					}
				})).ServeHTTP(rec, req)
				res := rec.Result()
				defer func() { _ = res.Body.Close() }()
				vary := strings.Join(res.Header.Values("Vary"), ",")
				if !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Accept-Encoding") {
					t.Errorf("Vary = %q", vary)
				}
				wantTag := tag
				if accept != "" {
					wantTag = `W/"version"`
					if res.Header.Get("Content-Length") != "" {
						t.Error("uncompressed content length survived compression")
					}
				}
				if got := res.Header.Get("ETag"); got != wantTag {
					t.Errorf("ETag = %q, want %q", got, wantTag)
				}
			})
		}
	}
}

type shortGzipWriter struct{ *httptest.ResponseRecorder }

func (w *shortGzipWriter) Write(p []byte) (int, error) {
	return w.ResponseRecorder.Write(p[:len(p)/2])
}

func TestGzipReadFromShortWrite(t *testing.T) {
	t.Parallel()
	w := &shortGzipWriter{ResponseRecorder: httptest.NewRecorder()}
	g := &gzipResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	defer g.close()
	n, err := g.ReadFrom(plainReader{r: bytes.NewReader([]byte{0, 1, 2, 3})})
	if n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("ReadFrom = %d, %v; want 2, ErrShortWrite", n, err)
	}
}

type flushErrorWriter struct {
	header http.Header
	err    error
}

func (w *flushErrorWriter) Header() http.Header         { return w.header }
func (w *flushErrorWriter) WriteHeader(int)             {}
func (w *flushErrorWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *flushErrorWriter) FlushError() error           { return w.err }

func TestGzipFlushError(t *testing.T) {
	t.Parallel()
	want := errors.New("flush failed")
	w := &flushErrorWriter{header: make(http.Header), err: want}
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, want) {
			t.Errorf("Flush = %v, want %v", err, want)
		}
	})).ServeHTTP(w, gzipContractRequest())
}

type observingGzipWriter struct {
	*httptest.ResponseRecorder
	onWrite func()
}

func (w *observingGzipWriter) Write(p []byte) (int, error) {
	w.onWrite()
	return w.ResponseRecorder.Write(p)
}

func TestGzipPendingIsBounded(t *testing.T) {
	t.Parallel()
	var g *gzipResponseWriter
	largest := 0
	w := &observingGzipWriter{ResponseRecorder: httptest.NewRecorder()}
	w.onWrite = func() { largest = max(largest, len(g.pending)) }
	g = &gzipResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	defer g.close()
	g.Header().Set("Content-Type", "text/plain")
	if _, err := g.Write([]byte("prefix")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Write(bytes.Repeat([]byte("b"), 2<<20)); err != nil {
		t.Fatal(err)
	}
	if largest >= minGzipSize {
		t.Errorf("buffered %d bytes; pending must remain below %d", largest, minGzipSize)
	}
}

type hijackGzipWriter struct {
	*httptest.ResponseRecorder
	conn     net.Conn
	hijacked bool
	after    int
}

func (w *hijackGzipWriter) WriteHeader(code int) {
	if w.hijacked {
		w.after++
		return
	}
	w.ResponseRecorder.WriteHeader(code)
}

func (w *hijackGzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestGzipHijackDoesNotFinishHTTP(t *testing.T) {
	t.Parallel()
	conn, peer := net.Pipe()
	defer func() { _ = conn.Close() }()
	defer func() { _ = peer.Close() }()
	w := &hijackGzipWriter{ResponseRecorder: httptest.NewRecorder(), conn: conn}
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(w, gzipContractRequest())
	if w.after != 0 || w.Body.Len() != 0 {
		t.Errorf("middleware wrote after hijack: headers=%d body=%d", w.after, w.Body.Len())
	}
}

func TestGzipEmptyReadFromLeavesStatusUncommitted(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if n, err := io.Copy(w, plainReader{r: strings.NewReader("")}); n != 0 || err != nil {
			t.Fatalf("empty copy = %d, %v", n, err)
		}
		w.WriteHeader(http.StatusNotFound)
	})).ServeHTTP(rec, gzipContractRequest())
	if rec.Code != http.StatusNotFound || len(gzipContractBody(t, rec)) != 0 {
		t.Errorf("empty stream committed status %d", rec.Code)
	}
}

func TestGzipPreservesContentTypeSuppression(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		if _, err := w.Write(bytes.Repeat([]byte("x"), minGzipSize)); err != nil {
			t.Fatal(err)
		}
	})).ServeHTTP(rec, gzipContractRequest())
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("Content-Type") != "" || res.Header.Get("Content-Encoding") != "" {
		t.Errorf("suppressed content type was sniffed: %v", res.Header)
	}
}

func TestGzipVaryWildcard(t *testing.T) {
	t.Parallel()
	h := http.Header{"Vary": {"*"}}
	addVaryAcceptEncoding(h)
	if !slices.Equal(h.Values("Vary"), []string{"*"}) {
		t.Errorf("Vary wildcard changed: %v", h)
	}
	h = http.Header{"Vary": {"Origin", "aCCept-eNCoding"}}
	addVaryAcceptEncoding(h)
	if len(h.Values("Vary")) != 2 {
		t.Errorf("duplicate Vary added: %v", h)
	}
}

func TestGzipPanicDoesNotFinalizeBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	const marker = "handler panic"
	func() {
		defer func() {
			if got := recover(); got != marker {
				t.Errorf("panic = %v, want marker", got)
			}
		}()
		Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if _, err := w.Write(bytes.Repeat([]byte("x"), minGzipSize)); err != nil {
				t.Fatal(err)
			}
			panic(marker)
		})).ServeHTTP(rec, gzipContractRequest())
	}()
	if abandoned, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes())); err == nil {
		defer func() { _ = abandoned.Close() }()
		if _, err := io.ReadAll(abandoned); err == nil {
			t.Error("panicking handler left a deceptively complete gzip stream")
		}
	}
	// A later successful request must not inherit the abandoned stream.
	res := serveGzip(t, "gzip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(bytes.Repeat([]byte("y"), minGzipSize))
	})
	defer func() { _ = res.Body.Close() }()
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	body, err := io.ReadAll(gz)
	if err != nil || !bytes.Equal(body, bytes.Repeat([]byte("y"), minGzipSize)) {
		t.Errorf("response after panic: bytes=%d err=%v", len(body), err)
	}
}

type errorGzipReader struct {
	body []byte
	err  error
}

func (r *errorGzipReader) Read(p []byte) (int, error) {
	n := copy(p, r.body)
	r.body = r.body[n:]
	return n, r.err
}

func TestGzipReadFromPreservesReaderError(t *testing.T) {
	t.Parallel()
	want := errors.New("reader failed")
	rec := httptest.NewRecorder()
	Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		n, err := io.Copy(w, &errorGzipReader{body: []byte("partial"), err: want})
		if n != 7 || !errors.Is(err, want) {
			t.Errorf("ReadFrom = %d, %v", n, err)
		}
	})).ServeHTTP(rec, gzipContractRequest())
	if got := gzipContractBody(t, rec); string(got) != "partial" {
		t.Errorf("bytes accompanying the read error were lost: %q", got)
	}
}

func TestGzipHijackDrainsPendingBody(t *testing.T) {
	t.Parallel()
	for _, size := range []int{6, minGzipSize + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			conn, peer := net.Pipe()
			defer func() { _ = conn.Close() }()
			defer func() { _ = peer.Close() }()
			w := &hijackGzipWriter{ResponseRecorder: httptest.NewRecorder(), conn: conn}
			body := bytes.Repeat([]byte("j"), size)
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if _, err := w.Write(body); err != nil {
					t.Fatal(err)
				}
				if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte("late")); !errors.Is(err, http.ErrHijacked) {
					t.Errorf("Write after hijack = %v", err)
				}
				if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrHijacked) {
					t.Errorf("Flush after hijack = %v", err)
				}
			})).ServeHTTP(w, gzipContractRequest())
			if w.after != 0 || !bytes.Equal(gzipContractBody(t, w.ResponseRecorder), body) {
				t.Error("pending response was not completely handed off")
			}
		})
	}
}

type readerFromGzipWriter struct {
	*httptest.ResponseRecorder
	called bool
}

func (w *readerFromGzipWriter) ReadFrom(r io.Reader) (int64, error) {
	w.called = true
	return io.Copy(w.ResponseRecorder, r)
}

type unwrapGzipWriter struct{ http.ResponseWriter }

func (w unwrapGzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestGzipReaderFromAndControllerForwarding(t *testing.T) {
	t.Parallel()
	for _, accept := range []string{"", "gzip"} {
		t.Run(accept, func(t *testing.T) {
			base := &readerFromGzipWriter{ResponseRecorder: httptest.NewRecorder()}
			req := gzipContractRequest()
			req.Header.Set("Accept-Encoding", accept)
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "image/png")
				if _, err := w.Write([]byte("prefix")); err != nil {
					t.Fatal(err)
				}
				if n, err := io.Copy(w, plainReader{r: strings.NewReader("suffix")}); n != 6 || err != nil {
					t.Fatalf("copy = %d, %v", n, err)
				}
				w.(http.Flusher).Flush()
			})).ServeHTTP(base, req)
			if !base.called || !base.Flushed || string(gzipContractBody(t, base.ResponseRecorder)) != "prefixsuffix" {
				t.Error("underlying ReaderFrom/Flusher contract was lost")
			}
		})
	}
	base := httptest.NewRecorder()
	g := &gzipResponseWriter{ResponseWriter: unwrapGzipWriter{ResponseWriter: base}}
	if g.Unwrap() == nil || http.NewResponseController(g).Flush() != nil || !base.Flushed {
		t.Error("ResponseController did not traverse Unwrap")
	}
	if _, _, err := g.Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("unsupported Hijack = %v", err)
	}
	if err := g.Push("/app.css", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("unsupported Push = %v", err)
	}
}

type writeErrorGzipWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w *writeErrorGzipWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGzipPropagatesCompressedWriteErrors(t *testing.T) {
	t.Parallel()
	for _, flush := range []bool{false, true} {
		t.Run(strconv.FormatBool(flush), func(t *testing.T) {
			want := errors.New("write failed")
			base := &writeErrorGzipWriter{ResponseRecorder: httptest.NewRecorder(), err: want}
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if flush {
					if _, err := w.Write([]byte("prefix")); err != nil {
						t.Fatal(err)
					}
					if err := http.NewResponseController(w).Flush(); !errors.Is(err, want) {
						t.Errorf("compressed Flush = %v", err)
					}
				} else if n, err := w.Write(bytes.Repeat([]byte("x"), minGzipSize)); n != 0 || !errors.Is(err, want) {
					t.Errorf("compressed Write = %d, %v", n, err)
				}
			})).ServeHTTP(base, gzipContractRequest())
		})
	}
}

func TestGzipNotModifiedValidator(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"", "text/plain", "font/woff2"} {
		t.Run(contentType, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if contentType != "" {
					w.Header().Set("Content-Type", contentType)
				}
				w.Header().Set("ETag", `"version"`)
				w.WriteHeader(http.StatusNotModified)
			})).ServeHTTP(rec, gzipContractRequest())
			res := rec.Result()
			defer func() { _ = res.Body.Close() }()
			want := `W/"version"`
			if contentType == "font/woff2" {
				want = `"version"`
			}
			if got := res.Header.Get("ETag"); got != want {
				t.Errorf("304 replaced the cached representation's validator: got %q want %q", got, want)
			}
			if res.Header.Get("Content-Encoding") != "" || rec.Body.Len() != 0 {
				t.Error("304 acquired an encoded body")
			}
		})
	}
}

func FuzzGzipNegotiation(f *testing.F) {
	for _, seed := range []string{"", "gzip", "*;q=1", "gzip;q=0", "GZIP;Q=0.125", "gzip;q=NaN", "gzip;q=0,gzip"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 8192 {
			t.Skip()
		}
		if acceptsGzip("gzip;q=0,"+value) || acceptsGzip(value+",gzip;q=0") {
			t.Fatal("an explicit refusal was overridden")
		}
		// Unicode uppercasing can turn a non-token (e.g. dotless i) into an
		// ASCII token. Case-insensitivity is a property of ASCII field syntax.
		for i := range len(value) {
			if value[i] >= 0x80 {
				return
			}
		}
		if acceptsGzip(value) != acceptsGzip(strings.ToUpper(value)) {
			t.Fatal("negotiation depends on token/parameter casing")
		}
	})
}
