package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The About sheet prints whatever this endpoint returns, so the contract worth
// pinning is that a binary built outside the release scripts still names itself
// rather than rendering a blank line.
func TestVersionHandlerReportsBuildMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		build    BuildInfo
		wantVer  string
		wantDate string
	}{
		{
			name:     "linker stamped",
			build:    BuildInfo{Version: "v1.2.0", BuildDate: "2026-08-16T18:24:00Z"},
			wantVer:  "v1.2.0",
			wantDate: "2026-08-16T18:24:00Z",
		},
		{name: "unstamped build", wantVer: unsetVersion, wantDate: unsetBuildDate},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			versionHandler(&Dependencies{Build: tc.build})(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s",
					recorder.Code, http.StatusOK, recorder.Body.String())
			}
			// A restart can put a different binary behind a tab that never
			// reloads, so the answer must not be cached past revalidation.
			if got := recorder.Header().Get("Cache-Control"); got != "private, no-cache" {
				t.Errorf("Cache-Control = %q, want private, no-cache", got)
			}

			var response versionResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Version != tc.wantVer {
				t.Errorf("version = %q, want %q", response.Version, tc.wantVer)
			}
			if response.BuildDate != tc.wantDate {
				t.Errorf("build date = %q, want %q", response.BuildDate, tc.wantDate)
			}
		})
	}
}
