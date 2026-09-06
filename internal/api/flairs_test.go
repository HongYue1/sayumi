package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sayumi/internal/storage"
)

func TestNormalizeCreateFlairBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      createFlairBody
		wantLabel string
		wantColor string
		wantMsg   string
		wantOK    bool
	}{
		{
			name:      "trims valid payload",
			body:      createFlairBody{Label: "  Favorite  ", Color: "  #3b82f6  "},
			wantLabel: "Favorite",
			wantColor: "#3b82f6",
			wantOK:    true,
		},
		{
			name:    "empty label",
			body:    createFlairBody{Label: "   ", Color: "#3b82f6"},
			wantMsg: "label must be 1-40 characters",
		},
		{
			name:      "forty ASCII characters",
			body:      createFlairBody{Label: strings.Repeat("a", maxFlairLabelLen), Color: "#abc"},
			wantLabel: strings.Repeat("a", maxFlairLabelLen),
			wantColor: "#abc",
			wantOK:    true,
		},
		{
			name:    "forty one ASCII characters",
			body:    createFlairBody{Label: strings.Repeat("a", maxFlairLabelLen+1), Color: "#abc"},
			wantMsg: "label must be 1-40 characters",
		},
		{
			name:      "forty Arabic characters",
			body:      createFlairBody{Label: strings.Repeat("م", maxFlairLabelLen), Color: "#abcdef"},
			wantLabel: strings.Repeat("م", maxFlairLabelLen),
			wantColor: "#abcdef",
			wantOK:    true,
		},
		{
			name:    "forty one Arabic characters",
			body:    createFlairBody{Label: strings.Repeat("م", maxFlairLabelLen+1), Color: "#abcdef"},
			wantMsg: "label must be 1-40 characters",
		},
		{
			name:    "invalid color",
			body:    createFlairBody{Label: "Favorite", Color: "blue"},
			wantMsg: "color must be a hex value like #3b82f6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.body
			msg, ok := normalizeCreateFlairBody(&body)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (msg %q)", ok, tc.wantOK, msg)
			}
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if tc.wantOK && body.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", body.Label, tc.wantLabel)
			}
			if tc.wantOK && body.Color != tc.wantColor {
				t.Errorf("color = %q, want %q", body.Color, tc.wantColor)
			}
		})
	}
}

func TestFlairCreateRoundTrip(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t)
	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/flairs", ""), http.StatusOK, []flairTestJSON{})

	// Pin the public 40-rune boundary independently of the production constant;
	// emoji use more than one UTF-16 code unit and more than one UTF-8 byte.
	label := strings.Repeat("😀", 40)
	body := fmt.Sprintf(`{"id":"flair_chosen","userId":"other","createdAt":"1900-01-01 00:00:00","label":%q,"color":" \t#AbC \n"}`, "\u2003"+label+"\u00a0")
	w := c.request(t, http.MethodPost, "/api/flairs?userId=other", body)
	var got flairTestJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.ID, "flair_") || got.ID == "flair_chosen" {
		t.Fatalf("server-generated id = %q", got.ID)
	}
	want := flairTestJSON{ID: got.ID, Label: label, Color: "#AbC"}
	assertSettingsResponse(t, w, http.StatusCreated, want)
	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/flairs", ""), http.StatusOK, []flairTestJSON{want})

	rows, err := c.pd.DB.ListFlairsContext(t.Context(), "default")
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored flairs = %+v, %v", rows, err)
	}
	if rows[0].CreatedAt == "" || rows[0].CreatedAt == "1900-01-01 00:00:00" {
		t.Fatalf("server-created timestamp = %q", rows[0].CreatedAt)
	}
	assertFlairRecords(t, c.pd, "default", storage.FlairRecord{
		ID: got.ID, UserID: "default", Label: label, Color: "#AbC", CreatedAt: rows[0].CreatedAt,
	})
	assertFlairRecords(t, c.pd, "other")
}

func TestFlairHandlersListAndDeleteIsolation(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t, "a", "b", "keep")
	second := newFlairTestClient(t, "a")
	late := storage.FlairRecord{ID: "flair_late", UserID: "default", Label: "Late", Color: "#123456", CreatedAt: "2026-02-03 00:00:00"}
	if err := c.pd.DB.InsertFlairContext(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	b := seedFlairTestRecord(t, c.pd, "flair_b", "default")
	a := seedFlairTestRecord(t, c.pd, "flair_a", "default")
	other := seedFlairTestRecord(t, c.pd, "flair_other", "other")
	secondRecord := seedFlairTestRecord(t, second.pd, "flair_a", "default")
	// Deliberately insert out of order: timestamps sort before IDs, with IDs
	// breaking equal-time ties. Built-ins and other users' rows are not listed.
	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/flairs?userId=other", ""), http.StatusOK, []flairTestJSON{
		{ID: a.ID, Label: a.Label, Color: a.Color},
		{ID: b.ID, Label: b.Label, Color: b.Color},
		{ID: late.ID, Label: late.Label, Color: late.Color},
	})
	for _, id := range []string{"a", "b"} {
		assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/"+id+"/flair", `{"flairId":"flair_a"}`))
	}
	assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/keep/flair", `{"flairId":"reading"}`))
	assertFlairNoContent(t, second.request(t, http.MethodPut, "/api/books/a/flair", `{"flairId":"flair_a"}`))
	if err := c.pd.DB.SetBookFlairCheckedContext(t.Context(), "a", "other", other.ID, builtinFlairIDs); err != nil {
		t.Fatal(err)
	}

	before := map[string]string{"a": a.ID, "b": a.ID, "keep": "reading"}
	for _, id := range []string{"missing", other.ID, "reading", "' OR 1=1 --"} {
		t.Run("reject "+id, func(t *testing.T) {
			w := c.request(t, http.MethodDelete, "/api/flairs/"+url.PathEscape(id)+"?userId=other", "")
			assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "flair not found"})
			assertFlairRecords(t, c.pd, "default", a, b, late)
			assertBookFlairs(t, c.pd, "default", before)
		})
	}
	assertFlairNoContent(t, c.request(t, http.MethodDelete, "/api/flairs/flair_a?userId=other", ""))
	assertFlairRecords(t, c.pd, "default", b, late)
	assertBookFlairs(t, c.pd, "default", map[string]string{"keep": "reading"})
	assertFlairRecords(t, c.pd, "other", other)
	assertBookFlairs(t, c.pd, "other", map[string]string{"a": other.ID})
	assertFlairRecords(t, second.pd, "default", secondRecord)
	assertBookFlairs(t, second.pd, "default", map[string]string{"a": secondRecord.ID})
	assertSettingsResponse(t, second.request(t, http.MethodGet, "/api/flairs", ""), http.StatusOK, []flairTestJSON{
		{ID: secondRecord.ID, Label: secondRecord.Label, Color: secondRecord.Color},
	})
	assertSettingsError(t, c.request(t, http.MethodDelete, "/api/flairs/flair_a", ""), http.StatusNotFound, apiError{Code: "not_found", Error: "flair not found"})
}

func TestFlairBookAssignmentContracts(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t, "book", "keep")
	second := newFlairTestClient(t, "book")
	custom := seedFlairTestRecord(t, c.pd, "flair_custom", "default")
	assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/keep/flair", `{"flairId":"reading"}`))
	assertFlairNoContent(t, second.request(t, http.MethodPut, "/api/books/book/flair", `{"flairId":"dropped"}`))
	if err := c.pd.DB.SetBookFlairCheckedContext(t.Context(), "book", "other", "finished", builtinFlairIDs); err != nil {
		t.Fatal(err)
	}
	assertAssignment := func(t *testing.T, id string) {
		t.Helper()
		want := map[string]string{"keep": "reading"}
		if id != "" {
			want["book"] = id
		}
		assertBookFlairs(t, c.pd, "default", want)
		assertBookFlairs(t, c.pd, "other", map[string]string{"book": "finished"})
		assertBookFlairs(t, second.pd, "default", map[string]string{"book": "dropped"})
	}

	// Keep these literal IDs aligned with DEFAULT_FLAIRS in frontend/src/lib/flairs.ts.
	// Deriving cases from builtinFlairIDs would hide an accidentally removed entry.
	builtins := []string{"reading", "finished", "dropped", "plan-to-read"}
	if len(builtinFlairIDs) != len(builtins) {
		t.Fatalf("built-in catalog has %d entries, want %d", len(builtinFlairIDs), len(builtins))
	}
	for _, id := range builtins {
		t.Run(id, func(t *testing.T) {
			body := fmt.Sprintf(`{"flairId":%q}`, " \t"+id+"\n\u2003")
			assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/book/flair?userId=other", body))
			assertAssignment(t, id)
		})
	}
	for _, body := range []string{`{"flairId":null}`, `{"flairId":""}`, `{"flairId":" \t\u2003 "}`, `{}`} {
		t.Run("clear "+body, func(t *testing.T) {
			assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/book/flair", `{"flairId":"  flair_custom  "}`))
			assertAssignment(t, custom.ID)
			// Re-seed each case so every nullable/empty form must actually remove
			// an assignment, then repeat it to check idempotent clearing.
			for range 2 {
				assertFlairNoContent(t, c.request(t, http.MethodPut, "/api/books/book/flair", body))
				assertAssignment(t, "")
			}
		})
	}
	assertFlairRecords(t, c.pd, "default", custom)
}

func TestFlairCreateRejectsWithoutMutation(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t)
	kept := seedFlairTestRecord(t, c.pd, "flair_kept", "default")
	for _, tc := range []struct {
		name, body, message string
	}{
		{"empty", "", "invalid JSON body"},
		{"malformed", `{`, "invalid JSON body"},
		{"array", `[]`, "invalid JSON body"},
		{"scalar", `true`, "invalid JSON body"},
		{"wrong label type", `{"label":42,"color":"#abc"}`, "invalid JSON body"},
		{"wrong color type", `{"label":"Favorite","color":[]}`, "invalid JSON body"},
		{"second value", `{"label":"Favorite","color":"#abc"} {}`, "invalid JSON body"},
		{"trailing junk", `{"label":"Favorite","color":"#abc"} x`, "invalid JSON body"},
		{"null", `null`, "label must be 1-40 characters"},
		{"missing label", `{"color":"#abc"}`, "label must be 1-40 characters"},
		{"blank label", `{"label":" \t\u2003 ","color":"#abc"}`, "label must be 1-40 characters"},
		{"41 runes", fmt.Sprintf(`{"label":%q,"color":"#abc"}`, strings.Repeat("😀", 41)), "label must be 1-40 characters"},
		{"missing color", `{"label":"Favorite"}`, "color must be a hex value like #3b82f6"},
		{"named color", `{"label":"Favorite","color":"blue"}`, "color must be a hex value like #3b82f6"},
		{"alpha shorthand", `{"label":"Favorite","color":"#abcd"}`, "color must be a hex value like #3b82f6"},
		{"alpha long", `{"label":"Favorite","color":"#12345678"}`, "color must be a hex value like #3b82f6"},
		{"non-hex", `{"label":"Favorite","color":"#ggg"}`, "color must be a hex value like #3b82f6"},
		{"CSS suffix", `{"label":"Favorite","color":"#abc; background:url(x)"}`, "color must be a hex value like #3b82f6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := c.request(t, http.MethodPost, "/api/flairs", tc.body)
			assertSettingsError(t, w, http.StatusBadRequest, apiError{Code: "invalid_body", Error: tc.message})
			assertFlairRecords(t, c.pd, "default", kept)
		})
	}
}

func TestFlairAssignmentRejectsWithoutMutation(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t, "book", "keep")
	second := newFlairTestClient(t, "foreign-book")
	custom := seedFlairTestRecord(t, c.pd, "flair_custom", "default")
	other := seedFlairTestRecord(t, c.pd, "flair_other", "other")
	foreign := seedFlairTestRecord(t, second.pd, "flair_foreign", "default")
	before := map[string]string{"book": "reading", "keep": "finished"}
	for bookID, flairID := range before {
		if err := c.pd.DB.SetBookFlairCheckedContext(t.Context(), bookID, "default", flairID, builtinFlairIDs); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, body string
		status     int
		message    string
	}{
		{"empty", "", http.StatusBadRequest, "invalid JSON body"},
		{"malformed", `{`, http.StatusBadRequest, "invalid JSON body"},
		{"array", `[]`, http.StatusBadRequest, "invalid JSON body"},
		{"scalar", `false`, http.StatusBadRequest, "invalid JSON body"},
		{"wrong field type", `{"flairId":42}`, http.StatusBadRequest, "invalid JSON body"},
		{"second value", `{"flairId":"dropped"} null`, http.StatusBadRequest, "invalid JSON body"},
		{"trailing junk", `{"flairId":null} x`, http.StatusBadRequest, "invalid JSON body"},
		{"129 ASCII bytes", fmt.Sprintf(`{"flairId":%q}`, strings.Repeat("a", 129)), http.StatusBadRequest, "flairId too long"},
		{"129 UTF-8 bytes", fmt.Sprintf(`{"flairId":%q}`, strings.Repeat("界", 43)), http.StatusBadRequest, "flairId too long"},
		{"128 bytes reach lookup", fmt.Sprintf(`{"flairId":%q}`, strings.Repeat("a", 128)), http.StatusNotFound, "book or flair not found"},
		{"unknown", `{"flairId":"missing"}`, http.StatusNotFound, "book or flair not found"},
		{"case-sensitive builtin", `{"flairId":"READING"}`, http.StatusNotFound, "book or flair not found"},
		{"other user", `{"flairId":"flair_other","userId":"other"}`, http.StatusNotFound, "book or flair not found"},
		{"other profile", `{"flairId":"flair_foreign"}`, http.StatusNotFound, "book or flair not found"},
		{"SQL-like id", `{"flairId":"' OR 1=1 --"}`, http.StatusNotFound, "book or flair not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := "invalid_body"
			if tc.status == http.StatusNotFound {
				code = "not_found"
			}
			w := c.request(t, http.MethodPut, "/api/books/book/flair?userId=other", tc.body)
			assertSettingsError(t, w, tc.status, apiError{Code: code, Error: tc.message})
			assertBookFlairs(t, c.pd, "default", before)
		})
	}
	// Cached absence wins before JSON decoding, even for a valid clear or an
	// invalid body. A book present only in another profile is absent here too.
	for _, id := range []string{"missing", "foreign-book"} {
		for _, body := range []string{`{"flairId":null}`, `{`} {
			w := c.request(t, http.MethodPut, "/api/books/"+id+"/flair", body)
			assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"})
		}
	}
	// Model a stale cached existence result after a book has disappeared from
	// SQLite. Both built-in and custom writes must translate storage not-found
	// to 404, rather than succeeding or leaking a constraint/database error.
	c.pd.Books.Add(storage.BookRecord{ID: "stale", Title: "Stale"})
	for _, id := range []string{"reading", custom.ID} {
		w := c.request(t, http.MethodPut, "/api/books/stale/flair", fmt.Sprintf(`{"flairId":%q}`, id))
		assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "book or flair not found"})
	}
	assertBookFlairs(t, c.pd, "default", before)
	assertBookFlairs(t, c.pd, "other", map[string]string{})
	assertBookFlairs(t, second.pd, "default", map[string]string{})
	assertFlairRecords(t, c.pd, "default", custom)
	assertFlairRecords(t, c.pd, "other", other)
	assertFlairRecords(t, second.pd, "default", foreign)
}

func TestFlairRequestBodyLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, path, body, largeBody string
		status                              int
	}{
		{"create", http.MethodPost, "/api/flairs", `{"label":"Bounded","color":"#abc"}`, `{"label":"` + strings.Repeat("x", 64<<10) + `","color":"#abc"}`, http.StatusCreated},
		{"assign", http.MethodPut, "/api/books/book/flair", `{"flairId":"finished"}`, `{"flairId":"` + strings.Repeat("x", 64<<10) + `"}`, http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFlairTestClient(t, "book")
			exact := tc.body + strings.Repeat(" ", (64<<10)-len(tc.body))
			w := c.request(t, tc.method, tc.path, exact)
			if w.Code != tc.status {
				t.Fatalf("exact 64 KiB: status=%d, body=%s", w.Code, w.Body.String())
			}
			rows, err := c.pd.DB.ListFlairsContext(t.Context(), "default")
			if err != nil {
				t.Fatal(err)
			}
			assigned := map[string]string{}
			if tc.method == http.MethodPut {
				assigned["book"] = "finished"
			}
			for _, body := range []string{exact + " ", tc.largeBody} {
				w := c.request(t, tc.method, tc.path, body)
				assertSettingsError(t, w, http.StatusRequestEntityTooLarge, apiError{Code: "too_large", Error: "request body too large"})
				assertFlairRecords(t, c.pd, "default", rows...)
				assertBookFlairs(t, c.pd, "default", assigned)
			}
		})
	}
}

func TestFlairHandlersMissingProfileAndCancellation(t *testing.T) {
	t.Parallel()
	c := newFlairTestClient(t, "book", "keep")
	custom := seedFlairTestRecord(t, c.pd, "flair_custom", "default")
	before := map[string]string{"book": "reading", "keep": custom.ID}
	for bookID, flairID := range before {
		if err := c.pd.DB.SetBookFlairCheckedContext(t.Context(), bookID, "default", flairID, builtinFlairIDs); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, method, path, body, message string
	}{
		{"list", http.MethodGet, "/api/flairs", "", "failed to list flairs"},
		{"create", http.MethodPost, "/api/flairs", `{"label":"New","color":"#abc"}`, "failed to create flair"},
		{"delete", http.MethodDelete, "/api/flairs/flair_custom", "", "failed to delete flair"},
		{"custom", http.MethodPut, "/api/books/book/flair", `{"flairId":"flair_custom"}`, "failed to set flair"},
		{"builtin", http.MethodPut, "/api/books/book/flair", `{"flairId":"finished"}`, "failed to set flair"},
		{"clear", http.MethodPut, "/api/books/book/flair", `{"flairId":null}`, "failed to set flair"},
	} {
		for _, missing := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/missing=%v", tc.name, missing), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				pd := c.pd
				want := apiError{Code: "db_error", Error: tc.message}
				if missing {
					pd = nil
					want = apiError{Code: "server_error", Error: "profile not available"}
				} else {
					cancel()
				}
				r := httptest.NewRequestWithContext(ctx, tc.method, tc.path, strings.NewReader(tc.body))
				w := httptest.NewRecorder()
				c.mux.ServeHTTP(w, withProfileDeps(r, pd))
				assertSettingsError(t, w, http.StatusInternalServerError, want)
				assertFlairRecords(t, c.pd, "default", custom)
				assertBookFlairs(t, c.pd, "default", before)
			})
		}
	}
}

func TestFlairRoutesRequireSession(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{sessions: newSessionStore(nil)}
	handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/flairs", ""},
		{http.MethodPost, "/api/flairs", `{"label":"New","color":"#abc"}`},
		{http.MethodDelete, "/api/flairs/flair_custom", ""},
		{http.MethodPut, "/api/books/book/flair", `{"flairId":"reading"}`},
	} {
		for _, cookie := range []string{"", "unknown"} {
			t.Run(tc.method+"/cookie="+cookie, func(t *testing.T) {
				r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
				if cookie != "" {
					r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
				}
				// A forged context cannot substitute for the route's session gate.
				r = withProfileDeps(r, &profileDeps{})
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				assertSettingsError(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
			})
		}
		if tc.method != http.MethodGet {
			t.Run(tc.method+"/cross-site", func(t *testing.T) {
				r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				assertSettingsError(t, w, http.StatusForbidden, apiError{Code: "cross_site", Error: "cross-site request blocked"})
			})
		}
	}
}

// Keep wire expectations independent of flairResponse/flairToResponse so an
// accidentally exposed owner/timestamp or changed JSON field fails at HTTP level.
type flairTestJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
}

type flairTestClient struct {
	pd  *profileDeps
	mux *http.ServeMux
}

func newFlairTestClient(t *testing.T, bookIDs ...string) flairTestClient {
	t.Helper()
	// Reuse the temporary real SQLite/profile fixture and its borrowed-ref
	// assertion. No scanner, coalescer, external service, or SQL mock is needed.
	pd := settingsTestProfile(t)
	for _, id := range bookIDs {
		book := storage.BookRecord{
			ID: id, Title: id, FilePath: filepath.Join(pd.LibPath, id+".epub"), FileHash: "hash-" + id,
		}
		if got, err := pd.DB.InsertBookContext(t.Context(), book); err != nil || got != id {
			t.Fatalf("insert book %q = %q, %v", id, got, err)
		}
	}
	books, err := storage.NewBookCache(t.Context(), pd.DB)
	if err != nil {
		t.Fatal(err)
	}
	pd.Books = books
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flairs", listFlairsHandler(nil))
	mux.HandleFunc("POST /api/flairs", createFlairHandler(nil))
	mux.HandleFunc("DELETE /api/flairs/{id}", deleteFlairHandler(nil))
	mux.HandleFunc("PUT /api/books/{id}/flair", setBookFlairHandler(nil))
	return flairTestClient{pd: pd, mux: mux}
}

func (c flairTestClient) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// The current single-user-per-profile API must ignore caller-selected owners.
	r.Header.Set("X-User-Id", "other")
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, withProfileDeps(r, c.pd))
	return w
}

func seedFlairTestRecord(t *testing.T, pd *profileDeps, id, userID string) storage.FlairRecord {
	t.Helper()
	rec := storage.FlairRecord{ID: id, UserID: userID, Label: id + " label", Color: "#abc", CreatedAt: "2026-01-02 03:04:05"}
	if err := pd.DB.InsertFlairContext(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func assertFlairRecords(t *testing.T, pd *profileDeps, userID string, want ...storage.FlairRecord) {
	t.Helper()
	got, err := pd.DB.ListFlairsContext(t.Context(), userID)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("stored flairs for %q = %+v, %v; want %+v", userID, got, err, want)
	}
}

func assertBookFlairs(t *testing.T, pd *profileDeps, userID string, want map[string]string) {
	t.Helper()
	got, err := pd.DB.GetAllBookFlairsContext(t.Context(), userID)
	if err != nil || !maps.Equal(got, want) {
		t.Fatalf("book flairs for %q = %v, %v; want %v", userID, got, err, want)
	}
}

func assertFlairNoContent(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("response = %d %q, want 204 with no body", w.Code, w.Body.String())
	}
}
