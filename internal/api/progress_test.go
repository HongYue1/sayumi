package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sayumi/internal/storage"
)

func progressRequest(pd *profileDeps, method, body string) *http.Request {
	target := "/api/books/" + enrichBookID + "/progress"
	if method == http.MethodPost {
		target += "/beacon"
	}
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.SetPathValue("id", enrichBookID)
	r.Header.Set("Content-Type", "application/json")
	return withProfileDeps(r, pd)
}

func assertProgressResponse(t *testing.T, w *httptest.ResponseRecorder, want progressBody) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("content type = %q, want JSON", got)
	}
	var got progressBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode progress response: %v", err)
	}
	if got != want {
		t.Errorf("progress = %+v, want %+v", got, want)
	}
	if want.CFI == "" && strings.Contains(w.Body.String(), `"cfi"`) {
		t.Errorf("empty CFI must be omitted: %s", w.Body.String())
	}
}

func TestProgressHandlersRequireProfile(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{name: "get", method: http.MethodGet, handler: getProgressHandler(nil)},
		{name: "put", method: http.MethodPut, handler: putProgressHandler(nil)},
		{name: "beacon", method: http.MethodPost, handler: beaconProgressHandler(nil)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.handler(w, progressRequest(nil, tt.method, `{}`))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
			var got apiError
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Code != "server_error" {
				t.Errorf("error response = %+v, decode error = %v", got, err)
			}
		})
	}
}

func TestGetProgressHandlerStates(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		stored      *progressBody
		pending     *progressBody
		missingBook bool
		cancel      bool
		status      int
		code        string
		want        progressBody
	}{
		{name: "unread", status: http.StatusOK},
		{
			name: "persisted with CFI", status: http.StatusOK,
			stored: &progressBody{Chapter: 2, Percent: 0.25, CFI: "epubcfi(/6/4)"},
			want:   progressBody{Chapter: 2, Percent: 0.25, CFI: "epubcfi(/6/4)"},
		},
		{
			name: "persisted without CFI", status: http.StatusOK,
			stored: &progressBody{Chapter: 3, Percent: 0.5},
			want:   progressBody{Chapter: 3, Percent: 0.5},
		},
		{
			name: "staged wins", status: http.StatusOK,
			stored:  &progressBody{Chapter: 1, Percent: 0.1, CFI: "old"},
			pending: &progressBody{Chapter: 7, Percent: 0.75, CFI: "new"},
			want:    progressBody{Chapter: 7, Percent: 0.75, CFI: "new"},
		},
		{
			name: "staged clears CFI", status: http.StatusOK,
			stored:  &progressBody{Chapter: 1, CFI: "old"},
			pending: &progressBody{Chapter: 7, Percent: 0.75},
			want:    progressBody{Chapter: 7, Percent: 0.75},
		},
		{name: "missing book", missingBook: true, status: http.StatusNotFound, code: "not_found"},
		{name: "database failure", cancel: true, status: http.StatusInternalServerError, code: "db_error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pd := newEnrichDeps(t)
			if tt.stored != nil {
				rec := toProgressRecord(enrichBookID, "default", *tt.stored)
				if err := pd.DB.SaveProgressContext(t.Context(), rec); err != nil {
					t.Fatal(err)
				}
			}
			if tt.pending != nil {
				pd.Progress.stage(toProgressRecord(enrichBookID, "default", *tt.pending))
			}
			r := progressRequest(pd, http.MethodGet, "")
			if tt.missingBook {
				r.SetPathValue("id", "missing")
			}
			if tt.cancel {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			getProgressHandler(nil)(w, r)
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tt.status, w.Body.String())
			}
			if tt.status == http.StatusOK {
				assertProgressResponse(t, w, tt.want)
				return
			}
			var got apiError
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Code != tt.code {
				t.Errorf("error response = %+v, decode error = %v, want code %q", got, err, tt.code)
			}
		})
	}
}

func TestGetProgressHandlerDuringFlush(t *testing.T) {
	t.Parallel()
	pd := newEnrichDeps(t)
	pd.Progress.stop()
	saver, started, unblock := blockFirstProgressSave(pd.DB, nil)
	pd.Progress = newProgressCoalescer(saver, time.Hour, 1000)
	t.Cleanup(func() {
		unblock()
		pd.Progress.stop()
	})
	if err := pd.DB.SaveProgressContext(t.Context(), storage.ProgressRecord{
		BookID: enrichBookID, UserID: "default", Chapter: 1, Percent: 0.1,
	}); err != nil {
		t.Fatal(err)
	}

	want := progressBody{Chapter: 8, Percent: 0.5, CFI: "epubcfi(/6/18)"}
	w := httptest.NewRecorder()
	putProgressHandler(nil)(w, progressRequest(pd, http.MethodPut,
		`{"chapter":8,"percent":0.5,"cfi":"epubcfi(/6/18)"}`))
	assertProgressResponse(t, w, want)
	pd.Progress.flushSignal <- struct{}{}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not reach the blocked save")
	}

	// A completed PUT must remain visible even while its WAL write is blocked.
	w = httptest.NewRecorder()
	getProgressHandler(nil)(w, progressRequest(pd, http.MethodGet, ""))
	assertProgressResponse(t, w, want)

	unblock()
	pd.Progress.stop()
	w = httptest.NewRecorder()
	getProgressHandler(nil)(w, progressRequest(pd, http.MethodGet, ""))
	assertProgressResponse(t, w, want)
}

func TestValidateProgressEmptyBook(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		count int
	}{
		{name: "no chapters", count: 0},
		{name: "negative chapter count", count: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateProgress(progressBody{}, tt.count); got != "chapter index out of range" {
				t.Errorf("validation = %q, want chapter index out of range", got)
			}
		})
	}
}

func TestProgressWriteHandlers(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{name: "put", method: http.MethodPut, handler: putProgressHandler(nil)},
		{name: "beacon", method: http.MethodPost, handler: beaconProgressHandler(nil)},
	} {
		for _, tt := range []struct {
			name        string
			body        string
			want        progressBody
			status      int
			code        string
			bestEffort  bool
			missingBook bool
		}{
			{name: "origin clears CFI", body: `{"chapter":0,"percent":0}`, status: http.StatusOK},
			{
				name: "last chapter end", body: `{"chapter":9,"percent":1}`, status: http.StatusOK,
				want: progressBody{Chapter: 9, Percent: 1},
			},
			{
				name: "CFI preserved", body: `{"chapter":5,"percent":0.25,"cfi":"epubcfi(/6/12)"}`,
				status: http.StatusOK, want: progressBody{Chapter: 5, Percent: 0.25, CFI: "epubcfi(/6/12)"},
			},
			{
				name: "negative chapter", body: `{"chapter":-1,"percent":0}`,
				status: http.StatusBadRequest, code: "invalid_body", bestEffort: true,
			},
			{
				name: "chapter past end", body: `{"chapter":10,"percent":0}`,
				status: http.StatusBadRequest, code: "invalid_body", bestEffort: true,
			},
			{
				name: "negative percent", body: `{"chapter":1,"percent":-0.01}`,
				status: http.StatusBadRequest, code: "invalid_body", bestEffort: true,
			},
			{
				name: "percent past end", body: `{"chapter":1,"percent":1.01}`,
				status: http.StatusBadRequest, code: "invalid_body", bestEffort: true,
			},
			{
				name: "fractional chapter", body: `{"chapter":1.5,"percent":0}`,
				status: http.StatusBadRequest, code: "invalid_body",
			},
			{
				name: "wrong field type", body: `{"chapter":"1","percent":0}`,
				status: http.StatusBadRequest, code: "invalid_body",
			},
			{
				name: "nonfinite percent", body: `{"chapter":1,"percent":NaN}`,
				status: http.StatusBadRequest, code: "invalid_body",
			},
			{
				name: "overflowing percent", body: `{"chapter":1,"percent":1e999}`,
				status: http.StatusBadRequest, code: "invalid_body",
			},
			{name: "empty body", status: http.StatusBadRequest, code: "invalid_body"},
			{name: "malformed JSON", body: `{`, status: http.StatusBadRequest, code: "invalid_body"},
			{
				name: "second JSON value", body: `{"chapter":1,"percent":0} {}`,
				status: http.StatusBadRequest, code: "invalid_body",
			},
			{
				name: "oversized CFI", body: `{"chapter":1,"percent":0,"cfi":"` + strings.Repeat("x", maxJSONBodySize) + `"}`,
				status: http.StatusRequestEntityTooLarge, code: "too_large",
			},
			{
				name: "missing book", body: `{"chapter":1,"percent":0}`, missingBook: true,
				status: http.StatusNotFound, code: "not_found", bestEffort: true,
			},
		} {
			t.Run(endpoint.name+"/"+tt.name, func(t *testing.T) {
				pd := newEnrichDeps(t)
				initial := storage.ProgressRecord{
					BookID: enrichBookID, UserID: "default", Chapter: 3, Percent: 0.75,
					CFI: sql.NullString{String: "old", Valid: true},
				}
				if err := pd.DB.SaveProgressContext(t.Context(), initial); err != nil {
					t.Fatal(err)
				}
				pd.Progress.stage(initial)
				before, _ := pd.Progress.get(enrichBookID, "default")
				r := progressRequest(pd, endpoint.method, tt.body)
				if tt.missingBook {
					r.SetPathValue("id", "missing")
				}
				w := httptest.NewRecorder()
				endpoint.handler(w, r)
				wantStatus := tt.status
				if endpoint.method == http.MethodPost && (tt.status == http.StatusOK || tt.bestEffort) {
					wantStatus = http.StatusNoContent
				}
				if w.Code != wantStatus {
					t.Fatalf("status = %d, want %d; body = %s", w.Code, wantStatus, w.Body.String())
				}
				switch wantStatus {
				case http.StatusOK:
					assertProgressResponse(t, w, tt.want)
				case http.StatusNoContent:
					if w.Body.Len() != 0 {
						t.Errorf("beacon response must be empty: %s", w.Body.String())
					}
				default:
					var got apiError
					if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Code != tt.code {
						t.Errorf("error response = %+v, decode error = %v, want code %q", got, err, tt.code)
					}
				}

				after, ok := pd.Progress.get(enrichBookID, "default")
				if !ok {
					t.Fatal("progress unexpectedly disappeared")
				}
				if tt.status == http.StatusOK {
					if after.BookID != enrichBookID || after.UserID != "default" ||
						after.Chapter != tt.want.Chapter || after.Percent != tt.want.Percent ||
						after.CFI.String != tt.want.CFI || after.CFI.Valid != (tt.want.CFI != "") {
						t.Errorf("staged record = %+v, want %+v for the current book/user", after, tt.want)
					}
				} else if after != before {
					t.Errorf("rejected write changed pending progress: before=%+v after=%+v", before, after)
				}
				stored, err := pd.DB.GetProgressContext(t.Context(), enrichBookID, "default")
				if err != nil || stored.Chapter != initial.Chapter || stored.Percent != initial.Percent || stored.CFI != initial.CFI {
					t.Errorf("handler wrote synchronously: stored=%+v err=%v", stored, err)
				}
			})
		}
	}
}

func TestProgressHandlersProfileIsolation(t *testing.T) {
	t.Parallel()
	first := newEnrichDeps(t)
	second := newEnrichDeps(t)
	if err := first.DB.SaveProgressContext(t.Context(), storage.ProgressRecord{
		BookID: enrichBookID, UserID: "another-user", Chapter: 9, Percent: 1,
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	getProgressHandler(nil)(w, progressRequest(first, http.MethodGet, ""))
	assertProgressResponse(t, w, progressBody{})

	w = httptest.NewRecorder()
	putProgressHandler(nil)(w, progressRequest(first, http.MethodPut, `{"chapter":2,"percent":0.25}`))
	assertProgressResponse(t, w, progressBody{Chapter: 2, Percent: 0.25})
	w = httptest.NewRecorder()
	beaconProgressHandler(nil)(w, progressRequest(second, http.MethodPost, `{"chapter":6,"percent":0.5}`))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("beacon response: status=%d body=%s", w.Code, w.Body.String())
	}

	for _, tt := range []struct {
		name string
		pd   *profileDeps
		want progressBody
	}{
		{name: "first", pd: first, want: progressBody{Chapter: 2, Percent: 0.25}},
		{name: "second", pd: second, want: progressBody{Chapter: 6, Percent: 0.5}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			getProgressHandler(nil)(w, progressRequest(tt.pd, http.MethodGet, ""))
			assertProgressResponse(t, w, tt.want)
			tt.pd.Progress.stop()
			w = httptest.NewRecorder()
			getProgressHandler(nil)(w, progressRequest(tt.pd, http.MethodGet, ""))
			assertProgressResponse(t, w, tt.want)
		})
	}
}
