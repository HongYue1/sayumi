package main

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstrumentationPreservesAbortHandler(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, tc := range []struct {
		name string
		wrap middlewareFactory
	}{
		{"normal", recoveryMiddleware},
		{"debug", debugInstrumentMiddleware},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			recorder := httptest.NewRecorder()
			wrapped := tc.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler)
			}))
			func() {
				defer func() {
					if got := recover(); got != http.ErrAbortHandler { //nolint:errorlint // Verify the sentinel was propagated unchanged.
						t.Errorf("panic = %v, want http.ErrAbortHandler", got)
					}
				}()
				wrapped.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/book", nil))
			}()
			if logs.Len() != 0 || recorder.Body.Len() != 0 {
				t.Errorf("abort was logged or turned into an error response: logs=%q body=%q", logs.String(), recorder.Body.String())
			}
		})
	}
}

func TestInstrumentationPanicAfterEarlyHints(t *testing.T) {
	setTestLogger(t, slog.LevelError+1)
	for _, tc := range []struct {
		name string
		wrap middlewareFactory
	}{
		{"normal", recoveryMiddleware},
		{"debug", debugInstrumentMiddleware},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ResponseRecorder commits 1xx as final; exercise the real net/http
			// writer to verify that early hints still permit a final 500.
			server := httptest.NewServer(tc.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusEarlyHints)
				panic("after hints")
			})))
			defer server.Close()
			response, err := server.Client().Get(server.URL + "/api/book")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "server_error") {
				t.Fatalf("response = %d %s, want final 500", response.StatusCode, body)
			}
		})
	}
}

type successfulHijacker struct {
	*interfaceResponseWriter
	conn net.Conn
}

func (w successfulHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func TestResponseTrackerHijackState(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	tracker := &responseTracker{ResponseWriter: successfulHijacker{newInterfaceResponseWriter(), server}}
	conn, rw, err := tracker.Hijack()
	if err != nil || conn != server || rw == nil || rw.Reader == nil || rw.Writer == nil || !tracker.wrote {
		t.Errorf("successful Hijack = (%v, %v), committed=%v", conn, err, tracker.wrote)
	}
	tracker = &responseTracker{ResponseWriter: newInterfaceResponseWriter()}
	if _, _, err := tracker.Hijack(); err == nil || tracker.wrote {
		t.Errorf("failed Hijack error=%v, committed=%v", err, tracker.wrote)
	}
}

func TestHumanizeBytesExtremes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{-1, "-1B"},
		{math.MinInt64, "-9223372036854775808B"},
		{1024*1024 - 1, "1024.0KB"},
		{math.MaxInt64, "8796093022208.0MB"},
	} {
		if got := humanizeBytes(tc.n); got != tc.want {
			t.Errorf("humanizeBytes(%d)=%q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestStatusWriterInformationalResponses(t *testing.T) {
	t.Parallel()
	for _, final := range []int{http.StatusOK, http.StatusCreated, http.StatusSwitchingProtocols} {
		writer := &statusWriter{ResponseWriter: newInterfaceResponseWriter(), status: http.StatusOK}
		writer.WriteHeader(http.StatusContinue)
		writer.WriteHeader(http.StatusEarlyHints)
		if writer.wrote {
			t.Fatal("informational response marked the response as committed")
		}
		writer.WriteHeader(final)
		writer.WriteHeader(http.StatusTeapot)
		if !writer.wrote || writer.status != final {
			t.Errorf("final response = (%v, %d), want (true, %d)", writer.wrote, writer.status, final)
		}
	}
}
