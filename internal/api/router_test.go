package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	first := newUserFontToken()
	second := newUserFontToken()
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
