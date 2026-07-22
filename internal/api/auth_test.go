package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidatePIN(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestValidateProfileName(t *testing.T) {
	t.Parallel()
	// Max is 1–32: first + up to 30 middle + last = 32 when both ends are alnum.
	ok := []string{
		"a",
		"Ada",
		"user 1",
		"my-profile",
		"my_profile",
		"A" + strings.Repeat("b", 30),       // 31
		"A" + strings.Repeat("b", 30) + "Z", // 32
	}
	for _, name := range ok {
		if !validateProfileName(name) {
			t.Errorf("validateProfileName(%q) = false, want true", name)
		}
	}
	bad := []string{
		"",
		" ",
		"-leading",
		"trailing-",
		"has/slash",
		`has\backslash`,
		"has:colon",
		"has*star",
		"dot..dot",
		"a..b",
		"A" + strings.Repeat("b", 31) + "Z", // 33
		"日本語",
	}
	for _, name := range bad {
		if validateProfileName(name) {
			t.Errorf("validateProfileName(%q) = true, want false", name)
		}
	}
}

func TestSessionStoreLifecycle(t *testing.T) {
	t.Parallel()

	ss := newSessionStore(nil)

	token, sess, err := ss.create("alice", false)
	if err != nil || token == "" || sess.profile != "alice" || sess.remember {
		t.Fatalf("create non-remember: token=%q sess=%+v err=%v", token, sess, err)
	}
	got, ok := ss.get(token)
	if !ok || got.profile != "alice" {
		t.Fatalf("get = %+v %v", got, ok)
	}

	// Remember session.
	rTok, rSess, err := ss.create("bob", true)
	if err != nil || !rSess.remember || rSess.expiry.Before(time.Now().Add(24*time.Hour)) {
		t.Fatalf("create remember: %+v err=%v", rSess, err)
	}
	if _, ok := ss.get(rTok); !ok {
		t.Fatal("remember get miss")
	}

	// deleteToken
	ss.deleteToken(token)
	if _, ok := ss.get(token); ok {
		t.Fatal("deleted token still present")
	}

	// deleteAllForProfile
	t2, _, err := ss.create("bob", false)
	if err != nil {
		t.Fatal(err)
	}
	ss.deleteAllForProfile("bob")
	if _, ok := ss.get(rTok); ok {
		t.Fatal("bob remember still present")
	}
	if _, ok := ss.get(t2); ok {
		t.Fatal("bob session still present")
	}

	// Expired session: inject and get/sweep.
	expTok := "expiredtoken000000000000000000000000000000000000000000000000"
	ss.mu.Lock()
	ss.data[expTok] = session{profile: "carol", expiry: time.Now().Add(-time.Minute)}
	ss.mu.Unlock()
	if _, ok := ss.get(expTok); ok {
		t.Fatal("get should drop expired")
	}

	ss.mu.Lock()
	ss.data[expTok] = session{profile: "carol", expiry: time.Now().Add(-time.Minute)}
	ss.data["live"] = session{profile: "dave", expiry: time.Now().Add(time.Hour)}
	ss.mu.Unlock()
	ss.sweep()
	if _, ok := ss.get(expTok); ok {
		t.Fatal("sweep left expired")
	}
	if _, ok := ss.get("live"); !ok {
		t.Fatal("sweep removed live")
	}

	// markProfileVerified no-op on missing; works on live.
	ss.markProfileVerified("missing", time.Now().Add(time.Minute))
	until := time.Now().Add(time.Minute)
	ss.markProfileVerified("live", until)
	ss.mu.Lock()
	v := ss.data["live"].verifiedUntil
	ss.mu.Unlock()
	if v.Before(until.Add(-time.Second)) {
		t.Fatalf("verifiedUntil not set: %v", v)
	}
}

func TestSetCookieRememberVsSession(t *testing.T) {
	t.Parallel()

	// Session cookie (no remember): no MaxAge/Expires persistence fields set to positive.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setCookie(rr, req, "tok1", session{profile: "a", remember: false, expiry: time.Now().Add(time.Hour)})
	res := rr.Result()
	defer func() { _ = res.Body.Close() }()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookie || c.Value != "tok1" || !c.HttpOnly {
		t.Fatalf("cookie = %+v", c)
	}
	if c.MaxAge != 0 {
		t.Fatalf("session cookie MaxAge = %d, want 0", c.MaxAge)
	}

	// Remember: MaxAge > 0 and Expires set.
	rr2 := httptest.NewRecorder()
	exp := time.Now().Add(48 * time.Hour)
	setCookie(rr2, req, "tok2", session{profile: "a", remember: true, expiry: exp})
	res2 := rr2.Result()
	defer func() { _ = res2.Body.Close() }()
	c2 := res2.Cookies()[0]
	if c2.MaxAge <= 0 {
		t.Fatalf("remember MaxAge = %d", c2.MaxAge)
	}
	if c2.Expires.IsZero() {
		t.Fatal("remember Expires zero")
	}

	// clearCookie
	rr3 := httptest.NewRecorder()
	clearCookie(rr3, req)
	res3 := rr3.Result()
	defer func() { _ = res3.Body.Close() }()
	c3 := res3.Cookies()[0]
	if c3.MaxAge >= 0 && c3.Value != "" {
		// MaxAge -1 clears; Value empty
		if c3.MaxAge != -1 {
			t.Fatalf("clear MaxAge = %d", c3.MaxAge)
		}
	}
}

func TestCalcProgress(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			got := calcProgress(tc.chapter, tc.percent, tc.chapterCount)
			if got < tc.want-1e-9 || got > tc.want+1e-9 {
				t.Errorf("calcProgress(%d, %v, %d) = %v, want %v",
					tc.chapter, tc.percent, tc.chapterCount, got, tc.want)
			}
		})
	}
}
