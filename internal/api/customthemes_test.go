package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/storage"
)

func validCustomThemeBody(name string) customThemeBody {
	return customThemeBody{
		Name:   name,
		Group:  "light",
		Bg:     "#ffffff",
		Fg:     "#111111",
		Accent: "",
	}
}

func TestExceedsRuneLimit(t *testing.T) {
	t.Parallel()

	// The limit counts code points, not UTF-8 bytes or grapheme clusters.
	// Flairs and presets borrow this helper with their own positive limits.
	for _, tc := range []struct {
		name  string
		input string
		limit int
		want  bool
	}{
		{name: "empty", limit: 0},
		{name: "zero limit", input: "a", limit: 0, want: true},
		{name: "ASCII boundary", input: strings.Repeat("a", 40), limit: 40},
		{name: "ASCII overflow", input: strings.Repeat("a", 41), limit: 40, want: true},
		{name: "four byte boundary", input: strings.Repeat("📖", 60), limit: 60},
		{name: "four byte overflow", input: strings.Repeat("📖", 61), limit: 60, want: true},
		{name: "mixed boundary", input: strings.Repeat("aم界📖", 15), limit: 60},
		{name: "mixed overflow", input: strings.Repeat("aم界📖", 15) + "x", limit: 60, want: true},
		{name: "combining code points", input: strings.Repeat("e\u0301", 30), limit: 60},
		{name: "combining overflow", input: strings.Repeat("e\u0301", 31), limit: 60, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := exceedsRuneLimit(tc.input, tc.limit); got != tc.want {
				t.Errorf("exceedsRuneLimit(%q, %d) = %v, want %v", tc.input, tc.limit, got, tc.want)
			}
		})
	}
}

func TestNormalizeCustomThemeBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     customThemeBody
		wantName string
		wantMsg  string
		wantOK   bool
	}{
		{
			name:     "trims valid payload",
			body:     customThemeBody{Name: "  Paper  ", Group: "light", Bg: "  #fff  ", Fg: " #111111 ", Accent: " #2563eb "},
			wantName: "Paper",
			wantOK:   true,
		},
		{
			name:    "empty name",
			body:    validCustomThemeBody("   "),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:     "sixty ASCII characters",
			body:     validCustomThemeBody(strings.Repeat("a", maxThemeNameLen)),
			wantName: strings.Repeat("a", maxThemeNameLen),
			wantOK:   true,
		},
		{
			name:    "sixty one ASCII characters",
			body:    validCustomThemeBody(strings.Repeat("a", maxThemeNameLen+1)),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:     "sixty Arabic characters",
			body:     validCustomThemeBody(strings.Repeat("م", maxThemeNameLen)),
			wantName: strings.Repeat("م", maxThemeNameLen),
			wantOK:   true,
		},
		{
			name:    "sixty one Arabic characters",
			body:    validCustomThemeBody(strings.Repeat("م", maxThemeNameLen+1)),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:     "sixty supplementary characters",
			body:     validCustomThemeBody(strings.Repeat("📖", maxThemeNameLen)),
			wantName: strings.Repeat("📖", maxThemeNameLen),
			wantOK:   true,
		},
		{
			name:    "sixty one supplementary characters",
			body:    validCustomThemeBody(strings.Repeat("📖", maxThemeNameLen+1)),
			wantMsg: "name must be 1-60 characters",
		},
		{
			name:    "invalid group",
			body:    customThemeBody{Name: "Paper", Group: "sepia", Bg: "#fff", Fg: "#111"},
			wantMsg: "group must be 'light' or 'dark'",
		},
		{
			name:    "invalid background",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "white", Fg: "#111"},
			wantMsg: "bg must be a hex color like #1c1917",
		},
		{
			name:    "invalid foreground",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "#fff", Fg: "black"},
			wantMsg: "fg must be a hex color like #1c1917",
		},
		{
			name:    "invalid accent",
			body:    customThemeBody{Name: "Paper", Group: "dark", Bg: "#fff", Fg: "#111", Accent: "blue"},
			wantMsg: "accent must be a hex color like #2563eb",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.body
			msg, ok := normalizeCustomThemeBody(&body)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (msg %q)", ok, tc.wantOK, msg)
			}
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if tc.wantName != "" && body.Name != tc.wantName {
				t.Errorf("name = %q, want %q", body.Name, tc.wantName)
			}
		})
	}
}

func TestCustomThemePaletteNormalization(t *testing.T) {
	t.Parallel()

	// Colors reach CSS consumers. Accept only the documented hex forms, not
	// arbitrary CSS expressions; only accent may be empty (the client's auto).
	for _, field := range []string{"bg", "fg", "accent"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name  string
				input string
				want  string
				valid bool
			}{
				{name: "short mixed case", input: "#aBc", want: "#aBc", valid: true},
				{name: "long mixed case", input: "#A1b2C3", want: "#A1b2C3", valid: true},
				{name: "Unicode space", input: "\u2003#ABC\t", want: "#ABC", valid: true},
				{name: "empty", valid: field == "accent"},
				{name: "whitespace", input: " \t\n", valid: field == "accent"},
				{name: "missing hash", input: "abcdef"},
				{name: "non hex", input: "#ggg"},
				{name: "alpha shorthand", input: "#abcd"},
				{name: "alpha long", input: "#12345678"},
				{name: "embedded newline", input: "#ab\nc"},
				{name: "CSS variable", input: "var(--color)"},
				{name: "CSS URL", input: "url(https://example.invalid/pixel)"},
				{name: "CSS suffix", input: "#fff; background:red"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					body := validCustomThemeBody("Paper")
					color := &body.Bg
					switch field {
					case "fg":
						color = &body.Fg
					case "accent":
						color = &body.Accent
					}
					*color = tc.input
					msg, ok := normalizeCustomThemeBody(&body)
					if ok != tc.valid {
						t.Fatalf("valid = %v, want %v; message = %q", ok, tc.valid, msg)
					}
					if ok && (msg != "" || *color != tc.want) {
						t.Errorf("color = %q, message = %q; want %q and no message", *color, msg, tc.want)
					}
				})
			}
		})
	}
}

type customThemeTestClient struct {
	pd  *profileDeps
	mux *http.ServeMux
}

func newCustomThemeTestClient(t *testing.T) *customThemeTestClient {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Auth owns the live reference. Exercise the handlers with a borrowed
	// profile without starting unrelated scanners, stores, or coalescers.
	pd := &profileDeps{DB: db, refs: 1}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	t.Cleanup(func() {
		pd.lifetimeMu.Lock()
		refs := pd.refs
		pd.lifetimeMu.Unlock()
		if refs != 1 {
			t.Errorf("custom theme handler changed borrowed refs: got %d, want 1", refs)
		} else {
			pd.release()
		}
		if err := db.Close(); err != nil {
			t.Errorf("close custom theme DB: %v", err)
		}
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/themes", listCustomThemesHandler(nil))
	mux.HandleFunc("POST /api/themes", createCustomThemeHandler(nil))
	mux.HandleFunc("PUT /api/themes/{id}", updateCustomThemeHandler(nil))
	mux.HandleFunc("DELETE /api/themes/{id}", deleteCustomThemeHandler(nil))
	return &customThemeTestClient{pd: pd, mux: mux}
}

func (c *customThemeTestClient) request(
	t *testing.T,
	ctx context.Context,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, withProfileDeps(r, c.pd))
	c.pd.lifetimeMu.Lock()
	refs := c.pd.refs
	c.pd.lifetimeMu.Unlock()
	if refs != 1 {
		t.Fatalf("request changed borrowed refs: got %d, want 1", refs)
	}
	return w
}

func assertCustomThemeJSON(t *testing.T, w *httptest.ResponseRecorder, status int, want any) {
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
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &expected); err != nil {
		t.Fatal(err)
	}
	// Compare the complete JSON shape, including unexpected fields, without
	// making object key order part of the HTTP contract.
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("JSON = %s, want %s", w.Body.String(), encoded)
	}
}

func assertCustomThemeRecords(t *testing.T, db *storage.DB, userID string, want []storage.CustomThemeRecord) {
	t.Helper()
	got, err := db.ListCustomThemesContext(t.Context(), userID)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("stored themes for %q = %+v, %v; want %+v", userID, got, err, want)
	}
}

func customThemeFixture(id, userID string) storage.CustomThemeRecord {
	return storage.CustomThemeRecord{
		ID: id, UserID: userID, Name: "Original", Group: "light",
		Bg: "#fff", Fg: "#111", Accent: "#ABC",
		CreatedAt: "2000-01-02 03:04:05", UpdatedAt: "2001-02-03 04:05:06",
	}
}

func TestCustomThemeHandlersCRUDAndIsolation(t *testing.T) {
	t.Parallel()
	client := newCustomThemeTestClient(t)
	otherProfile := newCustomThemeTestClient(t)
	ctx := t.Context()
	assertCustomThemeJSON(t, client.request(t, ctx, http.MethodGet, "/api/themes", ""), http.StatusOK, []any{})

	// Extra JSON fields must not choose the owner, ID, or stored timestamps.
	payload := `{"name":"  Paper <&>  ","group":"dark","bg":" #ABC ","fg":"#123456","accent":" ",` +
		`"id":"forged","userId":"other","createdAt":"forged","updatedAt":"forged"}`
	before := time.Now().UTC().Truncate(time.Second)
	w := client.request(t, ctx, http.MethodPost, "/api/themes", payload)
	var created customThemeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	idBytes, err := hex.DecodeString(strings.TrimPrefix(created.ID, "theme_"))
	if err != nil || !strings.HasPrefix(created.ID, "theme_") || len(idBytes) != 8 {
		t.Fatalf("invalid server-generated ID: %q", created.ID)
	}
	createdAt, err := time.Parse(time.DateTime, created.CreatedAt)
	if err != nil || createdAt.Before(before) || createdAt.After(time.Now().UTC()) {
		t.Fatalf("invalid creation time: %q, %v", created.CreatedAt, err)
	}
	want := map[string]any{
		"id": created.ID, "name": "Paper <&>", "group": "dark",
		"bg": "#ABC", "fg": "#123456", "accent": "",
		"createdAt": created.CreatedAt, "updatedAt": created.CreatedAt,
	}
	assertCustomThemeJSON(t, w, http.StatusCreated, want)
	assertCustomThemeJSON(t, client.request(t, ctx, http.MethodGet, "/api/themes", ""), http.StatusOK, []any{want})
	record := storage.CustomThemeRecord{
		ID: created.ID, UserID: "default", Name: "Paper <&>", Group: "dark",
		Bg: "#ABC", Fg: "#123456", Accent: "",
		CreatedAt: created.CreatedAt, UpdatedAt: created.CreatedAt,
	}
	assertCustomThemeRecords(t, client.pd.DB, "default", []storage.CustomThemeRecord{record})
	assertCustomThemeRecords(t, client.pd.DB, "other", nil)
	assertCustomThemeJSON(t, otherProfile.request(t, ctx, http.MethodGet, "/api/themes", ""), http.StatusOK, []any{})

	// Identical IDs in different profile databases must still be isolated.
	other := customThemeFixture(created.ID, "default")
	if err := otherProfile.pd.DB.InsertCustomThemeContext(ctx, other); err != nil {
		t.Fatal(err)
	}
	// Seed an old update time so this assertion does not depend on a sleep or
	// on two HTTP requests falling in different wall-clock seconds.
	record.UpdatedAt = "2001-02-03 04:05:06"
	if _, err := client.pd.DB.UpdateCustomThemeContext(ctx, record); err != nil {
		t.Fatal(err)
	}
	before = time.Now().UTC().Truncate(time.Second)
	payload = `{"name":" Night ","group":"light","bg":"#000","fg":" #FFF ","accent":" #a1B2c3 ",` +
		`"id":"forged","userId":"other","createdAt":"forged","updatedAt":"forged"}`
	w = client.request(t, ctx, http.MethodPut, "/api/themes/"+created.ID, payload)
	var updated customThemeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.DateTime, updated.UpdatedAt)
	if err != nil || updatedAt.Before(before) || updatedAt.After(time.Now().UTC()) {
		t.Fatalf("invalid update time: %q, %v", updated.UpdatedAt, err)
	}
	want["name"], want["group"] = "Night", "light"
	want["bg"], want["fg"], want["accent"] = "#000", "#FFF", "#a1B2c3"
	want["updatedAt"] = updated.UpdatedAt
	assertCustomThemeJSON(t, w, http.StatusOK, want)
	assertCustomThemeJSON(t, client.request(t, ctx, http.MethodGet, "/api/themes", ""), http.StatusOK, []any{want})
	record.Name, record.Group = "Night", "light"
	record.Bg, record.Fg, record.Accent = "#000", "#FFF", "#a1B2c3"
	record.UpdatedAt = updated.UpdatedAt
	assertCustomThemeRecords(t, client.pd.DB, "default", []storage.CustomThemeRecord{record})
	assertCustomThemeRecords(t, otherProfile.pd.DB, "default", []storage.CustomThemeRecord{other})

	foreign := customThemeFixture("theme_foreign", "other")
	if err := client.pd.DB.InsertCustomThemeContext(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{foreign.ID, "theme_missing"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			w = client.request(t, ctx, method, "/api/themes/"+id+"?userId=other&profile=other", payload)
			assertCustomThemeJSON(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "custom theme not found"})
		}
	}
	assertCustomThemeJSON(t, client.request(t, ctx, http.MethodGet, "/api/themes?userId=other", ""), http.StatusOK, []any{want})
	assertCustomThemeRecords(t, client.pd.DB, "other", []storage.CustomThemeRecord{foreign})

	w = client.request(t, ctx, http.MethodDelete, "/api/themes/"+created.ID, "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("delete = %d, %q; want 204 without a body", w.Code, w.Body.String())
	}
	w = client.request(t, ctx, http.MethodDelete, "/api/themes/"+created.ID, "")
	assertCustomThemeJSON(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "custom theme not found"})
	assertCustomThemeJSON(t, client.request(t, ctx, http.MethodGet, "/api/themes", ""), http.StatusOK, []any{})
	assertCustomThemeRecords(t, client.pd.DB, "other", []storage.CustomThemeRecord{foreign})
	assertCustomThemeRecords(t, otherProfile.pd.DB, "default", []storage.CustomThemeRecord{other})
}

func TestCustomThemeHandlersRejectWithoutMutation(t *testing.T) {
	t.Parallel()
	const valid = `{"name":"Changed","group":"dark","bg":"#000","fg":"#fff","accent":""}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			client := newCustomThemeTestClient(t)
			original := customThemeFixture("theme_original", "default")
			if err := client.pd.DB.InsertCustomThemeContext(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			path := "/api/themes"
			if method == http.MethodPut {
				path += "/" + original.ID
			}
			for _, tc := range []struct {
				name   string
				body   string
				status int
				code   string
			}{
				{name: "empty", status: http.StatusBadRequest, code: "invalid_body"},
				{name: "null", body: "null", status: http.StatusBadRequest, code: "invalid_body"},
				{name: "wrong type", body: "[]", status: http.StatusBadRequest, code: "invalid_body"},
				{name: "syntax", body: `{"name":`, status: http.StatusBadRequest, code: "invalid_body"},
				{name: "trailing value", body: valid + `{}`, status: http.StatusBadRequest, code: "invalid_body"},
				{name: "trailing junk", body: valid + `x`, status: http.StatusBadRequest, code: "invalid_body"},
				{name: "long name", body: strings.Replace(valid, "Changed", strings.Repeat("📖", 61), 1), status: http.StatusBadRequest, code: "invalid_body"},
				{name: "invalid group", body: strings.Replace(valid, "dark", "DARK", 1), status: http.StatusBadRequest, code: "invalid_body"},
				{name: "CSS injection", body: strings.Replace(valid, "#000", "#000; color:red", 1), status: http.StatusBadRequest, code: "invalid_body"},
				// The decoder must account for trailing whitespace too, not just
				// stop after the first valid object and accidentally save it.
				{name: "over body limit", body: valid + strings.Repeat(" ", maxJSONBodySize-len(valid)+1), status: http.StatusRequestEntityTooLarge, code: "too_large"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := client.request(t, t.Context(), method, path, tc.body)
					var got apiError
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if got.Code != tc.code || got.Error == "" {
						t.Errorf("error = %+v, want code %q and a message", got, tc.code)
					}
					assertCustomThemeJSON(t, w, tc.status, got)
					assertCustomThemeRecords(t, client.pd.DB, "default", []storage.CustomThemeRecord{original})
				})
			}
			// Exactly the byte limit is allowed, including trailing whitespace.
			w := client.request(t, t.Context(), method, path, valid+strings.Repeat(" ", maxJSONBodySize-len(valid)))
			wantStatus := http.StatusCreated
			if method == http.MethodPut {
				wantStatus = http.StatusOK
			}
			if w.Code != wantStatus {
				t.Fatalf("at body limit: status = %d, want %d; body = %s", w.Code, wantStatus, w.Body.String())
			}
			var accepted customThemeResponse
			if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
				t.Fatal(err)
			}
			record := storage.CustomThemeRecord{
				ID: accepted.ID, UserID: "default", Name: "Changed", Group: "dark",
				Bg: "#000", Fg: "#fff", Accent: "",
				CreatedAt: accepted.CreatedAt, UpdatedAt: accepted.UpdatedAt,
			}
			if record.ID == "" || record.CreatedAt == "" || record.UpdatedAt == "" {
				t.Fatalf("incomplete response at body limit: %+v", accepted)
			}
			wantRecords := []storage.CustomThemeRecord{record}
			if method == http.MethodPost {
				if record.ID == original.ID {
					t.Fatal("create reused the existing theme ID")
				}
				wantRecords = []storage.CustomThemeRecord{original, record}
			} else if record.ID != original.ID || record.CreatedAt != original.CreatedAt {
				t.Fatalf("update changed immutable fields: %+v", accepted)
			}
			assertCustomThemeRecords(t, client.pd.DB, "default", wantRecords)
		})
	}
}

func TestCustomThemeHandlersMissingProfileAndCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method string
		path   string
		action string
	}{
		{method: http.MethodGet, path: "/api/themes", action: "list custom themes"},
		{method: http.MethodPost, path: "/api/themes", action: "create custom theme"},
		{method: http.MethodPut, path: "/api/themes/theme_original", action: "update custom theme"},
		{method: http.MethodDelete, path: "/api/themes/theme_original", action: "delete custom theme"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			client := newCustomThemeTestClient(t)
			original := customThemeFixture("theme_original", "default")
			if err := client.pd.DB.InsertCustomThemeContext(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			const body = `{"name":"Changed","group":"dark","bg":"#000","fg":"#fff"}`
			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(body))
			w := httptest.NewRecorder()
			client.mux.ServeHTTP(w, r)
			assertCustomThemeJSON(t, w, http.StatusInternalServerError, apiError{Code: "server_error", Error: "profile not available"})

			// A canceled database operation is not a missing theme and must not
			// mutate data or expose the storage error to the HTTP client.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			w = client.request(t, ctx, tc.method, tc.path, body)
			assertCustomThemeJSON(t, w, http.StatusInternalServerError, apiError{Code: "db_error", Error: "failed to " + tc.action})
			assertCustomThemeRecords(t, client.pd.DB, "default", []storage.CustomThemeRecord{original})
		})
	}
}

func TestCustomThemeRoutesRequireSession(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	RegisterRoutes(mux, &Dependencies{})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/themes"},
		{method: http.MethodPost, path: "/api/themes"},
		{method: http.MethodPut, path: "/api/themes/theme_original"},
		{method: http.MethodDelete, path: "/api/themes/theme_original"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
			// Supplying a profile context must not bypass the actual route's
			// session gate; the bare-handler fixture above deliberately does.
			r = withProfileDeps(r, &profileDeps{})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			assertCustomThemeJSON(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
		})
	}
}
