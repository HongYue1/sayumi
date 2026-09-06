package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"sayumi/internal/storage"
)

func TestBookmarkToResponseCFI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfi  sql.NullString
		want string
	}{
		{name: "present", cfi: sql.NullString{String: "epubcfi(/6/2)", Valid: true}, want: "epubcfi(/6/2)"},
		{name: "empty", cfi: sql.NullString{Valid: true}},
		{name: "null"},
		{name: "invalid payload", cfi: sql.NullString{String: "not a valid value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeJSON(w, http.StatusOK, bookmarkToResponse(storage.BookmarkRecord{
				ID: "mark", BookID: "private-book", UserID: "private-user", Chapter: 2,
				Percent: 0.25, CFI: tc.cfi, Label: "Label", Comment: "Note", CreatedAt: "2026-01-02 03:04:05",
			}))
			assertSettingsResponse(t, w, http.StatusOK, bookmarkTestJSON{
				ID: "mark", Chapter: 2, Percent: 0.25, CFI: tc.want,
				Label: "Label", Comment: "Note", CreatedAt: "2026-01-02 03:04:05",
			})
		})
	}
}

func TestBookmarkListOrderingAndIsolation(t *testing.T) {
	t.Parallel()
	c := newBookmarkTestClient(t)
	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/books/book/bookmarks", ""), http.StatusOK, []bookmarkTestJSON{})
	const stamp = "2026-01-02 03:04:05"
	want := []bookmarkTestJSON{
		{ID: "chapter-first", Chapter: 0, Percent: 1, CFI: "epubcfi(/6/2)", Label: "First", CreatedAt: stamp},
		{ID: "percent-first", Chapter: 1, Percent: 0.1, Comment: "Second", CreatedAt: stamp},
		{ID: "time-first", Chapter: 1, Percent: 0.5, Label: "Earlier", CreatedAt: "2026-01-01 00:00:00"},
		{ID: "a-tie", Chapter: 1, Percent: 0.5, CFI: "epubcfi(/6/4)", Label: "A", Comment: "A note", CreatedAt: stamp},
		{ID: "b-tie", Chapter: 1, Percent: 0.5, Label: "B", Comment: "B note", CreatedAt: stamp},
	}
	for _, b := range slices.Backward(want) {
		seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{
			ID: b.ID, BookID: "book", UserID: "default", Chapter: b.Chapter, Percent: b.Percent,
			CFI:   sql.NullString{String: b.CFI, Valid: b.CFI != "" || b.ID == "percent-first"},
			Label: b.Label, Comment: b.Comment, CreatedAt: b.CreatedAt,
		})
	}
	seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "foreign-user", BookID: "book", UserID: "other"})
	otherBook := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "foreign-book", BookID: "other", UserID: "default"})
	other := newBookmarkTestClient(t)
	otherProfile := seedBookmarkTestRecord(t, other.pd, storage.BookmarkRecord{ID: "a-tie", BookID: "book", UserID: "default", Label: "Other profile"})

	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/books/book/bookmarks", ""), http.StatusOK, want)
	assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/books/other/bookmarks", ""), http.StatusOK, []bookmarkTestJSON{bookmarkTestWire(otherBook)})
	assertSettingsResponse(t, other.request(t, http.MethodGet, "/api/books/book/bookmarks", ""), http.StatusOK, []bookmarkTestJSON{bookmarkTestWire(otherProfile)})
	assertSettingsError(t, c.request(t, http.MethodGet, "/api/books/missing/bookmarks", ""), http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"})
}

func TestBookmarkCreateRoundTrip(t *testing.T) {
	t.Parallel()
	labelLimit := strings.Repeat("x", 2000)
	commentLimit := strings.Repeat("🙂", 500)
	for _, tc := range []struct {
		name string
		body string
		want bookmarkTestJSON
	}{
		{name: "start and omitted text", body: `{"chapter":0,"percent":0}`},
		{name: "last chapter and byte limits", body: fmt.Sprintf(`{"chapter":2,"percent":1,"cfi":"epubcfi(/6/4)","label":%q,"comment":%q,"id":"client-id","bookId":"other","userId":"other","createdAt":"1999-01-01 00:00:00"}`, labelLimit, commentLimit), want: bookmarkTestJSON{Chapter: 2, Percent: 1, CFI: "epubcfi(/6/4)", Label: labelLimit, Comment: commentLimit}},
		{name: "Unicode and whitespace", body: `{"chapter":1,"percent":0.375,"cfi":" opaque locator ","label":" \tمرحبا <&> ","comment":"\nnote\n"}`, want: bookmarkTestJSON{Chapter: 1, Percent: 0.375, CFI: " opaque locator ", Label: " \tمرحبا <&> ", Comment: "\nnote\n"}},
		{name: "null optional fields", body: `{"chapter":1,"percent":0.5,"cfi":null,"label":null,"comment":null}`, want: bookmarkTestJSON{Chapter: 1, Percent: 0.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			before := time.Now().UTC().Truncate(time.Second)
			w := c.request(t, http.MethodPost, "/api/books/book/bookmarks", tc.body)
			got := decodeBookmarkTestResponse(t, w, http.StatusCreated)
			if id, err := hex.DecodeString(got.ID); err != nil || len(id) != 8 || strings.ToLower(got.ID) != got.ID {
				t.Fatalf("server bookmark ID = %q, %v", got.ID, err)
			}
			created, err := time.Parse(time.DateTime, got.CreatedAt)
			if err != nil || created.Before(before) || created.After(time.Now().UTC()) {
				t.Fatalf("server CreatedAt = %q, %v", got.CreatedAt, err)
			}
			want := tc.want
			want.ID, want.CreatedAt = got.ID, got.CreatedAt
			assertSettingsResponse(t, w, http.StatusCreated, want)
			record := storage.BookmarkRecord{
				ID: want.ID, BookID: "book", UserID: "default", Chapter: want.Chapter, Percent: want.Percent,
				CFI:   sql.NullString{String: want.CFI, Valid: want.CFI != ""},
				Label: want.Label, Comment: want.Comment, CreatedAt: want.CreatedAt,
			}
			assertBookmarkTestRecords(t, c.pd, "book", "default", record)
			assertBookmarkTestRecords(t, c.pd, "book", "other")
			assertBookmarkTestRecords(t, c.pd, "other", "default")
			assertSettingsResponse(t, c.request(t, http.MethodGet, "/api/books/book/bookmarks", ""), http.StatusOK, []bookmarkTestJSON{want})
		})
	}
}

func TestBookmarkCreateRejectsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, book, body, message string
	}{
		{name: "empty body", body: "", message: "invalid JSON body"},
		{name: "malformed", body: "{", message: "invalid JSON body"},
		{name: "array", body: "[]", message: "invalid JSON body"},
		{name: "scalar", body: "42", message: "invalid JSON body"},
		{name: "trailing value", body: `{"chapter":0} {}`, message: "invalid JSON body"},
		{name: "trailing junk", body: `{"chapter":0} junk`, message: "invalid JSON body"},
		{name: "chapter type", body: `{"chapter":"0"}`, message: "invalid JSON body"},
		{name: "fractional chapter", body: `{"chapter":0.5}`, message: "invalid JSON body"},
		{name: "chapter overflow", body: `{"chapter":1e30}`, message: "invalid JSON body"},
		{name: "negative chapter", body: `{"chapter":-1}`, message: "chapter must be >= 0"},
		{name: "chapter at count", body: `{"chapter":3}`, message: "chapter index out of range"},
		{name: "empty book", book: "empty", body: `{"chapter":0}`, message: "chapter index out of range"},
		{name: "percent below zero", body: `{"percent":-0.01}`, message: "percent must be 0-1"},
		{name: "percent above one", body: `{"percent":1.01}`, message: "percent must be 0-1"},
		{name: "percent overflow", body: `{"percent":1e400}`, message: "invalid JSON body"},
		{name: "NaN", body: `{"percent":NaN}`, message: "invalid JSON body"},
		{name: "CFI type", body: `{"cfi":false}`, message: "invalid JSON body"},
		{name: "label type", body: `{"label":[]}`, message: "invalid JSON body"},
		{name: "comment type", body: `{"comment":{}}`, message: "invalid JSON body"},
		{name: "long label", body: fmt.Sprintf(`{"label":%q}`, strings.Repeat("x", 2001)), message: "label too long"},
		{name: "multibyte label", body: fmt.Sprintf(`{"label":%q}`, strings.Repeat("🙂", 500)+"x"), message: "label too long"},
		{name: "long comment", body: fmt.Sprintf(`{"comment":%q}`, strings.Repeat("x", 2001)), message: "comment too long"},
		{name: "multibyte comment", body: fmt.Sprintf(`{"comment":%q}`, strings.Repeat("م", 1001)), message: "comment too long"},
		{name: "missing book before decode", book: "missing", body: "{", message: "book not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "kept", BookID: "book", UserID: "default", Label: "Keep"})
			book := tc.book
			if book == "" {
				book = "book"
			}
			status, code := http.StatusBadRequest, "invalid_body"
			if book == "missing" {
				status, code = http.StatusNotFound, "not_found"
			}
			w := c.request(t, http.MethodPost, "/api/books/"+book+"/bookmarks", tc.body)
			assertSettingsError(t, w, status, apiError{Code: code, Error: tc.message})
			assertBookmarkTestRecords(t, c.pd, "book", "default", kept)
			assertBookmarkTestRecords(t, c.pd, "empty", "default")
		})
	}
}

func TestBookmarkCreateDuringBookDeletion(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"stale cache", "after existence check"} {
		t.Run(name, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "kept", BookID: "other", UserID: "default", Label: "Keep"})
			deleted := false
			deleteBook := func() {
				if err := c.pd.DB.DeleteBookContext(t.Context(), "book"); err != nil {
					t.Fatal(err)
				}
				deleted = true
			}
			var body io.Reader = strings.NewReader(`{"chapter":1,"percent":0.5}`)
			if name == "stale cache" {
				deleteBook()
			} else {
				// Body decoding follows the cached existence check. Remove the
				// DB row here to reproduce that interleaving without sleeps.
				body = &bookmarkReadHook{reader: body, before: deleteBook}
			}
			w := c.serve(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/books/book/bookmarks", body))
			if !deleted {
				t.Fatal("book deletion was not exercised")
			}
			assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "book not found"})
			assertBookmarkTestRecords(t, c.pd, "book", "default")
			assertBookmarkTestRecords(t, c.pd, "other", "default", kept)
		})
	}
}

func TestBookmarkUpdatePreservesLocation(t *testing.T) {
	t.Parallel()
	c := newBookmarkTestClient(t)
	before := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{
		ID: "mark", BookID: "book", UserID: "default", Chapter: 2, Percent: 0.875,
		CFI: sql.NullString{String: "epubcfi(/6/6)", Valid: true}, Label: "Original", Comment: "Original note",
	})
	for _, tc := range []struct{ name, label, comment string }{
		{name: "byte limits", label: strings.Repeat("🙂", 500), comment: strings.Repeat("x", 2000)},
		{name: "whitespace preserved", label: " \tمرحبا <&> ", comment: "\nNote\n"},
		{name: "clear text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"label": tc.label, "comment": tc.comment, "chapter": 0, "percent": 0,
				"cfi": "replace-me", "id": "new-id", "bookId": "other", "userId": "other", "createdAt": "1999-01-01 00:00:00",
			})
			if err != nil {
				t.Fatal(err)
			}
			want := before
			want.Label, want.Comment = tc.label, tc.comment
			w := c.request(t, http.MethodPatch, "/api/books/book/bookmarks/mark", string(body))
			assertSettingsResponse(t, w, http.StatusOK, bookmarkTestWire(want))
			assertBookmarkTestRecords(t, c.pd, "book", "default", want)
		})
	}
}

func TestBookmarkUpdateRejectsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, message string }{
		{name: "empty body", message: "invalid JSON body"},
		{name: "malformed", body: "{", message: "invalid JSON body"},
		{name: "array", body: "[]", message: "invalid JSON body"},
		{name: "scalar", body: "true", message: "invalid JSON body"},
		{name: "trailing value", body: `{"label":"changed"} {}`, message: "invalid JSON body"},
		{name: "trailing junk", body: `{"label":"changed"} junk`, message: "invalid JSON body"},
		{name: "label type", body: `{"label":42}`, message: "invalid JSON body"},
		{name: "comment type", body: `{"comment":false}`, message: "invalid JSON body"},
		{name: "long label", body: fmt.Sprintf(`{"label":%q}`, strings.Repeat("x", 2001)), message: "label too long"},
		{name: "multibyte label", body: fmt.Sprintf(`{"label":%q}`, strings.Repeat("🙂", 500)+"x"), message: "label too long"},
		{name: "long comment", body: fmt.Sprintf(`{"comment":%q}`, strings.Repeat("x", 2001)), message: "comment too long"},
		{name: "multibyte comment", body: fmt.Sprintf(`{"comment":%q}`, strings.Repeat("م", 1001)), message: "comment too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "kept", BookID: "book", UserID: "default", Label: "Keep", Comment: "Keep note"})
			w := c.request(t, http.MethodPatch, "/api/books/book/bookmarks/kept", tc.body)
			assertSettingsError(t, w, http.StatusBadRequest, apiError{Code: "invalid_body", Error: tc.message})
			assertBookmarkTestRecords(t, c.pd, "book", "default", kept)
		})
	}
}

func TestBookmarkUpdateAfterLookup(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"deleted", "canceled"} {
		t.Run(name, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "mark", BookID: "book", UserID: "default", Label: "Original"})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			hookRan := false
			body := &bookmarkReadHook{
				reader: strings.NewReader(`{"label":"Changed","comment":"Changed"}`),
				before: func() {
					hookRan = true
					if name == "deleted" {
						if err := c.pd.DB.DeleteBookmarkContext(t.Context(), "mark", "book", "default"); err != nil {
							t.Fatal(err)
						}
					} else {
						cancel()
					}
				},
			}
			w := c.serve(httptest.NewRequestWithContext(ctx, http.MethodPatch, "/api/books/book/bookmarks/mark", body))
			if !hookRan {
				t.Fatal("post-lookup interleaving was not exercised")
			}
			if name == "deleted" {
				assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "bookmark not found"})
				assertBookmarkTestRecords(t, c.pd, "book", "default")
			} else {
				assertSettingsError(t, w, http.StatusInternalServerError, apiError{Code: "db_error", Error: "failed to update bookmark"})
				assertBookmarkTestRecords(t, c.pd, "book", "default", kept)
			}
		})
	}
}

func TestBookmarkMutationIsolationAndDelete(t *testing.T) {
	t.Parallel()
	c := newBookmarkTestClient(t)
	owned := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "owned", BookID: "book", UserID: "default", Label: "Owned"})
	foreign := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "foreign", BookID: "book", UserID: "other", Label: "Other user"})
	otherBook := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "other-book", BookID: "other", UserID: "default", Label: "Other book"})
	other := newBookmarkTestClient(t)
	otherProfile := seedBookmarkTestRecord(t, other.pd, storage.BookmarkRecord{ID: "owned", BookID: "book", UserID: "default", Label: "Other profile"})
	profileOnly := seedBookmarkTestRecord(t, other.pd, storage.BookmarkRecord{ID: "profile-only", BookID: "book", UserID: "default", Label: "Not here"})
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		for _, tc := range []struct{ name, book, id string }{
			{name: "wrong book", book: "other", id: "owned"},
			{name: "wrong owner", book: "book", id: "foreign"},
			{name: "missing", book: "book", id: "missing"},
			{name: "other profile only", book: "book", id: "profile-only"},
			{name: "SQL-like ID", book: "book", id: "' OR 1=1 --"},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				path := "/api/books/" + tc.book + "/bookmarks/" + url.PathEscape(tc.id)
				// Scope checks precede decoding; a malformed PATCH cannot hide
				// a missing or foreign bookmark behind a different error.
				w := c.request(t, method, path, "{")
				assertSettingsError(t, w, http.StatusNotFound, apiError{Code: "not_found", Error: "bookmark not found"})
				assertBookmarkTestRecords(t, c.pd, "book", "default", owned)
				assertBookmarkTestRecords(t, c.pd, "book", "other", foreign)
				assertBookmarkTestRecords(t, c.pd, "other", "default", otherBook)
				assertBookmarkTestRecords(t, other.pd, "book", "default", otherProfile, profileOnly)
			})
		}
	}
	w := c.request(t, http.MethodDelete, "/api/books/book/bookmarks/owned", "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Fatalf("delete response: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	assertBookmarkTestRecords(t, c.pd, "book", "default")
	assertBookmarkTestRecords(t, c.pd, "book", "other", foreign)
	assertBookmarkTestRecords(t, c.pd, "other", "default", otherBook)
	assertBookmarkTestRecords(t, other.pd, "book", "default", otherProfile, profileOnly)
	assertSettingsError(t, c.request(t, http.MethodDelete, "/api/books/book/bookmarks/owned", ""), http.StatusNotFound, apiError{Code: "not_found", Error: "bookmark not found"})
	if _, err := c.pd.DB.GetBookmarkContext(t.Context(), "owned", "default"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleted bookmark still exists: %v", err)
	}
}

func TestBookmarkRequestBodyLimit(t *testing.T) {
	t.Parallel()
	const limit = 64 << 10
	const payload = `{"chapter":0,"percent":0.25,"label":"Updated","comment":"Note"}`
	exact := payload + strings.Repeat(" ", limit-len(payload))
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for _, tc := range []struct{ name, body string }{
			{name: "exact limit", body: exact},
			{name: "one byte over", body: exact + " "},
			{name: "oversized first value", body: `{"label":"` + strings.Repeat("x", limit) + `"}`},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				c := newBookmarkTestClient(t)
				kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "mark", BookID: "book", UserID: "default", Label: "Original"})
				path := "/api/books/book/bookmarks"
				if method == http.MethodPatch {
					path += "/mark"
				}
				w := c.request(t, method, path, tc.body)
				if tc.name != "exact limit" {
					assertSettingsError(t, w, http.StatusRequestEntityTooLarge, apiError{Code: "too_large", Error: "request body too large"})
					assertBookmarkTestRecords(t, c.pd, "book", "default", kept)
					return
				}
				want := kept
				want.Label, want.Comment = "Updated", "Note"
				if method == http.MethodPost {
					got := decodeBookmarkTestResponse(t, w, http.StatusCreated)
					want.ID, want.CreatedAt, want.Percent = got.ID, got.CreatedAt, 0.25
					assertSettingsResponse(t, w, http.StatusCreated, bookmarkTestWire(want))
					assertBookmarkTestRecords(t, c.pd, "book", "default", kept, want)
				} else {
					assertSettingsResponse(t, w, http.StatusOK, bookmarkTestWire(want))
					assertBookmarkTestRecords(t, c.pd, "book", "default", want)
				}
			})
		}
	}
}

func TestBookmarkHandlersMissingProfileAndCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, path, body, message string }{
		{http.MethodGet, "/api/books/book/bookmarks", "", "failed to list bookmarks"},
		{http.MethodPost, "/api/books/book/bookmarks", `{"chapter":0,"percent":0.5}`, "failed to create bookmark"},
		{http.MethodPatch, "/api/books/book/bookmarks/mark", `{"label":"Changed","comment":"Changed"}`, "failed to load bookmark"},
		{http.MethodDelete, "/api/books/book/bookmarks/mark", "", "failed to delete bookmark"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			c := newBookmarkTestClient(t)
			kept := seedBookmarkTestRecord(t, c.pd, storage.BookmarkRecord{ID: "mark", BookID: "book", UserID: "default", Label: "Original"})
			w := httptest.NewRecorder()
			c.mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body)))
			assertSettingsError(t, w, http.StatusInternalServerError, apiError{Code: "server_error", Error: "profile not available"})
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			w = c.serve(httptest.NewRequestWithContext(ctx, tc.method, tc.path, strings.NewReader(tc.body)))
			assertSettingsError(t, w, http.StatusInternalServerError, apiError{Code: "db_error", Error: tc.message})
			assertBookmarkTestRecords(t, c.pd, "book", "default", kept)
		})
	}
}

func TestBookmarkRoutesRequireSession(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{sessions: newSessionStore(nil)}
	handler := NewHandler(deps, http.NotFoundHandler(), http.NotFoundHandler())
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/books/book/bookmarks", ""},
		{http.MethodPost, "/api/books/book/bookmarks", `{"chapter":0,"percent":0.5}`},
		{http.MethodPatch, "/api/books/book/bookmarks/mark", `{"label":"Changed","comment":"Changed"}`},
		{http.MethodDelete, "/api/books/book/bookmarks/mark", ""},
	} {
		for _, cookie := range []string{"", "unknown"} {
			t.Run(tc.method+"/cookie="+cookie, func(t *testing.T) {
				r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
				if cookie != "" {
					r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, withProfileDeps(r, &profileDeps{}))
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

// These fields mirror the client contract independently of bookmarkResponse.
// Exact JSON assertions also catch leaked book/user IDs and missing empty text.
type bookmarkTestJSON struct {
	ID        string  `json:"id"`
	Chapter   int     `json:"chapter"`
	Percent   float64 `json:"percent"`
	CFI       string  `json:"cfi,omitempty"`
	Label     string  `json:"label"`
	Comment   string  `json:"comment"`
	CreatedAt string  `json:"createdAt"`
}

func bookmarkTestWire(b storage.BookmarkRecord) bookmarkTestJSON {
	cfi := ""
	if b.CFI.Valid {
		cfi = b.CFI.String
	}
	return bookmarkTestJSON{ID: b.ID, Chapter: b.Chapter, Percent: b.Percent, CFI: cfi, Label: b.Label, Comment: b.Comment, CreatedAt: b.CreatedAt}
}

type bookmarkTestClient struct {
	pd  *profileDeps
	mux *http.ServeMux
}

func newBookmarkTestClient(t *testing.T) bookmarkTestClient {
	t.Helper()
	// The shared fixture owns auth's simulated reference and verifies that
	// handlers only borrow it. Real temporary SQLite needs no service or tag.
	pd := settingsTestProfile(t)
	for _, book := range []struct {
		id       string
		chapters int
	}{{"book", 3}, {"other", 3}, {"empty", 0}} {
		record := storage.BookRecord{
			ID: book.id, Title: book.id, ChapterCount: book.chapters,
			FilePath: filepath.Join(pd.LibPath, book.id+".epub"), FileHash: "hash-" + book.id,
		}
		if id, err := pd.DB.InsertBookContext(t.Context(), record); err != nil || id != book.id {
			t.Fatalf("insert book %q = %q, %v", book.id, id, err)
		}
	}
	books, err := storage.NewBookCache(t.Context(), pd.DB)
	if err != nil {
		t.Fatal(err)
	}
	pd.Books = books
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/books/{id}/bookmarks", listBookmarksHandler(nil))
	mux.HandleFunc("POST /api/books/{id}/bookmarks", createBookmarkHandler(nil))
	mux.HandleFunc("PATCH /api/books/{id}/bookmarks/{bid}", updateBookmarkHandler(nil))
	mux.HandleFunc("DELETE /api/books/{id}/bookmarks/{bid}", deleteBookmarkHandler(nil))
	return bookmarkTestClient{pd: pd, mux: mux}
}

func (c bookmarkTestClient) request(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	// Caller-selected identity must never replace the authenticated profile
	// or its current single-user "default" scope.
	r := httptest.NewRequestWithContext(t.Context(), method, path+"?userId=other&profile=foreign", strings.NewReader(body))
	return c.serve(r)
}

func (c bookmarkTestClient) serve(r *http.Request) *httptest.ResponseRecorder {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-User-Id", "other")
	w := httptest.NewRecorder()
	c.mux.ServeHTTP(w, withProfileDeps(r, c.pd))
	return w
}

func seedBookmarkTestRecord(t *testing.T, pd *profileDeps, record storage.BookmarkRecord) storage.BookmarkRecord {
	t.Helper()
	if record.CreatedAt == "" {
		record.CreatedAt = "2026-01-02 03:04:05"
	}
	if err := pd.DB.InsertBookmarkContext(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func assertBookmarkTestRecords(t *testing.T, pd *profileDeps, book, user string, want ...storage.BookmarkRecord) {
	t.Helper()
	got, err := pd.DB.ListBookmarksContext(t.Context(), book, user)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("bookmarks for %q/%q = (%+v, %v), want %+v", book, user, got, err, want)
	}
}

func decodeBookmarkTestResponse(t *testing.T, w *httptest.ResponseRecorder, status int) bookmarkTestJSON {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	var got bookmarkTestJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

type bookmarkReadHook struct {
	reader io.Reader
	before func()
}

func (r *bookmarkReadHook) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.reader.Read(p)
}
