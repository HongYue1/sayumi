package api

import (
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestApplyBookMetaPatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		curTitle  string
		curAuthor string
		req       updateBookRequest
		wantTitle string
		wantAuth  string
		wantErr   string
	}{
		{
			name:      "omit both keeps current",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{},
			wantTitle: "Old",
			wantAuth:  "Ada",
		},
		{
			name:      "title only",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Title: strPtr("  New Title  ")},
			wantTitle: "New Title",
			wantAuth:  "Ada",
		},
		{
			name:      "author only including empty",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Author: strPtr("  ")},
			wantTitle: "Old",
			wantAuth:  "",
		},
		{
			name:      "both fields",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Title: strPtr("T"), Author: strPtr("B")},
			wantTitle: "T",
			wantAuth:  "B",
		},
		{
			name:     "empty title rejected",
			curTitle: "Old",
			req:      updateBookRequest{Title: strPtr("   ")},
			wantErr:  "title must not be empty",
		},
		{
			name:     "title too long",
			curTitle: "Old",
			req:      updateBookRequest{Title: strPtr(strings.Repeat("x", maxBookTitleLen+1))},
			wantErr:  "title too long",
		},
		{
			name:      "author too long",
			curTitle:  "Old",
			curAuthor: "A",
			req:       updateBookRequest{Author: strPtr(strings.Repeat("y", maxBookAuthorLen+1))},
			wantErr:   "author too long",
		},
		{
			name:      "max length title ok",
			curTitle:  "Old",
			curAuthor: "A",
			req:       updateBookRequest{Title: strPtr(strings.Repeat("z", maxBookTitleLen))},
			wantTitle: strings.Repeat("z", maxBookTitleLen),
			wantAuth:  "A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotTitle, gotAuth, errMsg := applyBookMetaPatch(tc.curTitle, tc.curAuthor, tc.req)
			if tc.wantErr != "" {
				if errMsg != tc.wantErr {
					t.Fatalf("errMsg = %q, want %q", errMsg, tc.wantErr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("unexpected errMsg %q", errMsg)
			}
			if gotTitle != tc.wantTitle || gotAuth != tc.wantAuth {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotTitle, gotAuth, tc.wantTitle, tc.wantAuth)
			}
		})
	}
}
