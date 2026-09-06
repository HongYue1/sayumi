package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// maxJSONBodySize caps all JSON API request bodies. 64 KB is generous for any
// structured request (settings, progress, bookmarks, auth) and keeps even an
// unauthenticated endpoint from buffering an unbounded stream.
const maxJSONBodySize = 64 << 10 // 64 KB

// decodeJSONBody limits r.Body to maxJSONBodySize, decodes JSON into dst, and
// writes the appropriate HTTP error response on failure.
// Returns true on success; on false the handler must return immediately.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		writeJSONDecodeError(w, err)
		return false
	}
	// A JSON request is exactly one value. Decode once more so trailing
	// non-whitespace data (including a second valid value) cannot be silently
	// ignored. This second pass uses the same bounded reader; a read beyond
	// the limit still maps to 413, while earlier syntax errors remain 400.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSONDecodeError(w, err)
		return false
	}
	return true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// jsonNewline is shared so writeJSON can emit it separately without potentially
// growing and copying the marshaled body just to append one byte.
var jsonNewline = []byte{'\n'}

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Marshal before writing headers so a marshal error can still return a
	// proper 500 instead of a truncated response after headers are committed.
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("JSON marshal failed", "err", err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error","code":"server_error"}` + "\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// Spare capacity in json.Marshal's result is not guaranteed. Keep the
	// newline separate to avoid a possible body copy; net/http and gzip
	// normally buffer these writes, and the newline does not request a flush.
	// After a failed or short body write, do not try to continue the response
	// or replace its already committed status with another error response.
	if n, err := w.Write(data); err != nil || n != len(data) {
		return
	}
	_, _ = w.Write(jsonNewline)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: message, Code: code})
}

func requireProfileDeps(w http.ResponseWriter, r *http.Request) *profileDeps {
	pd := profileDepsFromCtx(r)
	if pd == nil {
		writeError(w, http.StatusInternalServerError, "server_error", "profile not available")
		return nil
	}
	return pd
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
