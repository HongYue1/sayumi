package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"sayumi/internal/fonts"
)

func TestFontsHandlersEmptyCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, tc := range []struct {
		name    string
		scanner *fonts.Scanner
	}{
		{name: "nil scanner"},
		{name: "disabled scanner", scanner: fonts.NewScanner("")},
		{name: "missing root", scanner: fonts.NewScanner(filepath.Join(root, "missing"))},
		{name: "empty root", scanner: fonts.NewScanner(root)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps := &Dependencies{Fonts: tc.scanner, fontToken: "server-font-token"}
			for _, endpoint := range []struct {
				name    string
				method  string
				path    string
				handler http.HandlerFunc
			}{
				{"list", http.MethodGet, "/api/fonts", listFontsHandler(deps)},
				{"rescan", http.MethodPost, "/api/fonts/rescan", rescanFontsHandler(deps)},
			} {
				t.Run(endpoint.name, func(t *testing.T) {
					r := httptest.NewRequestWithContext(t.Context(), endpoint.method, endpoint.path+"?token=caller-token", nil)
					w := httptest.NewRecorder()
					endpoint.handler(w, r)
					if user := assertFontsResponse(t, w, deps.fontToken); len(user) != 0 {
						t.Errorf("user families = %s, want []", user)
					}
				})
			}
		})
	}
}

func TestFontsHandlersCacheAndRescan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFontsTestFile(t, root, "Family/Regular.woff2", "fixture")
	// Explicit metrics keep this an HTTP contract test, not another font parser
	// fixture. The label must survive JSON escaping without exposing Dir.
	writeFontsTestFile(t, root, "Family/family.json", `{
		"label":"A \"كتاب\" <&>", "category":"sans-serif", "variable":false,
		"metrics":{"unitsPerEm":1000,"xHeight":0.5,"capHeight":0.7,"ascent":0.8,"descent":0.2,"lineGap":0}
	}`)
	deps := &Dependencies{Fonts: fonts.NewScanner(root), fontToken: "server-font-token"}
	request := func(method string) []json.RawMessage {
		t.Helper()
		path, handler := "/api/fonts", listFontsHandler(deps)
		if method == http.MethodPost {
			path, handler = "/api/fonts/rescan", rescanFontsHandler(deps)
		}
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequestWithContext(t.Context(), method, path, nil))
		return assertFontsResponse(t, w, deps.fontToken)
	}

	initial := request(http.MethodGet)
	if len(initial) != 1 {
		t.Fatalf("initial families = %s, want one", initial)
	}
	var got, want map[string]any
	if err := json.Unmarshal(initial[0], &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{
		"id":"user:Family","label":"A \"كتاب\" <&>","category":"sans-serif",
		"files":["Regular.woff2"],"variable":false,
		"detected":{"regular":"Regular.woff2","italic":"","bold":"","boldItalic":""},
		"metrics":{"unitsPerEm":1000,"xHeight":0.5,"capHeight":0.7,"ascent":0.8,"descent":0.2,"lineGap":0}
	}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("family JSON = %#v, want %#v", got, want)
	}
	snapshot := deps.Fonts.Families()
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	writeFontsTestFile(t, root, "Family/Bold.woff2", "new fixture")
	if cached := request(http.MethodGet); !reflect.DeepEqual(cached, initial) {
		t.Errorf("GET rescanned the directory: got %s, want %s", cached, initial)
	}
	refreshed := request(http.MethodPost)
	if len(refreshed) != 1 {
		t.Fatalf("rescanned families = %s, want one", refreshed)
	}
	var family fonts.Family
	if err := json.Unmarshal(refreshed[0], &family); err != nil {
		t.Fatal(err)
	}
	if family.ID != "user:Family" || !slices.Equal(family.Files, []string{"Bold.woff2", "Regular.woff2"}) || family.Detected.Bold != "Bold.woff2" {
		t.Errorf("rescan did not publish the new face: %+v", family)
	}
	if cached := request(http.MethodGet); !reflect.DeepEqual(cached, refreshed) {
		t.Errorf("GET after rescan = %s, want %s", cached, refreshed)
	}

	// A response can still be encoding an old shared snapshot during a rescan.
	// Publishing the new catalog must not mutate the old slice or nested data.
	after, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || snapshot[0].Dir != "Family" {
		t.Errorf("old snapshot changed: before %s, after %s", before, after)
	}

	if err := os.RemoveAll(filepath.Join(root, "Family")); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		if user := request(method); len(user) != 0 {
			t.Errorf("%s after removal = %s, want []", method, user)
		}
	}
}

func TestFontsRoutesSessionAndMethodGates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		cookie string
		site   string
		status int
	}{
		{"list session", http.MethodGet, "/api/fonts", "active-session", "", http.StatusOK},
		{"rescan session", http.MethodPost, "/api/fonts/rescan", "active-session", "", http.StatusOK},
		{"list missing session", http.MethodGet, "/api/fonts", "", "", http.StatusUnauthorized},
		{"head missing session", http.MethodHead, "/api/fonts", "", "", http.StatusUnauthorized},
		{"list unknown session", http.MethodGet, "/api/fonts", "unknown-session", "", http.StatusUnauthorized},
		{"list expired session", http.MethodGet, "/api/fonts", "expired-session", "", http.StatusUnauthorized},
		{"rescan missing session", http.MethodPost, "/api/fonts/rescan", "", "", http.StatusUnauthorized},
		{"rescan unknown session", http.MethodPost, "/api/fonts/rescan", "unknown-session", "", http.StatusUnauthorized},
		{"rescan expired session", http.MethodPost, "/api/fonts/rescan", "expired-session", "", http.StatusUnauthorized},
		{"cross-site rescan", http.MethodPost, "/api/fonts/rescan", "active-session", "cross-site", http.StatusForbidden},
		{"wrong list method", http.MethodPost, "/api/fonts", "active-session", "", http.StatusMethodNotAllowed},
		{"wrong rescan method", http.MethodGet, "/api/fonts/rescan", "active-session", "", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			scanner := fonts.NewScanner(root)
			scanner.Families()
			writeFontsTestFile(t, root, "Family/Regular.woff2", "fixture")

			// Fonts are process-wide, so no profile DB is needed. Keep an
			// unrelated borrow outstanding to expose accidental double-release.
			pd := &profileDeps{refs: 1}
			pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
			t.Cleanup(pd.release)
			pm := NewProfileManager(t.TempDir())
			pm.open["reader"] = pd
			deps := &Dependencies{
				Fonts:      scanner,
				ProfileMgr: pm,
				sessions:   newSessionStore(nil),
				fontToken:  "private-font-token",
			}
			validUntil := time.Now().Add(time.Hour)
			deps.sessions.data["active-session"] = session{
				profile: "reader", expiry: validUntil, verifiedUntil: validUntil,
			}
			deps.sessions.data["expired-session"] = session{
				profile: "reader", expiry: time.Now().Add(-time.Hour), verifiedUntil: validUntil,
			}
			handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path+"?token="+deps.fontToken, nil)
			// The font-file bearer token and a preexisting profile context do
			// not authorize discovery; only the session cookie does.
			r.Header.Set("Authorization", "Bearer "+deps.fontToken)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.cookie})
			}
			r = withProfileDeps(r, pd)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.status, w.Body.String())
			}
			wantFamilies := 0
			if tc.status == http.StatusOK {
				if tc.method == http.MethodPost {
					wantFamilies = 1
				}
				if user := assertFontsResponse(t, w, deps.fontToken); len(user) != wantFamilies {
					t.Errorf("user families = %s, want %d", user, wantFamilies)
				}
			} else if bytes.Contains(w.Body.Bytes(), []byte(deps.fontToken)) {
				t.Error("rejected request disclosed the font token")
			}
			if user := scanner.Families(); len(user) != wantFamilies {
				t.Errorf("request unexpectedly changed the scanner cache: %+v", user)
			}
			pd.lifetimeMu.Lock()
			refs := pd.refs
			pd.lifetimeMu.Unlock()
			if refs != 1 {
				t.Errorf("profile refs = %d, want the original borrow", refs)
			}
		})
	}
}

func assertFontsResponse(t *testing.T, w *httptest.ResponseRecorder, token string) []json.RawMessage {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	for name, want := range map[string]string{
		"Cache-Control":          "private, no-store",
		"Content-Type":           "application/json; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// Decode the envelope independently: matching Go response tags can hide
	// a wire-key change from the TypeScript consumer.
	var response map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response) != 3 || response["user"] == nil || response["embedded"] == nil || response["userToken"] == nil {
		t.Fatalf("response keys = %v, want only user, embedded, userToken", response)
	}
	var gotToken string
	if err := json.Unmarshal(response["userToken"], &gotToken); err != nil || gotToken != token {
		t.Errorf("userToken = %s, want %q; error = %v", response["userToken"], token, err)
	}
	metrics := fonts.EmbeddedFaceMetrics()
	if len(metrics) == 0 {
		t.Fatal("embedded metrics catalog is empty")
	}
	wantMetrics, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response["embedded"], wantMetrics) {
		t.Error("embedded metrics differ from the measured catalog")
	}
	var user []json.RawMessage
	if err := json.Unmarshal(response["user"], &user); err != nil {
		t.Fatal(err)
	}
	if user == nil {
		t.Fatal("user families encoded as null, want an array")
	}
	return user
}

func writeFontsTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
