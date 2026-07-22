package api

import (
	"strings"
	"testing"
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
