package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// levelDiscardHandler preserves level filtering while discarding output. Using
// slog.DiscardHandler would disable Debug entirely and invalidate the debug
// baseline/replacement comparison.
type levelDiscardHandler struct {
	level slog.Level
}

func (h levelDiscardHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (levelDiscardHandler) Handle(context.Context, slog.Record) error { return nil }
func (h levelDiscardHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h levelDiscardHandler) WithGroup(string) slog.Handler           { return h }

func setTestLogger(tb testing.TB, level slog.Level) {
	tb.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(levelDiscardHandler{level: level}))
	tb.Cleanup(func() {
		slog.SetDefault(previous)
	})
}

func TestRecoveryMiddlewarePanicBeforeWrite(t *testing.T) {
	setTestLogger(t, slog.LevelError+1)
	handler := recoveryMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/fail", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if got, want := recorder.Body.String(), "{\"error\":\"internal server error\",\"code\":\"server_error\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestRecoveryMiddlewarePanicAfterWritePreservesResponse(t *testing.T) {
	setTestLogger(t, slog.LevelError+1)
	handler := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "accepted")
		panic("boom")
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/fail", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if got := recorder.Body.String(); got != "accepted" {
		t.Fatalf("body = %q, want accepted", got)
	}
}

func TestInstrumentMiddlewareDebugAccessLog(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	handler := slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	wrapped := instrumentMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "ok")
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/books", nil)
	wrapped.ServeHTTP(recorder, request)

	output := logs.String()
	for _, fragment := range []string{"msg=request", "method=POST", "path=/api/books", "status=201", "size=2B"} {
		if !strings.Contains(output, fragment) {
			t.Errorf("access log %q does not contain %q", output, fragment)
		}
	}
}

type interfaceResponseWriter struct {
	header       http.Header
	readFromUsed bool
	flushed      bool
}

func newInterfaceResponseWriter() *interfaceResponseWriter {
	return &interfaceResponseWriter{header: make(http.Header)}
}

func (w *interfaceResponseWriter) Header() http.Header { return w.header }
func (*interfaceResponseWriter) WriteHeader(int)       {}
func (*interfaceResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *interfaceResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	w.readFromUsed = true
	return io.Copy(io.Discard, r)
}
func (w *interfaceResponseWriter) Flush() { w.flushed = true }
func (*interfaceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test hijack")
}

type readerOnly struct {
	io.Reader
}

func TestResponseTrackerPreservesOptionalInterfaces(t *testing.T) {
	underlying := newInterfaceResponseWriter()
	tracker := &responseTracker{ResponseWriter: underlying}

	if _, err := io.Copy(tracker, readerOnly{Reader: strings.NewReader("payload")}); err != nil {
		t.Fatal(err)
	}
	tracker.Flush()
	if !underlying.readFromUsed {
		t.Error("io.ReaderFrom was not forwarded")
	}
	if !underlying.flushed {
		t.Error("http.Flusher was not forwarded")
	}
	if tracker.Unwrap() != underlying {
		t.Error("Unwrap did not return the underlying writer")
	}
	if _, _, err := tracker.Hijack(); err == nil || err.Error() != "test hijack" {
		t.Fatalf("Hijack error = %v", err)
	}
}

type legacyStatusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int
}

func (w *legacyStatusWriter) markWritten(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
}

func (w *legacyStatusWriter) WriteHeader(code int) {
	w.markWritten(code)
	w.ResponseWriter.WriteHeader(code)
}

func (w *legacyStatusWriter) Write(p []byte) (int, error) {
	w.markWritten(http.StatusOK)
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *legacyStatusWriter) ReadFrom(r io.Reader) (int64, error) {
	w.markWritten(http.StatusOK)
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		w.bytes += int(n)
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += int(n)
	return n, err
}

func (w *legacyStatusWriter) Flush() {
	w.markWritten(http.StatusOK)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *legacyStatusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *legacyStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func legacyInstrumentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &legacyStatusWriter{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))
				if !writer.wrote {
					writeInternalServerError(writer, r)
				}
			}
			if slog.Default().Enabled(r.Context(), slog.LevelDebug) {
				slog.Log(
					r.Context(), slog.LevelDebug, "request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", writer.status,
					"size", humanizeBytes(writer.bytes),
					"duration", time.Since(start),
				)
			}
		}()

		next.ServeHTTP(writer, r)
	})
}

type benchmarkResponseWriter struct {
	header http.Header
	status int
	bytes  int
}

func newBenchmarkResponseWriter() *benchmarkResponseWriter {
	return &benchmarkResponseWriter{header: make(http.Header)}
}

func (w *benchmarkResponseWriter) Header() http.Header { return w.header }
func (w *benchmarkResponseWriter) WriteHeader(code int) {
	w.status = code
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.bytes += len(p)
	return len(p), nil
}

func (w *benchmarkResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(io.Discard, r)
	w.bytes += int(n)
	return n, err
}

func (w *benchmarkResponseWriter) reset() {
	clear(w.header)
	w.status = 0
	w.bytes = 0
}

type middlewareFactory func(http.Handler) http.Handler

func runMiddlewareBenchmark(b *testing.B, level slog.Level, factory middlewareFactory, workload http.Handler) {
	setTestLogger(b, level)
	wantDebug := level <= slog.LevelDebug
	if got := slog.Default().Enabled(context.Background(), slog.LevelDebug); got != wantDebug {
		b.Fatalf("Debug enabled = %v, want %v", got, wantDebug)
	}
	wrapped := factory(workload)
	request := httptest.NewRequest(http.MethodGet, "/api/books", nil)
	writer := newBenchmarkResponseWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		wrapped.ServeHTTP(writer, request)
		writer.reset()
	}
}

var (
	benchmarkJSON   = []byte("{\"books\":[]}")
	benchmarkStatic = bytes.Repeat([]byte("sayumi-static-content-"), 4096)
)

func smallJSONWorkload(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(benchmarkJSON)
}

func staticContentWorkload(w http.ResponseWriter, _ *http.Request) {
	source := readerOnly{Reader: bytes.NewReader(benchmarkStatic)}
	_, _ = io.Copy(w, source)
}

func panicBeforeWriteWorkload(http.ResponseWriter, *http.Request) { panic("benchmark") }
func panicAfterWriteWorkload(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	panic("benchmark")
}

func BenchmarkInstrumentMiddlewareNormalSmallJSON(b *testing.B) {
	workload := http.HandlerFunc(smallJSONWorkload)
	b.Run("legacy", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, legacyInstrumentMiddleware, workload)
	})
	b.Run("specialized", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, recoveryMiddleware, workload)
	})
}

func BenchmarkInstrumentMiddlewareNormalStaticContent(b *testing.B) {
	workload := http.HandlerFunc(staticContentWorkload)
	b.Run("legacy", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, legacyInstrumentMiddleware, workload)
	})
	b.Run("specialized", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, recoveryMiddleware, workload)
	})
}

func BenchmarkInstrumentMiddlewareDebugSmallJSON(b *testing.B) {
	workload := http.HandlerFunc(smallJSONWorkload)
	b.Run("legacy", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelDebug, legacyInstrumentMiddleware, workload)
	})
	b.Run("specialized", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelDebug, debugInstrumentMiddleware, workload)
	})
}

func BenchmarkInstrumentMiddlewarePanicBeforeWrite(b *testing.B) {
	workload := http.HandlerFunc(panicBeforeWriteWorkload)
	b.Run("legacy", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, legacyInstrumentMiddleware, workload)
	})
	b.Run("specialized", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, recoveryMiddleware, workload)
	})
}

func BenchmarkInstrumentMiddlewarePanicAfterWrite(b *testing.B) {
	workload := http.HandlerFunc(panicAfterWriteWorkload)
	b.Run("legacy", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, legacyInstrumentMiddleware, workload)
	})
	b.Run("specialized", func(b *testing.B) {
		runMiddlewareBenchmark(b, slog.LevelError+1, recoveryMiddleware, workload)
	})
}
