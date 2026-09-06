package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
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
		{name: "unstamped build", wantVer: "dev", wantDate: "unknown"},
		{
			name:     "version only",
			build:    BuildInfo{Version: "v1.2.0"},
			wantVer:  "v1.2.0",
			wantDate: "unknown",
		},
		{
			name:     "date only",
			build:    BuildInfo{BuildDate: "2026-08-16T18:24:00Z"},
			wantVer:  "dev",
			wantDate: "2026-08-16T18:24:00Z",
		},
		{
			name:     "JSON escaping and Unicode",
			build:    BuildInfo{Version: `v1.2.0+"كتاب"\candidate`, BuildDate: "<unknown>\n&"},
			wantVer:  `v1.2.0+"كتاب"\candidate`,
			wantDate: "<unknown>\n&",
		},
		{
			name:     "nonempty stamps stay opaque",
			build:    BuildInfo{Version: " preview build ", BuildDate: "not-a-date"},
			wantVer:  " preview build ",
			wantDate: "not-a-date",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps := &Dependencies{Build: tc.build}
			recorder := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/version", nil)
			versionHandler(deps)(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s",
					recorder.Code, http.StatusOK, recorder.Body.String())
			}
			assertVersionHeaders(t, recorder.Header())
			assertVersionJSON(t, recorder.Body.Bytes(), tc.wantVer, tc.wantDate)
			if deps.Build != tc.build {
				t.Errorf("shared build metadata changed: got %+v, want %+v", deps.Build, tc.build)
			}
		})
	}
}

func TestVersionHandlerConcurrentRequests(t *testing.T) {
	t.Parallel()

	// Defaults belong to the response, not the shared Dependencies. Concurrent
	// callers must neither race on normalization nor replace linker metadata
	// with values supplied by the request.
	deps := &Dependencies{}
	handler := versionHandler(deps)
	t.Cleanup(func() {
		if deps.Build != (BuildInfo{}) {
			t.Errorf("shared build metadata changed: %+v", deps.Build)
		}
	})

	for i := range 16 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/api/version?version=client-supplied&buildDate=client-supplied", nil)
			recorder := httptest.NewRecorder()
			handler(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
			}
			assertVersionHeaders(t, recorder.Header())
			assertVersionJSON(t, recorder.Body.Bytes(), "dev", "unknown")
		})
	}
}

func TestVersionRouteRequiresSession(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, token := range []string{"", "unknown-session", "expired-session"} {
			t.Run(method+"/"+token, func(t *testing.T) {
				t.Parallel()

				deps := &Dependencies{
					Build:    BuildInfo{Version: "private-build", BuildDate: "private-stamp"},
					sessions: newSessionStore(nil),
				}
				deps.sessions.data["expired-session"] = session{
					profile:       "reader",
					expiry:        time.Now().Add(-time.Hour),
					verifiedUntil: time.Now().Add(time.Hour),
				}
				handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
				req := httptest.NewRequestWithContext(t.Context(), method, "/api/version", nil)
				if token != "" {
					req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
				}
				// A profile context alone must not bypass the real session gate,
				// including GET's implicit HEAD route.
				req = withProfileDeps(req, &profileDeps{})
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
				}
				var response map[string]string
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if len(response) != 2 || response["code"] != "unauthenticated" || response["error"] != "not logged in" {
					t.Errorf("error response = %#v, want only the unauthenticated error", response)
				}
			})
		}
	}
}

func TestVersionRouteHTTP(t *testing.T) {
	t.Parallel()

	// Seed an already-open profile without DB/book resources: the build stamp
	// is process-wide. Keep one unrelated borrow outstanding so an extra
	// handler release cannot be hidden by the non-positive-ref guard.
	pd := &profileDeps{refs: 1}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	t.Cleanup(func() {
		pd.lifetimeMu.Lock()
		refs := pd.refs
		pd.lifetimeMu.Unlock()
		if refs != 1 {
			t.Errorf("profile refs = %d after requests, want the original borrow", refs)
		}
		if refs > 0 {
			pd.release()
		}
	})
	pm := NewProfileManager(t.TempDir())
	pm.open["reader"] = pd
	deps := &Dependencies{
		Build:      BuildInfo{Version: "v1.2.0", BuildDate: "2026-08-16T18:24:00Z"},
		ProfileMgr: pm,
		sessions:   newSessionStore(nil),
	}
	token, _, err := deps.sessions.create(t.Context(), "reader", false)
	if err != nil {
		t.Fatal(err)
	}
	deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))

	server := httptest.NewServer(NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler()))
	// Close and join the server before checking the profile's outstanding ref.
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	for _, tc := range []struct {
		method string
		status int
	}{
		{http.MethodGet, http.StatusOK},
		{http.MethodHead, http.StatusOK},
		{http.MethodPost, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.method, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL+"/api/version", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Errorf("close response body: %v", err)
				}
			}()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", response.StatusCode, tc.status, body)
			}
			if tc.status == http.StatusMethodNotAllowed {
				if got := response.Header.Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q, want GET, HEAD", got)
				}
				return
			}
			assertVersionHeaders(t, response.Header)
			if tc.method == http.MethodHead {
				if len(body) != 0 {
					t.Errorf("HEAD body = %q, want empty", body)
				}
				return
			}
			assertVersionJSON(t, body, "v1.2.0", "2026-08-16T18:24:00Z")
		})
	}
}

func assertVersionHeaders(t *testing.T, header http.Header) {
	t.Helper()
	// A restart can put a different binary behind a tab that never reloads,
	// so the answer must not be cached past revalidation.
	for name, want := range map[string]string{
		"Cache-Control":          "private, no-cache",
		"Content-Type":           "application/json; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func assertVersionJSON(t *testing.T, body []byte, wantVersion, wantDate string) {
	t.Helper()
	// Decode independently of versionResponse: matching Go struct tags (and
	// case-insensitive decoding) could hide a wire-key change from TypeScript.
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, body)
	}
	if len(response) != 2 || response["version"] != wantVersion || response["buildDate"] != wantDate {
		t.Errorf("response = %#v, want only version=%q and buildDate=%q", response, wantVersion, wantDate)
	}
}
