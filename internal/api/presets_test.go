package api

import (
	"strings"
	"testing"
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
