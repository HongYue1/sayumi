package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestNewHandlerSeparatesAPIRoutesFromSPA(t *testing.T) {
	t.Parallel()

	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("spa shell"))
	})
	handler := NewHandler(&Dependencies{}, http.NotFoundHandler(), staticHandler)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantSPA    bool
	}{
		{name: "unknown API path", method: http.MethodGet, path: "/api/missing", wantStatus: http.StatusNotFound},
		{name: "API root", method: http.MethodGet, path: "/api", wantStatus: http.StatusNotFound},
		{name: "wrong API method", method: http.MethodPost, path: "/api/books", wantStatus: http.StatusMethodNotAllowed},
		{name: "SPA deep link", method: http.MethodGet, path: "/reader/book-1", wantStatus: http.StatusOK, wantSPA: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			handler.ServeHTTP(recorder, req)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			gotSPA := strings.Contains(recorder.Body.String(), "spa shell")
			if gotSPA != tc.wantSPA {
				t.Errorf("SPA response = %v, want %v; body = %s", gotSPA, tc.wantSPA, recorder.Body.String())
			}
		})
	}
}

func TestNewHandlerProtectsOnlyUserFonts(t *testing.T) {
	t.Parallel()

	const token = "0123456789abcdef"
	deps := &Dependencies{fontToken: token}
	fontHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := NewHandler(deps, fontHandler, http.NotFoundHandler())

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "embedded font stays public", path: "/fonts/Literata.woff2", wantStatus: http.StatusOK},
		{name: "missing user token", path: "/fonts/user/Family/Regular.woff2", wantStatus: http.StatusNotFound},
		{name: "wrong user token", path: "/fonts/user/Family/Regular.woff2?token=wrong", wantStatus: http.StatusNotFound},
		{
			name:       "extra query data rejected",
			path:       "/fonts/user/Family/Regular.woff2?token=" + token + "&x=1",
			wantStatus: http.StatusNotFound,
		},
		{name: "valid user token", path: "/fonts/user/Family/Regular.woff2?token=" + token, wantStatus: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body = %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestNewUserFontToken(t *testing.T) {
	t.Parallel()

	first, err := newUserFontToken()
	if err != nil {
		t.Fatalf("newUserFontToken: %v", err)
	}
	second, err := newUserFontToken()
	if err != nil {
		t.Fatalf("newUserFontToken: %v", err)
	}
	if len(first) != userFontTokenBytes*2 {
		t.Errorf("token length = %d, want %d", len(first), userFontTokenBytes*2)
	}
	if first == second {
		t.Error("two generated user-font tokens matched")
	}
}

func TestListFontsReturnsPrivateAccessToken(t *testing.T) {
	t.Parallel()

	const token = "font-access-token"
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/fonts", nil)
	listFontsHandler(&Dependencies{fontToken: token})(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}
	var response fontsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.UserToken != token {
		t.Errorf("user token = %q, want %q", response.UserToken, token)
	}
	if response.User == nil {
		t.Error("user families encoded as null, want empty array")
	}
}

func TestNewHandlerBlocksCrossSiteWrites(t *testing.T) {
	t.Parallel()

	handler := NewHandler(&Dependencies{}, http.NotFoundHandler(), http.NotFoundHandler())
	tests := []struct {
		name, method, path, secFetch, origin, host string
		wantStatus                                 int
	}{
		{name: "cross-site create", method: http.MethodPost, path: "/api/auth/create", secFetch: "cross-site", wantStatus: http.StatusForbidden},
		{name: "cross-site login", method: http.MethodPost, path: "/api/auth/login", secFetch: "cross-site", wantStatus: http.StatusForbidden},
		{name: "same-site create", method: http.MethodPost, path: "/api/auth/create", secFetch: "same-site", wantStatus: http.StatusForbidden},
		{name: "same-site beacon", method: http.MethodPost, path: "/api/books/book/progress/beacon", secFetch: "same-site", wantStatus: http.StatusForbidden},
		{name: "same-site PUT", method: http.MethodPut, path: "/api/settings", secFetch: "same-site", wantStatus: http.StatusForbidden},
		{name: "same-site PATCH", method: http.MethodPatch, path: "/api/books/book", secFetch: "same-site", wantStatus: http.StatusForbidden},
		{name: "same-site DELETE", method: http.MethodDelete, path: "/api/auth/profile", secFetch: "same-site", wantStatus: http.StatusForbidden},
		{name: "guard before redirect", method: http.MethodPost, path: "/api//auth/create", secFetch: "cross-site", wantStatus: http.StatusForbidden},
		{name: "guard before method rejection", method: http.MethodPost, path: "/api/books", secFetch: "cross-site", wantStatus: http.StatusForbidden},
		{name: "same-origin write", method: http.MethodPost, path: "/api/auth/create", secFetch: "same-origin", wantStatus: http.StatusBadRequest},
		{name: "rewritten proxy host", method: http.MethodPost, path: "/api/auth/create", secFetch: "same-origin", origin: "https://reader.example", host: "127.0.0.1:8080", wantStatus: http.StatusBadRequest},
		{name: "user initiated", method: http.MethodPost, path: "/api/auth/create", secFetch: "none", wantStatus: http.StatusBadRequest},
		{name: "non-browser client", method: http.MethodPost, path: "/api/auth/create", wantStatus: http.StatusBadRequest},
		{name: "legacy matching origin", method: http.MethodPost, path: "/api/auth/create", origin: "http://example.com", wantStatus: http.StatusBadRequest},
		{name: "legacy foreign origin", method: http.MethodPost, path: "/api/auth/create", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "legacy different port", method: http.MethodPost, path: "/api/auth/create", origin: "http://example.com:8080", wantStatus: http.StatusForbidden},
		{name: "opaque origin", method: http.MethodPost, path: "/api/auth/create", origin: "null", wantStatus: http.StatusForbidden},
		{name: "malformed origin", method: http.MethodPost, path: "/api/auth/create", origin: "://", wantStatus: http.StatusForbidden},
		{name: "unrecognized fetch metadata", method: http.MethodPost, path: "/api/auth/create", secFetch: "invalid", wantStatus: http.StatusForbidden},
		{name: "metadata takes precedence", method: http.MethodPost, path: "/api/auth/create", secFetch: "same-site", origin: "http://example.com", wantStatus: http.StatusForbidden},
		{name: "cross-site GET", method: http.MethodGet, path: "/api/health", secFetch: "cross-site", wantStatus: http.StatusOK},
		{name: "cross-site HEAD", method: http.MethodHead, path: "/api/health", secFetch: "cross-site", wantStatus: http.StatusOK},
		{name: "cross-site preflight", method: http.MethodOptions, path: "/api/books/book/resources/font.woff2", secFetch: "cross-site", origin: "null", wantStatus: http.StatusNoContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// An invalid body is harmless if admitted; rejected requests must not
			// reach body parsing, authentication, or profile/throttle mutations.
			body := strings.NewReader("{")
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, body)
			req.Header.Set("Sec-Fetch-Site", tc.secFetch)
			req.Header.Set("Origin", tc.origin)
			if tc.host != "" {
				req.Host = tc.host
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden {
				var got apiError
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Code != "cross_site" || got.Error != "cross-site request blocked" {
					t.Errorf("denial contract changed: %+v", got)
				}
				if body.Len() != 1 || w.Header().Get("Set-Cookie") != "" {
					t.Error("rejected request consumed its body or changed a cookie")
				}
			}
		})
	}
}

func TestNewHandlerCrossOriginLogoutPreservesSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, site, origin string }{
		{name: "same-site", site: "same-site"},
		{name: "origin fallback", origin: "https://attacker.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := &Dependencies{sessions: newSessionStore(nil)}
			token, _, err := deps.sessions.create(t.Context(), "reader", false)
			if err != nil {
				t.Fatal(err)
			}
			handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
			req := authTestRequest(http.MethodPost, "/api/auth/logout", "", token)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden || w.Header().Get("Set-Cookie") != "" {
				t.Fatalf("cross-origin logout = %d, headers = %v", w.Code, w.Header())
			}
			if _, ok := deps.sessions.get(token); !ok {
				t.Fatal("cross-origin logout revoked the session")
			}

			req = authTestRequest(http.MethodPost, "/api/auth/logout", "", token)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("same-origin logout = %d: %s", w.Code, w.Body.String())
			}
			if _, ok := deps.sessions.get(token); ok {
				t.Error("same-origin logout did not revoke the session")
			}
		})
	}
}

func TestNewHandlerRequiresSessionOnPrivateRoutes(t *testing.T) {
	t.Parallel()
	handler := NewHandler(&Dependencies{sessions: newSessionStore(nil)}, http.NotFoundHandler(), http.NotFoundHandler())
	for _, route := range []string{
		"GET /api/version",
		"POST /api/auth/clone",
		"DELETE /api/auth/profile",
		"GET /api/books",
		"POST /api/books/upload",
		"POST /api/library/rescan",
		"GET /api/books/book",
		"PATCH /api/books/book",
		"DELETE /api/books/book",
		"PUT /api/books/book/cover",
		"POST /api/books/book/gofile",
		"GET /api/books/book/file",
		"GET /api/books/book/toc",
		"GET /api/books/book/cover",
		"GET /api/books/book/chapters/0",
		"GET /api/settings",
		"PUT /api/settings",
		"GET /api/presets",
		"POST /api/presets",
		"DELETE /api/presets/preset",
		"GET /api/themes",
		"POST /api/themes",
		"PUT /api/themes/theme",
		"DELETE /api/themes/theme",
		"GET /api/fonts",
		"POST /api/fonts/rescan",
		"GET /api/flairs",
		"POST /api/flairs",
		"DELETE /api/flairs/flair",
		"PUT /api/books/book/flair",
		"GET /api/books/book/progress",
		"PUT /api/books/book/progress",
		"POST /api/books/book/progress/beacon",
		"GET /api/books/book/search",
		"GET /api/books/book/bookmarks",
		"POST /api/books/book/bookmarks",
		"PATCH /api/books/book/bookmarks/mark",
		"DELETE /api/books/book/bookmarks/mark",
	} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			method, path, _ := strings.Cut(route, " ")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader("{}")))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
			}
			var got apiError
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Code != "unauthenticated" {
				t.Errorf("error code = %q, want unauthenticated", got.Code)
			}
		})
	}
}

func TestNewHandlerKeepsResourceBearerAccess(t *testing.T) {
	t.Parallel()
	deps, _, _ := newResourceTestDeps(t)
	handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
	// net/http suppresses HEAD bodies on the wire; a recorder does not.
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for _, tc := range []struct {
		name, method, token string
		wantStatus          int
	}{
		{name: "missing bearer", method: http.MethodGet, wantStatus: http.StatusUnauthorized},
		{name: "wrong bearer", method: http.MethodGet, token: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "iframe GET", method: http.MethodGet, token: resourceTestHash, wantStatus: http.StatusOK},
		{name: "iframe HEAD", method: http.MethodHead, token: resourceTestHash, wantStatus: http.StatusOK},
		{name: "public preflight", method: http.MethodOptions, wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Do not set PathValue by hand: this checks the router's wildcard
			// binding as well as the deliberate session-middleware bypass.
			req, err := http.NewRequestWithContext(t.Context(), tc.method,
				server.URL+"/api/books/"+resourceTestBookID+"/resources/OPS/picture.svg?token="+tc.token, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "null")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := res.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", res.StatusCode, tc.wantStatus, body)
			}
			if tc.method == http.MethodGet && tc.wantStatus == http.StatusOK && string(body) != resourceTestSVG {
				t.Errorf("resource body = %q", body)
			}
			if tc.method != http.MethodGet && len(body) != 0 {
				t.Errorf("%s wrote a body: %q", tc.method, body)
			}
		})
	}
}

func TestDependenciesRestoreSessions(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
		t.Fatal(err)
	}
	remembered, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	temporary, _, err := deps.sessions.create(t.Context(), "reader", false)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewDependencies(deps.ProfilesDB, deps.ProfileMgr, deps.LibraryRoot, deps.Fonts)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreSessions(); err != nil {
		t.Fatal(err)
	}
	if sess, ok := restored.sessions.get(remembered); !ok || sess.profile != "reader" || !sess.remember || !sess.verifiedUntil.IsZero() {
		t.Errorf("restored session = %+v, present = %v", sess, ok)
	}
	if _, ok := restored.sessions.get(temporary); ok {
		t.Error("temporary session survived a restart")
	}
	if restored.fontToken == deps.fontToken || !validUserFontToken("token="+restored.fontToken, restored.fontToken) {
		t.Error("restart did not generate a fresh usable font token")
	}
}

func TestDependenciesBackgroundMaintenance(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var sweeps atomic.Int64
		deps := &Dependencies{
			sessions: newSessionStore(&authTestPersistence{
				pruneSessions: func(got context.Context, _ time.Time) error {
					if got != ctx {
						t.Error("maintenance lost its cancellation context")
					}
					sweeps.Add(1)
					return nil
				},
			}),
			throttle: newLoginThrottle(),
		}
		deps.sessions.data["expired"] = session{expiry: time.Now().Add(-time.Second)}
		key := loginThrottleKey{profile: "reader", client: "192.0.2.1"}
		deps.throttle.entries[key] = &throttleEntry{lastSeen: time.Now().Add(-loginWindow)}
		deps.throttle.clientEntries[key.client] = 1
		done := make(chan struct{})
		go func() {
			defer close(done)
			deps.StartBackgroundTasks(ctx)
		}()
		synctest.Wait()
		if sweeps.Load() != 0 {
			t.Fatal("maintenance ran before the first tick")
		}
		for want := int64(1); want <= 2; want++ {
			time.Sleep(5 * time.Minute)
			synctest.Wait()
			if got := sweeps.Load(); got != want {
				t.Fatalf("maintenance sweeps = %d, want %d", got, want)
			}
		}
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("maintenance did not finish after cancellation")
		}
		if len(deps.sessions.data) != 0 || len(deps.throttle.entries) != 0 || len(deps.throttle.clientEntries) != 0 {
			t.Error("maintenance did not prune session and throttle state")
		}
		time.Sleep(5 * time.Minute)
		if sweeps.Load() != 2 {
			t.Error("maintenance continued after shutdown")
		}
	})
}
