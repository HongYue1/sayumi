package fonts

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Requests reuse a warmed catalog and immutable inputs. Recorder allocation
// and response-body copying remain timed; socket/browser costs are not modeled.
func BenchmarkFontHandlerCP15(b *testing.B) {
	s := cp15BenchmarkScanner(b, 1)
	_, tag, ok := s.StatUserFont("Family00", "Regular.woff2")
	if !ok {
		b.Fatal("missing user fixture")
	}
	const embedded = "AtkinsonHyperlegibleNext-VariableFont.woff2"
	const userPath = "/user/Family00/Regular.woff2"
	for _, tc := range []struct {
		name, method, path, condition string
		status                        int
	}{
		{name: "EmbeddedGET", method: http.MethodGet, path: "/" + embedded, status: http.StatusOK},
		{name: "EmbeddedHEAD", method: http.MethodHead, path: "/" + embedded, status: http.StatusOK},
		{
			name:      "Embedded304",
			method:    http.MethodGet,
			path:      "/" + embedded,
			condition: fontETags[embedded],
			status:    http.StatusNotModified,
		},
		{name: "UserGET", method: http.MethodGet, path: userPath, status: http.StatusOK},
		{name: "UserHEAD", method: http.MethodHead, path: userPath, status: http.StatusOK},
		{name: "User304", method: http.MethodGet, path: userPath, condition: tag, status: http.StatusNotModified},
		{name: "UserConditionalMiss", method: http.MethodGet, path: userPath, condition: `"different"`, status: http.StatusOK},
		{name: "MissingGET", method: http.MethodGet, path: "/missing.woff2", status: http.StatusNotFound},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.condition != "" {
				r.Header.Set("If-None-Match", tc.condition)
			}
			h := Handler(s)
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.status {
					b.Fatalf("status %d, want %d", w.Code, tc.status)
				}
			}
		})
	}
}
