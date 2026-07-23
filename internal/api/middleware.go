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
	// ignored. The same bounded reader remains in place, so oversized trailing
	// data still maps to 413 rather than a generic syntax error.
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

// jsonNewline is the trailing newline appended after every JSON response body.
// It is a shared package-level slice so writeJSON can emit it as a separate
// tiny write rather than reallocating the marshaled body to append one byte.
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
	// Write the body and its trailing newline as two writes instead of
	// append(data, '\n'): json.Marshal returns a len==cap slice, so appending
	// would reallocate and copy the entire body on every response. gzip buffers
	// internally (the 1-byte write is not a flush) and net/http's response bufio
	// coalesces both writes on the uncompressed path, so this stays a single
	// effective write without the per-response copy.
	_, _ = w.Write(data)
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
