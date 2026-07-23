package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBodyRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{
			name:   "single value",
			body:   `{"name":"Sayumi"}`,
			wantOK: true,
		},
		{
			name:   "trailing whitespace",
			body:   "{\"name\":\"Sayumi\"}\n\t  ",
			wantOK: true,
		},
		{
			name:       "second object",
			body:       `{"name":"Sayumi"}{"extra":true}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "second scalar",
			body:       `{"name":"Sayumi"} true`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "trailing garbage",
			body:       `{"name":"Sayumi"} garbage`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "oversized trailing whitespace",
			body:       `{"name":"Sayumi"}` + strings.Repeat(" ", maxJSONBodySize),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			recorder := httptest.NewRecorder()
			var body struct {
				Name string `json:"name"`
			}

			gotOK := decodeJSONBody(recorder, req, &body)
			if gotOK != tc.wantOK {
				t.Fatalf("decodeJSONBody() = %v, want %v; status = %d, body = %s",
					gotOK, tc.wantOK, recorder.Code, recorder.Body.String())
			}
			if tc.wantOK {
				if body.Name != "Sayumi" {
					t.Errorf("decoded name = %q, want Sayumi", body.Name)
				}
				return
			}
			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body = %s",
					recorder.Code, tc.wantStatus, recorder.Body.String())
			}
		})
	}
}
