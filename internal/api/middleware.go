package api

import (
	"encoding/json"
	"errors"
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
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return false
	}
	return true
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

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
	// Append the newline to the marshaled slice before the single Write call
	// to avoid a second syscall and a second chunk through the gzip compressor.
	data = append(data, '\n')
	_, _ = w.Write(data)
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
