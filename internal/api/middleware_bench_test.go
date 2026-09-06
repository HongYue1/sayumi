package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"sayumi/internal/api/middleware"
)

// BenchmarkJSONResponses includes marshaling, the explicit status write, the
// newline and complete recorder output, with and without gzip. It is not a
// network or database benchmark. Keep the same fixtures/harness for comparisons.
func BenchmarkJSONResponses(b *testing.B) {
	type book struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Author string `json:"author"`
	}
	books := make([]book, 128)
	for i := range books {
		books[i] = book{
			ID:     fmt.Sprintf("book-%03d", i),
			Title:  fmt.Sprintf("Sayumi book %03d — 日本語 & reading", i),
			Author: fmt.Sprintf("Author %02d", i%16),
		}
	}
	for _, tc := range []struct {
		name     string
		value    any
		withGzip bool
	}{
		{name: "SmallPlain", value: map[string]string{"status": "ok"}},
		{name: "LargePlain", value: books},
		{name: "SmallGzip", value: map[string]string{"status": "ok"}, withGzip: true},
		{name: "LargeGzip", value: books, withGzip: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			want, err := json.Marshal(tc.value)
			if err != nil {
				b.Fatal(err)
			}
			want = append(want, '\n')
			var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, tc.value)
			})
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.withGzip {
				handler = middleware.Gzip(handler)
				r.Header.Set("Accept-Encoding", "gzip")
			}
			// Untimed warm-up also validates the complete representation. Timed
			// iterations allocate their own recorder and include gzip finalization.
			warm := httptest.NewRecorder()
			handler.ServeHTTP(warm, r)
			got := warm.Body.Bytes()
			// Use the committed snapshot, not the mutable handler header map.
			if warm.Result().Header.Get("Content-Encoding") == "gzip" {
				zr, err := gzip.NewReader(bytes.NewReader(got))
				if err != nil {
					b.Fatal(err)
				}
				got, err = io.ReadAll(zr)
				closeErr := zr.Close()
				if err != nil || closeErr != nil {
					b.Fatalf("decode warm response: %v; close: %v", err, closeErr)
				}
			}
			if warm.Code != http.StatusOK || !bytes.Equal(got, want) {
				b.Fatal("warm response did not match the JSON fixture")
			}
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
			}
		})
	}
}
