package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"sayumi/internal/storage"
)

func TestNormalizePresetName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		wantName string
		wantOK   bool
	}{
		{
			name:     "trims valid name",
			value:    "  Night  ",
			wantName: "Night",
			wantOK:   true,
		},
		{
			name:     "empty name",
			value:    "   ",
			wantName: "",
		},
		{
			name:     "Unicode whitespace",
			value:    "\u2003Night\u00a0",
			wantName: "Night",
			wantOK:   true,
		},
		{
			name:  "only Unicode whitespace",
			value: "\t\n\u2003\u00a0",
		},
		{
			name:     "sixty supplementary runes",
			value:    strings.Repeat("🌙", maxPresetNameLen),
			wantName: strings.Repeat("🌙", maxPresetNameLen),
			wantOK:   true,
		},
		{
			name:     "sixty one supplementary runes",
			value:    strings.Repeat("🌙", maxPresetNameLen+1),
			wantName: strings.Repeat("🌙", maxPresetNameLen+1),
		},
		{
			name:     "sixty ASCII characters",
			value:    strings.Repeat("a", maxPresetNameLen),
			wantName: strings.Repeat("a", maxPresetNameLen),
			wantOK:   true,
		},
		{
			name:     "sixty one ASCII characters",
			value:    strings.Repeat("a", maxPresetNameLen+1),
			wantName: strings.Repeat("a", maxPresetNameLen+1),
		},
		{
			name:     "sixty Arabic characters",
			value:    strings.Repeat("م", maxPresetNameLen),
			wantName: strings.Repeat("م", maxPresetNameLen),
			wantOK:   true,
		},
		{
			name:     "sixty one Arabic characters",
			value:    strings.Repeat("م", maxPresetNameLen+1),
			wantName: strings.Repeat("م", maxPresetNameLen+1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotName, gotOK := normalizePresetName(tc.value)
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if gotOK != tc.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, tc.wantOK)
			}
		})
	}
}

type presetTestClient struct {
	pd  *profileDeps
	mux *http.ServeMux
}

func newPresetTestClient(t *testing.T) *presetTestClient {
	t.Helper()
	// Reuse the real SQLite fixture and borrowed-reference checks from settings:
	// presets share that snapshot boundary, not the scanner or EPUB store.
	pd := settingsTestProfile(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presets", listPresetsHandler(nil))
	mux.HandleFunc("POST /api/presets", createPresetHandler(nil))
	mux.HandleFunc("DELETE /api/presets/{id}", deletePresetHandler(nil))
	return &presetTestClient{pd: pd, mux: mux}
}

func (c *presetTestClient) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	return c.serve(t, r)
}

func (c *presetTestClient) serve(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, withProfileDeps(r, c.pd))
	// authMiddleware owns this reference and releases it after ServeHTTP.
	// A handler release would let a concurrent profile close race a request.
	c.pd.lifetimeMu.Lock()
	refs := c.pd.refs
	c.pd.lifetimeMu.Unlock()
	if refs != 1 {
		t.Fatalf("preset handler changed borrowed refs: got %d, want 1", refs)
	}
	return w
}

func presetTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertPresetJSON(t *testing.T, w *httptest.ResponseRecorder, status int, want any) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, status, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	var got, expected any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(presetTestJSON(t, want)), &expected); err != nil {
		t.Fatal(err)
	}
	// Include the complete shape (no user ID, settings object rather than an
	// encoded string) without making JSON object-key order part of the API.
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("JSON = %s, want %s", w.Body.String(), presetTestJSON(t, want))
	}
}

func presetTestRecord(t *testing.T, id, userID string) storage.PresetRecord {
	t.Helper()
	return storage.PresetRecord{
		ID: id, UserID: userID, Name: "Saved <night>",
		SettingsJSON: presetTestJSON(t, settingsTestBase()),
		CreatedAt:    "2000-01-02 03:04:05", UpdatedAt: "2001-02-03 04:05:06",
	}
}

func presetTestResponse(p storage.PresetRecord) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "settings": json.RawMessage(p.SettingsJSON),
		"createdAt": p.CreatedAt, "updatedAt": p.UpdatedAt,
	}
}

func assertPresetRecords(t *testing.T, db *storage.DB, userID string, want []storage.PresetRecord) {
	t.Helper()
	got, err := db.ListPresetsContext(t.Context(), userID)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("stored presets for %q = %+v, %v; want %+v", userID, got, err, want)
	}
}

func TestPresetHandlersListAndDeleteIsolation(t *testing.T) {
	t.Parallel()
	c := newPresetTestClient(t)
	other := newPresetTestClient(t)
	assertPresetJSON(t, c.request(t, http.MethodGet, "/api/presets", ""), http.StatusOK, []any{})

	a := presetTestRecord(t, "preset_a", "default")
	b := presetTestRecord(t, "preset_b", "default")
	earlier := presetTestRecord(t, "preset_z", "default")
	earlier.CreatedAt = "1999-01-02 03:04:05"
	foreign := presetTestRecord(t, "preset_other", "other")
	// Reverse insertion and tied timestamps expose accidental handler sorting
	// or dropped rows; the storage contract is created_at ASC, id ASC.
	for _, rec := range []storage.PresetRecord{b, foreign, a, earlier} {
		if err := c.pd.DB.InsertPresetContext(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	otherRecord := a
	otherRecord.Name = "Other profile"
	if err := other.pd.DB.InsertPresetContext(t.Context(), otherRecord); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/presets?userID=other", nil)
	r.Header.Set("X-User-Id", "other")
	assertPresetJSON(t, c.serve(t, r), http.StatusOK, []any{
		presetTestResponse(earlier), presetTestResponse(a), presetTestResponse(b),
	})
	assertPresetJSON(t, other.request(t, http.MethodGet, "/api/presets", ""), http.StatusOK, []any{
		presetTestResponse(otherRecord),
	})

	for _, id := range []string{foreign.ID, "preset_missing", "preset_x%27%20OR%201=1--"} {
		w := c.request(t, http.MethodDelete, "/api/presets/"+id+"?userID=other", "")
		assertPresetJSON(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "preset not found"})
		assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{earlier, a, b})
		assertPresetRecords(t, c.pd.DB, "other", []storage.PresetRecord{foreign})
	}
	w := c.request(t, http.MethodDelete, "/api/presets/"+a.ID, "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("delete = %d %q, want empty 204", w.Code, w.Body.String())
	}
	assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{earlier, b})
	assertPresetRecords(t, c.pd.DB, "other", []storage.PresetRecord{foreign})
	assertPresetRecords(t, other.pd.DB, "default", []storage.PresetRecord{otherRecord})
	assertPresetJSON(t, c.request(t, http.MethodDelete, "/api/presets/"+a.ID, ""), http.StatusNotFound,
		apiError{Code: "not_found", Error: "preset not found"})
}

func TestPresetCreateSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	c := newPresetTestClient(t)
	active := settingsTestBase()
	assertSettingsResponse(t, settingsTestRequest(t, c.pd, http.MethodPut, presetTestJSON(t, active)), http.StatusOK, active)
	before, err := c.pd.DB.GetSettingsContext(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}

	// The wire fixture is independent of settingsJSON's tags. A preset must
	// preserve every setting, especially Auto/null versus an explicit zero,
	// while using the same normalization as PUT /api/settings.
	body := `{"name":"\u2003Night <>&\u00a0","id":"client-id","userID":"other","settings":{
		"fontSize":35,"fontFamily":" user:Reader ","lineHeight":1.75,
		"paragraphSpacing":0,"textIndent":null,"letterSpacing":-0.25,
		"contentWidth":80,"displayMode":" PAGED-TWO ","marginTop":0,
		"marginBottom":48,"marginSide":300,"preserveStyles":true,"preserveFonts":true,
		"justify":false,"hyphenation":true,"theme":" theme_Night ",
		"chapterTitleAlign":" CENTER ","chapterTitleSize":70,"chapterTitleSpacing":0,
		"chapterTitleFontFamily":" \t ","headingLetterSpacing":null,"headerSizesEnabled":true,
		"h1Size":80,"h2Size":70,"h3Size":60,"h4Size":50,"h5Size":40,"h6Size":30,
		"headerWeight":800,"textWeight":500,
		"fontRoles":{"user:Reader":{"regular":" Reg.ttf ","italic":" Italic.otf ",
		"bold":" ","boldItalic":" BI.ttf "},"empty":{"regular":" "}}
	}}`
	wantSettings := json.RawMessage(`{
		"fontSize":35,"fontFamily":"user:Reader","lineHeight":1.75,
		"paragraphSpacing":0,"textIndent":null,"letterSpacing":-0.25,
		"contentWidth":80,"displayMode":"paged-two","marginTop":0,
		"marginBottom":48,"marginSide":300,"preserveStyles":true,"preserveFonts":true,
		"justify":false,"hyphenation":true,"theme":"theme_Night",
		"chapterTitleAlign":"center","chapterTitleSize":70,"chapterTitleSpacing":0,
		"chapterTitleFontFamily":null,"headingLetterSpacing":null,"headerSizesEnabled":true,
		"h1Size":80,"h2Size":70,"h3Size":60,"h4Size":50,"h5Size":40,"h6Size":30,
		"headerWeight":800,"textWeight":500,
		"fontRoles":{"user:Reader":{"regular":"Reg.ttf","italic":"Italic.otf","boldItalic":"BI.ttf"}}
	}`)
	w := c.request(t, http.MethodPost, "/api/presets", body)
	var created presetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == "client-id" {
		t.Fatalf("preset ID was not generated: %q", created.ID)
	}
	if _, err := time.Parse(time.DateTime, created.CreatedAt); err != nil {
		t.Fatalf("invalid createdAt: %v", err)
	}
	if created.UpdatedAt != created.CreatedAt {
		t.Errorf("created timestamps differ: %+v", created)
	}
	want := map[string]any{
		"id": created.ID, "name": "Night <>&", "settings": wantSettings,
		"createdAt": created.CreatedAt, "updatedAt": created.CreatedAt,
	}
	assertPresetJSON(t, w, http.StatusCreated, want)
	assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{{
		ID: created.ID, UserID: "default", Name: "Night <>&", SettingsJSON: string(created.Settings),
		CreatedAt: created.CreatedAt, UpdatedAt: created.CreatedAt,
	}})
	assertPresetRecords(t, c.pd.DB, "other", nil)
	after, err := c.pd.DB.GetSettingsContext(t.Context(), "default")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("saving a preset changed active settings: %+v, %v", after, err)
	}
	assertPresetJSON(t, settingsTestRequest(t, c.pd, http.MethodPut, string(created.Settings)), http.StatusOK, wantSettings)
	// Changing active settings later must not rewrite the saved snapshot.
	assertSettingsResponse(t, settingsTestRequest(t, c.pd, http.MethodPut, presetTestJSON(t, active)), http.StatusOK, active)
	assertPresetJSON(t, c.request(t, http.MethodGet, "/api/presets", ""), http.StatusOK, []any{want})
}

func TestPresetCreateEmptyFontRoles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		roles string
	}{
		{name: "omitted"},
		{name: "null", roles: `,"fontRoles":null`},
		{name: "empty", roles: `,"fontRoles":{}`},
		{name: "pruned", roles: `,"fontRoles":{"empty":{"regular":" "}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newPresetTestClient(t)
			// Sixty non-BMP runes are within the name limit, not sixty bytes
			// or UTF-16 code units. Empty font roles always remain an object.
			name := strings.Repeat("🌙", maxPresetNameLen)
			body := `{"name":` + presetTestJSON(t, " "+name+" ") + `,"settings":{` +
				`"fontSize":26,"fontFamily":"literata","theme":"catppuccin","displayMode":"scroll"` + tc.roles + `}}`
			w := c.request(t, http.MethodPost, "/api/presets", body)
			var created presetResponse
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			assertPresetJSON(t, w, http.StatusCreated, map[string]any{
				"id": created.ID, "name": name, "settings": settingsTestBase(),
				"createdAt": created.CreatedAt, "updatedAt": created.UpdatedAt,
			})
			assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{{
				ID: created.ID, UserID: "default", Name: name, SettingsJSON: presetTestJSON(t, settingsTestBase()),
				CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt,
			}})
		})
	}
}

func TestPresetCreateRejectsWithoutMutation(t *testing.T) {
	t.Parallel()
	valid := `{"name":"Saved","settings":{"fontSize":26,"fontFamily":"literata","theme":"catppuccin","displayMode":"scroll"}}`
	invalidJSON := apiError{Code: "invalid_body", Error: "invalid JSON body"}
	invalidName := apiError{Code: "invalid_body", Error: "name must be 1-60 characters"}
	for _, tc := range []struct {
		name string
		body string
		want apiError
	}{
		{name: "empty body", want: invalidJSON},
		{name: "malformed", body: "{", want: invalidJSON},
		{name: "scalar body", body: "1", want: invalidJSON},
		{name: "null body", body: "null", want: invalidName},
		{name: "second value", body: valid + "{}", want: invalidJSON},
		{name: "trailing junk", body: valid + "x", want: invalidJSON},
		{name: "wrong name type", body: strings.Replace(valid, `"Saved"`, "1", 1), want: invalidJSON},
		{name: "blank name", body: strings.Replace(valid, "Saved", `\u2003`, 1), want: invalidName},
		{name: "long name", body: strings.Replace(valid, "Saved", strings.Repeat("🌙", 61), 1), want: invalidName},
		{
			name: "missing settings", body: `{"name":"Saved"}`,
			want: apiError{Code: "invalid", Error: "fontSize must be 10-50"},
		},
		{
			name: "null settings", body: `{"name":"Saved","settings":null}`,
			want: apiError{Code: "invalid", Error: "fontSize must be 10-50"},
		},
		{
			name: "bad font size", body: strings.Replace(valid, ":26", ":9", 1),
			want: apiError{Code: "invalid", Error: "fontSize must be 10-50"},
		},
		{
			name: "bad display mode", body: strings.Replace(valid, "scroll", "sideways", 1),
			want: apiError{Code: "invalid", Error: "displayMode must be scroll, paged, or paged-two"},
		},
		{
			name: "role traversal",
			body: strings.Replace(valid, `"scroll"`, `"scroll","fontRoles":{"user:Reader":{"regular":"../secret.ttf"}}`, 1),
			want: apiError{Code: "invalid", Error: "invalid font role file name"},
		},
		{
			name: "overflowing float", body: strings.Replace(valid, `"scroll"`, `"scroll","lineHeight":1e999`, 1),
			want: invalidJSON,
		},
		{name: "wrong settings type", body: `{"name":"Saved","settings":[]}`, want: invalidJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newPresetTestClient(t)
			original := presetTestRecord(t, "preset_original", "default")
			if err := c.pd.DB.InsertPresetContext(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			assertPresetJSON(t, c.request(t, http.MethodPost, "/api/presets", tc.body), http.StatusBadRequest, tc.want)
			assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{original})
		})
	}
}

func TestPresetCreateBodyLimit(t *testing.T) {
	t.Parallel()
	c := newPresetTestClient(t)
	body := presetTestJSON(t, createPresetBody{Name: "At limit", Settings: settingsTestBase()})
	atLimit := body + strings.Repeat(" ", maxJSONBodySize-len(body))
	w := c.request(t, http.MethodPost, "/api/presets", atLimit)
	if w.Code != http.StatusCreated {
		t.Fatalf("exact body limit = %d %s", w.Code, w.Body.String())
	}
	before, err := c.pd.DB.ListPresetsContext(t.Context(), "default")
	if err != nil || len(before) != 1 {
		t.Fatalf("accepted preset = %+v, %v", before, err)
	}
	// The second decoder pass must count whitespace too. The oversized name
	// exercises the first pass instead, before ordinary name validation.
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "trailing whitespace", body: atLimit + " "},
		{name: "inside value", body: `{"name":"` + strings.Repeat("a", maxJSONBodySize) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPresetJSON(t, c.request(t, http.MethodPost, "/api/presets", tc.body), http.StatusRequestEntityTooLarge,
				apiError{Code: "too_large", Error: "request body too large"})
			assertPresetRecords(t, c.pd.DB, "default", before)
		})
	}
}

func TestPresetHandlersMissingProfileAndCancellation(t *testing.T) {
	t.Parallel()
	body := presetTestJSON(t, createPresetBody{Name: "Saved", Settings: settingsTestBase()})
	for _, tc := range []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
		message string
	}{
		{
			name: "list", method: http.MethodGet, path: "/api/presets",
			handler: listPresetsHandler(nil), message: "failed to list presets",
		},
		{
			name: "create", method: http.MethodPost, path: "/api/presets",
			handler: createPresetHandler(nil), message: "failed to create preset",
		},
		{
			name: "delete", method: http.MethodDelete, path: "/api/presets/preset_original",
			handler: deletePresetHandler(nil), message: "failed to delete preset",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(body))
			w := httptest.NewRecorder()
			tc.handler(w, r)
			assertPresetJSON(t, w, http.StatusInternalServerError, apiError{Code: "server_error", Error: "profile not available"})

			c := newPresetTestClient(t)
			original := presetTestRecord(t, "preset_original", "default")
			if err := c.pd.DB.InsertPresetContext(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			// Already-canceled requests deterministically exercise the DB error
			// paths without timing races, a closed shared DB, or SQL mocks.
			w = c.serve(t, r.WithContext(ctx))
			assertPresetJSON(t, w, http.StatusInternalServerError, apiError{Code: "db_error", Error: tc.message})
			assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{original})
		})
	}
}

func TestPresetListInvalidStoredJSON(t *testing.T) {
	t.Parallel()
	c := newPresetTestClient(t)
	valid := presetTestRecord(t, "preset_a", "default")
	invalid := presetTestRecord(t, "preset_b", "default")
	invalid.SettingsJSON = `{"private-corruption":`
	for _, rec := range []storage.PresetRecord{valid, invalid} {
		if err := c.pd.DB.InsertPresetContext(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	// A corrupt persisted snapshot must fail before a 200 or a partial list
	// is written. Normal HTTP creation cannot persist this malformed JSON.
	assertPresetJSON(t, c.request(t, http.MethodGet, "/api/presets", ""), http.StatusInternalServerError,
		apiError{Code: "server_error", Error: "internal server error"})
	assertPresetRecords(t, c.pd.DB, "default", []storage.PresetRecord{valid, invalid})
}

func TestPresetRoutesRequireSession(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	RegisterRoutes(mux, &Dependencies{sessions: newSessionStore(nil)})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/presets"},
		{method: http.MethodPost, path: "/api/presets"},
		{method: http.MethodDelete, path: "/api/presets/preset_original"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			for _, token := range []string{"", "unknown-session"} {
				r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
				if token != "" {
					r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
				}
				// A borrowed context alone cannot bypass the real route's gate.
				r = withProfileDeps(r, &profileDeps{})
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				assertPresetJSON(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
			}
		})
	}
}
