package api

import "testing"

func TestValidatePIN(t *testing.T) {
	tests := []struct {
		pin    string
		wantOK bool
	}{
		{"", true},     // empty = open profile
		{"1234", true}, // min length
		{"123456789012", true},
		{"123", false},           // too short
		{"1234567890123", false}, // too long
		{"12a4", false},          // non-digit
		{"abcd", false},
		{"12 4", false},
	}
	for _, tc := range tests {
		if _, ok := validatePIN(tc.pin); ok != tc.wantOK {
			t.Errorf("validatePIN(%q) ok = %v, want %v", tc.pin, ok, tc.wantOK)
		}
	}
}

func TestHashAndVerifyPIN(t *testing.T) {
	// Open profile: empty hash, any pin verifies.
	hash, err := hashPIN("")
	if err != nil {
		t.Fatalf("hashPIN(\"\") error: %v", err)
	}
	if hash != "" {
		t.Errorf("hashPIN(\"\") = %q, want empty", hash)
	}
	if ok, _ := verifyPIN("", "anything"); !ok {
		t.Error("verifyPIN with empty hash should always succeed")
	}

	// Real PIN: correct verifies, wrong does not.
	hash, err = hashPIN("4242")
	if err != nil {
		t.Fatalf("hashPIN error: %v", err)
	}
	if hash == "" || hash == "4242" {
		t.Errorf("hashPIN should return a bcrypt hash, got %q", hash)
	}
	if ok, err := verifyPIN(hash, "4242"); err != nil || !ok {
		t.Errorf("verifyPIN(correct) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := verifyPIN(hash, "0000"); err != nil || ok {
		t.Errorf("verifyPIN(wrong) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCalcProgress(t *testing.T) {
	tests := []struct {
		name         string
		chapter      int
		percent      float64
		chapterCount int
		want         float64
	}{
		{"start", 0, 0, 10, 0},
		{"mid-first-chapter", 0, 0.5, 10, 0.05},
		{"second chapter", 1, 0, 10, 0.1},
		{"end", 9, 1, 10, 1},
		{"zero chapters is safe", 0, 0.5, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calcProgress(tc.chapter, tc.percent, tc.chapterCount)
			if got < tc.want-1e-9 || got > tc.want+1e-9 {
				t.Errorf("calcProgress(%d, %v, %d) = %v, want %v",
					tc.chapter, tc.percent, tc.chapterCount, got, tc.want)
			}
		})
	}
}
