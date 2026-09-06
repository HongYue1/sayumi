package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"sayumi/internal/api/middleware"
)

func TestDecodeJSONBodyRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{
			name:   "single value",
			body:   `{"name":"Sayumi"}`,
			wantOK: true,
		},
		{
			name:   "trailing whitespace",
			body:   "{\"name\":\"Sayumi\"}\n\t  ",
			wantOK: true,
		},
		{
			name:       "second object",
			body:       `{"name":"Sayumi"}{"extra":true}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "second scalar",
			body:       `{"name":"Sayumi"} true`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "trailing garbage",
			body:       `{"name":"Sayumi"} garbage`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "oversized trailing whitespace",
			body:       `{"name":"Sayumi"}` + strings.Repeat(" ", maxJSONBodySize),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			recorder := httptest.NewRecorder()
			var body struct {
				Name string `json:"name"`
			}

			gotOK := decodeJSONBody(recorder, req, &body)
			if gotOK != tc.wantOK {
				t.Fatalf("decodeJSONBody() = %v, want %v; status = %d, body = %s",
					gotOK, tc.wantOK, recorder.Code, recorder.Body.String())
			}
			if tc.wantOK {
				if body.Name != "Sayumi" {
					t.Errorf("decoded name = %q, want Sayumi", body.Name)
				}
				return
			}
			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body = %s",
					recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestDecodeJSONBodyLimitsAndCompatibility(t *testing.T) {
	t.Parallel()

	const emptyObject = `{"name":""}`
	exactName := strings.Repeat("x", maxJSONBodySize-len(emptyObject))
	exactBody := `{"name":"` + exactName + `"}`
	tests := []struct {
		name       string
		body       string
		wantName   string
		wantStatus int
	}{
		{name: "empty", wantStatus: http.StatusBadRequest},
		{name: "whitespace only", body: " \n\t", wantStatus: http.StatusBadRequest},
		{name: "truncated", body: `{"name":`, wantStatus: http.StatusBadRequest},
		{name: "wrong shape", body: `[]`, wantStatus: http.StatusBadRequest},
		{name: "wrong field type", body: `{"name":42}`, wantStatus: http.StatusBadRequest},
		{name: "null stays accepted", body: `null`},
		{name: "unknown fields stay accepted", body: `{"extra":true,"name":"Sayumi"}`, wantName: "Sayumi"},
		{name: "duplicate fields keep last value", body: `{"name":"first","name":"last"}`, wantName: "last"},
		{name: "unicode", body: `{"name":"日本語 café"}`, wantName: "日本語 café"},
		{name: "just below limit", body: `{"name":"` + exactName[:len(exactName)-1] + `"}`, wantName: exactName[:len(exactName)-1]},
		{name: "exact value limit", body: exactBody, wantName: exactName},
		{name: "value exceeds limit", body: " " + exactBody, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "exact trailing limit", body: emptyObject + strings.Repeat(" ", maxJSONBodySize-len(emptyObject))},
		{name: "trailing exceeds limit", body: emptyObject + strings.Repeat(" ", maxJSONBodySize-len(emptyObject)+1), wantStatus: http.StatusRequestEntityTooLarge},
		{name: "second null", body: emptyObject + " null", wantStatus: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := &jsonCountingBody{Reader: strings.NewReader(tc.body), chunk: 97}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
			r.Body, r.ContentLength = body, -1
			w := httptest.NewRecorder()
			var dst struct {
				Name string `json:"name"`
			}
			ok := decodeJSONBody(w, r, &dst)
			if ok != (tc.wantStatus == 0) {
				t.Fatalf("accepted = %v, want %v; response = %d %s", ok, tc.wantStatus == 0, w.Code, w.Body.String())
			}
			if ok {
				if dst.Name != tc.wantName || w.Body.Len() != 0 || len(w.Header()) != 0 {
					t.Fatal("successful decode changed the payload or wrote a response")
				}
			} else {
				assertJSONDecodeResponse(t, w, tc.wantStatus)
			}
			if body.readBytes > maxJSONBodySize+1 || body.closed {
				t.Fatalf("reader consumed %d bytes, closed=%v", body.readBytes, body.closed)
			}
			if err := r.Body.Close(); err != nil || !body.closed {
				t.Fatalf("bounded reader failed to forward Close: %v", err)
			}
		})
	}
}

func TestDecodeJSONBodyReadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prefix     string
		err        error
		wantStatus int
	}{
		{name: "initial read", err: errors.New("private read detail"), wantStatus: http.StatusBadRequest},
		{name: "after a value", prefix: `{}`, err: io.ErrUnexpectedEOF, wantStatus: http.StatusBadRequest},
		{name: "wrapped size error", err: fmt.Errorf("private limit detail: %w", &http.MaxBytesError{Limit: maxJSONBodySize}), wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := io.MultiReader(strings.NewReader(tc.prefix), iotest.ErrReader(tc.err))
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", body)
			w := httptest.NewRecorder()
			var dst json.RawMessage
			if decodeJSONBody(w, r, &dst) {
				t.Fatal("accepted a failed body read")
			}
			assertJSONDecodeResponse(t, w, tc.wantStatus)
		})
	}
}

func TestWriteJSONResponseContract(t *testing.T) {
	t.Parallel()

	const serverError = `{"error":"internal server error","code":"server_error"}` + "\n"
	tests := []struct {
		name   string
		value  any
		status int
		body   string
	}{
		{name: "escaped text", value: map[string]string{"text": "<script>&\u2028\u2029\n"}, status: http.StatusCreated, body: `{"text":"\u003cscript\u003e\u0026\u2028\u2029\n"}` + "\n"},
		{name: "nil", status: http.StatusCreated, body: "null\n"},
		{name: "nil slice", value: []string(nil), status: http.StatusCreated, body: "null\n"},
		{name: "empty slice", value: []string{}, status: http.StatusCreated, body: "[]\n"},
		{name: "raw JSON", value: json.RawMessage(` {"ok": true} `), status: http.StatusCreated, body: "{\"ok\":true}\n"},
		{name: "unsupported type", value: func() {}, status: http.StatusInternalServerError, body: serverError},
		{name: "unsupported number", value: math.NaN(), status: http.StatusInternalServerError, body: serverError},
		{name: "marshaler error", value: jsonMarshalFailure{}, status: http.StatusInternalServerError, body: serverError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Request-ID", "kept")
			writeJSON(w, http.StatusCreated, tc.value)
			assertJSONResponse(t, w, tc.status, tc.body)
			if w.Result().Header.Get("X-Request-ID") != "kept" {
				t.Error("unrelated response header was lost")
			}
		})
	}
}

func TestWriteJSONStopsAfterBodyFailure(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("connection failed")
	tests := []struct {
		name string
		n    int
		err  error
	}{
		{name: "no bytes", err: writeErr},
		{name: "partial body", n: 3, err: writeErr},
		{name: "full body with error", n: -1, err: writeErr},
		// A short nil-error write violates io.Writer, but must not get a newline either.
		{name: "defensive short write", n: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &jsonWriteProbe{header: make(http.Header)}
			w.result = func(p []byte) (int, error) {
				if tc.n < 0 {
					return len(p), tc.err
				}
				return min(tc.n, len(p)), tc.err
			}
			writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
			if w.writes != 1 {
				t.Fatalf("Write called %d times after body failure, want 1", w.writes)
			}
			if len(w.statuses) != 1 || w.statuses[0] != http.StatusCreated {
				t.Fatalf("statuses = %v; must not try replacing an already committed response", w.statuses)
			}
		})
	}
}

func TestWriteJSONNewlineFailureDoesNotRetry(t *testing.T) {
	t.Parallel()

	w := &jsonWriteProbe{header: make(http.Header)}
	w.result = func(p []byte) (int, error) {
		if w.writes == 2 {
			if string(p) != "\n" {
				t.Errorf("second write = %q, want newline", p)
			}
			return 0, io.ErrClosedPipe
		}
		return len(p), nil
	}
	writeJSON(w, http.StatusOK, true)
	if w.writes != 2 || len(w.statuses) != 1 || w.statuses[0] != http.StatusOK {
		t.Fatalf("writes=%d, statuses=%v after newline failure", w.writes, w.statuses)
	}
}

func TestWriteJSONWithGzip(t *testing.T) {
	t.Parallel()

	for _, size := range []int{8, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			value := strings.Repeat("x", size)
			want := "\"" + value + "\"\n"
			handler := middleware.Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusCreated, value)
			}))
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			res := w.Result()
			defer closeJSONTestReader(t, res.Body)
			var reader io.Reader = res.Body
			if size > 1400 {
				if res.Header.Get("Content-Encoding") != "gzip" {
					t.Fatal("large JSON response was not compressed")
				}
				zr, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer closeJSONTestReader(t, zr)
				reader = zr
			} else if res.Header.Get("Content-Encoding") != "" {
				t.Fatal("small JSON response was compressed")
			}
			got, err := io.ReadAll(io.LimitReader(reader, int64(len(want)+1)))
			if err != nil || string(got) != want || res.StatusCode != http.StatusCreated {
				t.Fatalf("JSON/gzip response: status=%d, bytes=%d, err=%v", res.StatusCode, len(got), err)
			}
			assertJSONHeaders(t, res.Header)
		})
	}
}

func TestRequireProfileDepsContract(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	pd := &profileDeps{refs: 1}
	w := httptest.NewRecorder()
	if got := requireProfileDeps(w, withProfileDeps(r, pd)); got != pd || pd.refs != 1 {
		t.Fatal("profile lookup changed identity or reference ownership")
	}
	if w.Body.Len() != 0 || len(w.Header()) != 0 {
		t.Fatal("successful lookup wrote a response")
	}
	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{name: "missing", req: r},
		{name: "typed nil", req: withProfileDeps(r, nil)},
		{name: "nil request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			if requireProfileDeps(w, tc.req) != nil {
				t.Fatal("missing lookup returned a profile")
			}
			assertJSONResponse(t, w, http.StatusInternalServerError, `{"error":"profile not available","code":"server_error"}`+"\n")
		})
	}
}

func TestHealthHandlerContract(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	healthHandler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", nil))
	assertJSONResponse(t, w, http.StatusOK, "{\"status\":\"ok\"}\n")
}

func FuzzDecodeJSONBody(f *testing.F) {
	for _, seed := range []string{
		"", `{}`, `null`, `[true,1,"日本語"]`, `{} {}`, `{"x":`,
		"\"" + strings.Repeat("x", maxJSONBodySize-2) + "\"",
		"0" + strings.Repeat(" ", maxJSONBodySize),
	} {
		f.Add([]byte(seed), uint8(96))
	}
	f.Fuzz(func(t *testing.T, data []byte, step uint8) {
		if len(data) > maxJSONBodySize+1 {
			t.Skip("bounded request corpus")
		}
		body := &jsonCountingBody{Reader: bytes.NewReader(data), chunk: int(step) + 1}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
		r.Body, r.ContentLength = body, -1
		w := httptest.NewRecorder()
		var dst json.RawMessage
		ok := decodeJSONBody(w, r, &dst)
		want := len(data) <= maxJSONBodySize && json.Valid(data)
		if ok != want {
			t.Fatalf("accepted=%v, want=%v; input bytes=%d, read=%d, status=%d", ok, want, len(data), body.readBytes, w.Code)
		}
		if body.readBytes > maxJSONBodySize+1 {
			t.Fatalf("read %d bytes, exceeded limit probe", body.readBytes)
		}
		if ok {
			if !bytes.Equal(dst, bytes.TrimSpace(data)) || w.Body.Len() != 0 {
				t.Fatal("successful decode changed the JSON value or wrote a response")
			}
		} else {
			if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("unexpected rejection status %d", w.Code)
			}
			assertJSONDecodeResponse(t, w, w.Code)
		}
	})
}

func assertJSONDecodeResponse(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	body := `{"error":"invalid JSON body","code":"invalid_body"}` + "\n"
	if status == http.StatusRequestEntityTooLarge {
		body = `{"error":"request body too large","code":"too_large"}` + "\n"
	}
	assertJSONResponse(t, w, status, body)
}

func assertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	res := w.Result()
	defer closeJSONTestReader(t, res.Body)
	if res.StatusCode != status || w.Body.String() != body {
		t.Fatalf("response = %d %q, want %d %q", res.StatusCode, w.Body.String(), status, body)
	}
	assertJSONHeaders(t, res.Header)
}

func closeJSONTestReader(t *testing.T, r io.Closer) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Errorf("closing response reader: %v", err)
	}
}

func assertJSONHeaders(t *testing.T, h http.Header) {
	t.Helper()
	if h.Get("Content-Type") != "application/json; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("JSON security headers = %v", h)
	}
}

type jsonCountingBody struct {
	io.Reader
	chunk     int
	readBytes int
	closed    bool
}

func (b *jsonCountingBody) Read(p []byte) (int, error) {
	if b.chunk > 0 && len(p) > b.chunk {
		p = p[:b.chunk]
	}
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}

func (b *jsonCountingBody) Close() error {
	b.closed = true
	return nil
}

type jsonMarshalFailure struct{}

func (jsonMarshalFailure) MarshalJSON() ([]byte, error) {
	return nil, errors.New("private marshal detail")
}

type jsonWriteProbe struct {
	header   http.Header
	statuses []int
	writes   int
	result   func([]byte) (int, error)
}

func (w *jsonWriteProbe) Header() http.Header { return w.header }

func (w *jsonWriteProbe) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}

func (w *jsonWriteProbe) Write(p []byte) (int, error) {
	w.writes++
	return w.result(p)
}
