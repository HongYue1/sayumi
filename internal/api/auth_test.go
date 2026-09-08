package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"sayumi/internal/storage"
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
	ctx := t.Context()

	token, sess, err := ss.create(ctx, "alice", false)
	if err != nil || token == "" || sess.profile != "alice" || sess.remember {
		t.Fatalf("create non-remember: token=%q sess=%+v err=%v", token, sess, err)
	}
	got, ok := ss.get(token)
	if !ok || got.profile != "alice" {
		t.Fatalf("get = %+v %v", got, ok)
	}

	// Remember session.
	rTok, rSess, err := ss.create(ctx, "bob", true)
	if err != nil || !rSess.remember || rSess.expiry.Before(time.Now().Add(24*time.Hour)) {
		t.Fatalf("create remember: %+v err=%v", rSess, err)
	}
	if _, ok := ss.get(rTok); !ok {
		t.Fatal("remember get miss")
	}

	// deleteToken
	if err := ss.deleteToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, ok := ss.get(token); ok {
		t.Fatal("deleted token still present")
	}

	// deleteAllForProfile
	t2, _, err := ss.create(ctx, "bob", false)
	if err != nil {
		t.Fatal(err)
	}
	ss.deleteAllForProfile(ctx, "bob")
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
	ss.sweep(ctx)
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
	for _, tc := range []struct {
		name      string
		target    string
		forwarded bool
		secure    bool
	}{
		{name: "localhost HTTP", target: "http://localhost/"},
		{name: "actual TLS", target: "https://localhost/", secure: true},
		{name: "untrusted forwarded TLS", target: "http://localhost/", forwarded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if tc.forwarded {
				req.Header.Set("X-Forwarded-Proto", "https")
				req.Header.Set("Forwarded", "proto=https")
			}
			checkFlags := func(c *http.Cookie) {
				t.Helper()
				if c.Name != sessionCookie || c.Path != "/" || c.Domain != "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != tc.secure {
					t.Errorf("cookie scope/security flags = %+v", c)
				}
			}
			for _, remember := range []bool{false, true} {
				w := httptest.NewRecorder()
				exp := time.Now().Add(time.Hour)
				setCookie(w, req, "token", session{profile: "reader", remember: remember, expiry: exp})
				c := authTestCookie(t, w)
				checkFlags(c)
				if c.Value != "token" {
					t.Error("cookie lost the issued token")
				}
				if remember {
					if c.MaxAge <= 0 || c.MaxAge > int(time.Hour.Seconds()) || !c.Expires.Equal(exp.Truncate(time.Second)) {
						t.Errorf("remembered cookie persistence = %+v", c)
					}
				} else if c.MaxAge != 0 || !c.Expires.IsZero() {
					t.Errorf("ordinary cookie was made persistent: %+v", c)
				}
			}
			w := httptest.NewRecorder()
			clearCookie(w, req)
			c := authTestCookie(t, w)
			checkFlags(c)
			if c.Value != "" || c.MaxAge != -1 || c.Expires.IsZero() || !c.Expires.Before(time.Now()) {
				t.Errorf("cookie was not cleared: %+v", c)
			}
		})
	}
}

func newAuthTestDependencies(t *testing.T) *Dependencies {
	t.Helper()
	root := t.TempDir()
	profiles, err := storage.OpenProfilesDB(root)
	if err != nil {
		t.Fatal(err)
	}
	pm := NewProfileManager(root)
	t.Cleanup(func() {
		pm.CloseAll()
		if err := profiles.Close(); err != nil {
			t.Errorf("close profiles: %v", err)
		}
	})
	deps, err := NewDependencies(profiles, pm, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return deps
}

type authTestPersistence struct {
	sessionPersistence
	saveSession   func(context.Context, string, string, time.Time) error
	deleteSession func(context.Context, string) error
	deleteProfile func(context.Context, string) error
	pruneSessions func(context.Context, time.Time) error
	loadSessions  func(context.Context) ([]storage.PersistedSession, error)
}

func (p *authTestPersistence) SaveSession(ctx context.Context, token, profile string, expiry time.Time) error {
	if p.saveSession != nil {
		return p.saveSession(ctx, token, profile, expiry)
	}
	return p.sessionPersistence.SaveSession(ctx, token, profile, expiry)
}

func (p *authTestPersistence) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if p.pruneSessions != nil {
		return p.pruneSessions(ctx, now)
	}
	return p.sessionPersistence.DeleteExpiredSessions(ctx, now)
}

func (p *authTestPersistence) LoadSessions(ctx context.Context) ([]storage.PersistedSession, error) {
	if p.loadSessions != nil {
		return p.loadSessions(ctx)
	}
	return p.sessionPersistence.LoadSessions(ctx)
}

func (p *authTestPersistence) DeleteSession(ctx context.Context, token string) error {
	if p.deleteSession != nil {
		return p.deleteSession(ctx, token)
	}
	return p.sessionPersistence.DeleteSession(ctx, token)
}

func (p *authTestPersistence) DeleteSessionsForProfile(ctx context.Context, profile string) error {
	if p.deleteProfile != nil {
		return p.deleteProfile(ctx, profile)
	}
	return p.sessionPersistence.DeleteSessionsForProfile(ctx, profile)
}

func authTestRequest(method, target, body, token string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	return r
}

func TestLogoutCanceledRequestRevokesRememberedSession(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := authTestRequest(http.MethodPost, "/api/auth/logout", "", token).WithContext(ctx)
	w := httptest.NewRecorder()
	logoutHandler(deps)(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, ok := deps.sessions.get(token); ok {
		t.Error("logged-out session remains in memory")
	}
	restarted := newSessionStore(deps.ProfilesDB)
	if err := restarted.restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.get(token); ok {
		t.Error("canceled logout resurrected the remembered session after restart")
	}
}

func TestLogoutPersistenceFailureCanRetry(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	persist := &authTestPersistence{
		sessionPersistence: deps.ProfilesDB,
		deleteSession: func(context.Context, string) error {
			return errors.New("injected delete failure")
		},
	}
	deps.sessions.persist = persist
	w := httptest.NewRecorder()
	logoutHandler(deps)(w, authTestRequest(http.MethodPost, "/api/auth/logout", "", token))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("failed revocation status = %d, want 500", w.Code)
	}
	if len(w.Header().Values("Set-Cookie")) != 0 {
		t.Error("failed revocation discarded the cookie needed to retry")
	}
	if _, ok := deps.sessions.get(token); ok {
		t.Error("failed disk revocation must still revoke the in-memory session")
	}
	persist.deleteSession = nil
	w = httptest.NewRecorder()
	logoutHandler(deps)(w, authTestRequest(http.MethodPost, "/api/auth/logout", "", token))
	if w.Code != http.StatusNoContent {
		t.Fatalf("retry status = %d, body = %s", w.Code, w.Body.String())
	}
	restarted := newSessionStore(deps.ProfilesDB)
	if err := restarted.restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.get(token); ok {
		t.Error("retry did not revoke the persisted session")
	}
}

func TestAuthMiddlewareRejectsRevocationDuringOpen(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	token, _, err := deps.sessions.create(t.Context(), "reader", false)
	if err != nil {
		t.Fatal(err)
	}
	deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
	opening := make(chan struct{})
	resume := make(chan struct{})
	if err := os.Mkdir(deps.ProfileMgr.profileDir("reader"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps.ProfileMgr.loadProfile = func(ctx context.Context, name string) (*profileDeps, error) {
		close(opening)
		<-resume
		return deps.ProfileMgr.openProfile(ctx, name)
	}
	called := false
	h := authMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, authTestRequest(http.MethodGet, "/api/books", "", token))
	}()
	<-opening
	logout := httptest.NewRecorder()
	logoutHandler(deps)(logout, authTestRequest(http.MethodPost, "/api/auth/logout", "", token))
	close(resume)
	<-done
	if called || w.Code != http.StatusUnauthorized {
		t.Errorf("revoked request reached handler = %v, status = %d", called, w.Code)
	}
}

func TestLoginCannotCrossProfileDeletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps := newAuthTestDependencies(t)
		if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
			t.Fatal(err)
		}
		// Warm-up is unrelated to the credential race and must not start a scan.
		deps.ProfileMgr.loadProfile = func(context.Context, string) (*profileDeps, error) {
			return nil, os.ErrNotExist
		}
		token, _, err := deps.sessions.create(t.Context(), "reader", false)
		if err != nil {
			t.Fatal(err)
		}
		deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
		revoking := make(chan struct{})
		resume := make(chan struct{})
		deps.sessions.persist = &authTestPersistence{
			sessionPersistence: deps.ProfilesDB,
			deleteProfile: func(ctx context.Context, profile string) error {
				close(revoking)
				<-resume
				return deps.ProfilesDB.DeleteSessionsForProfile(ctx, profile)
			},
		}
		deleted := make(chan struct{})
		deleteResponse := httptest.NewRecorder()
		go func() {
			defer close(deleted)
			deleteProfileHandler(deps)(deleteResponse, authTestRequest(http.MethodDelete, "/api/auth/profile", `{"pin":""}`, token))
		}()
		<-revoking
		loggedIn := make(chan struct{})
		loginResponse := httptest.NewRecorder()
		go func() {
			defer close(loggedIn)
			loginHandler(deps)(loginResponse, authTestRequest(http.MethodPost, "/api/auth/login", `{"name":"reader","pin":""}`, ""))
		}()
		synctest.Wait()
		crossedDeletion := false
		select {
		case <-loggedIn:
			crossedDeletion = true
		default:
		}
		close(resume)
		<-deleted
		<-loggedIn
		if crossedDeletion || loginResponse.Code != http.StatusUnauthorized {
			t.Errorf("login crossed deletion = %v, status = %d, body = %s", crossedDeletion, loginResponse.Code, loginResponse.Body.String())
		}
		if deleteResponse.Code != http.StatusNoContent {
			t.Errorf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
		}
	})
}

func authTestCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	res := w.Result()
	defer func() { _ = res.Body.Close() }()
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want one", len(cookies))
	}
	return cookies[0]
}

func TestSessionPersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
		t.Fatal(err)
	}
	transient, transientSession, err := deps.sessions.create(t.Context(), "reader", false)
	if err != nil {
		t.Fatal(err)
	}
	remembered, rememberedSession, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if transient == remembered || len(remembered) != 2*tokenLen || len(transient) != 2*tokenLen {
		t.Fatal("tokens must be distinct, full-length random values")
	}
	if remaining := time.Until(transientSession.expiry); remaining <= sessionDuration-time.Minute || remaining > sessionDuration {
		t.Errorf("transient lifetime = %v", remaining)
	}
	if remaining := time.Until(rememberedSession.expiry); remaining <= sessionRemember-time.Minute || remaining > sessionRemember {
		t.Errorf("remembered lifetime = %v", remaining)
	}
	restarted := newSessionStore(deps.ProfilesDB)
	if err := restarted.restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.get(transient); ok {
		t.Error("transient session survived restart")
	}
	if sess, ok := restarted.get(remembered); !ok || !sess.remember || !sess.verifiedUntil.IsZero() {
		t.Errorf("restored session = %+v, found = %v", sess, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	restarted.deleteAllForProfile(ctx, "reader")
	rows, err := deps.ProfilesDB.LoadSessions(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("profile revocation left persisted sessions: %v, %v", rows, err)
	}
	if token, _, err := restarted.create(ctx, "reader", false); !errors.Is(err, context.Canceled) || token != "" {
		t.Errorf("canceled create = %q, %v", token, err)
	}

	// A save fault is deliberately a non-fatal, memory-only login fallback.
	deps.sessions.persist = &authTestPersistence{
		sessionPersistence: deps.ProfilesDB,
		saveSession: func(context.Context, string, string, time.Time) error {
			return errors.New("injected save failure")
		},
	}
	fallback, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := deps.sessions.get(fallback); !ok {
		t.Error("save failure lost the working in-memory session")
	}
}

func TestSessionPersistenceSharesMutationLock(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
		t.Fatal(err)
	}
	writes := 0
	checkLocks := func() {
		t.Helper()
		writes++
		// Both disk effects must hold the same ordering lock. Inspect it at
		// the I/O boundary rather than waiting on a contended sync.Mutex:
		// mutex waits are not durably blocked under testing/synctest.
		if deps.sessions.writeMu.TryLock() {
			deps.sessions.writeMu.Unlock()
			t.Error("disk mutation escaped the shared ordering lock")
		}
		if deps.sessions.mu.TryLock() {
			deps.sessions.mu.Unlock()
		} else {
			t.Error("disk mutation held the hot-path map lock")
		}
	}
	deps.sessions.persist = &authTestPersistence{
		sessionPersistence: deps.ProfilesDB,
		saveSession: func(ctx context.Context, token, profile string, expiry time.Time) error {
			checkLocks()
			return deps.ProfilesDB.SaveSession(ctx, token, profile, expiry)
		},
		deleteSession: func(ctx context.Context, token string) error {
			checkLocks()
			return deps.ProfilesDB.DeleteSession(ctx, token)
		},
	}
	token, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.sessions.deleteToken(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Errorf("observed disk mutations = %d, want save and delete", writes)
	}
	restarted := newSessionStore(deps.ProfilesDB)
	if err := restarted.restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.get(token); ok {
		t.Error("revoked token survived restart")
	}
}

func TestSessionExpiryAndVerificationBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		ss := newSessionStore(nil)
		ss.data["boundary"] = session{profile: "reader", expiry: now}
		ss.data["expired"] = session{profile: "reader", expiry: now.Add(-time.Nanosecond)}
		ss.data["live"] = session{profile: "reader", expiry: now.Add(time.Hour)}
		// Exact equality remains valid, matching storage's strict expiry cutoff.
		ss.sweep(t.Context())
		if _, ok := ss.get("boundary"); !ok {
			t.Error("session expired at the inclusive cutoff")
		}
		for _, token := range []string{"expired", "missing"} {
			if _, ok := ss.markProfileVerified(token, now.Add(time.Minute)); ok {
				t.Errorf("verification resurrected %q", token)
			}
		}
		time.Sleep(time.Nanosecond)
		if _, ok := ss.markProfileVerified("boundary", now.Add(time.Minute)); ok {
			t.Error("verification accepted a session that expired during the lookup")
		}
		if sess, ok := ss.markProfileVerified("live", now.Add(time.Minute)); !ok || !sess.verifiedUntil.Equal(now.Add(time.Minute)) {
			t.Errorf("live verification = %+v, %v", sess, ok)
		}
	})
}

func TestSessionRestoreFailureDoesNotPublish(t *testing.T) {
	for _, stage := range []string{"prune", "load"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			fault := errors.New("injected restore failure")
			persist := &authTestPersistence{
				pruneSessions: func(context.Context, time.Time) error {
					if stage == "prune" {
						return fault
					}
					return nil
				},
				loadSessions: func(context.Context) ([]storage.PersistedSession, error) {
					return []storage.PersistedSession{{Token: "partial", Profile: "reader", Expiry: time.Now().Add(time.Hour)}}, fault
				},
			}
			ss := newSessionStore(persist)
			if err := ss.restore(t.Context()); !errors.Is(err, fault) {
				t.Fatalf("restore error = %v, want wrapped fault", err)
			}
			if len(ss.data) != 0 {
				t.Fatal("failed restore published partial rows")
			}
		})
	}
}

func TestAuthStatusSessionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cookie     bool
		profile    bool
		cached     bool
		expired    bool
		closeDB    bool
		wantStatus int
		wantAuth   bool
		wantClear  bool
	}{
		{name: "no cookie", wantStatus: 200},
		{name: "missing profile", cookie: true, wantStatus: 200, wantClear: true},
		{name: "live", cookie: true, profile: true, wantStatus: 200, wantAuth: true},
		{name: "expired cached", cookie: true, profile: true, cached: true, expired: true, wantStatus: 200, wantClear: true},
		{name: "cached existence", cookie: true, profile: true, cached: true, closeDB: true, wantStatus: 200, wantAuth: true},
		{name: "database failure", cookie: true, profile: true, closeDB: true, wantStatus: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := newAuthTestDependencies(t)
			if tc.profile {
				if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
					t.Fatal(err)
				}
			}
			token := ""
			if tc.cookie {
				var err error
				token, _, err = deps.sessions.create(t.Context(), "reader", false)
				if err != nil {
					t.Fatal(err)
				}
				if tc.cached {
					deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
				}
				if tc.expired {
					deps.sessions.mu.Lock()
					sess := deps.sessions.data[token]
					sess.expiry = time.Now().Add(-time.Second)
					deps.sessions.data[token] = sess
					deps.sessions.mu.Unlock()
				}
			}
			if tc.closeDB {
				if err := deps.ProfilesDB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			authStatusHandler(deps)(w, authTestRequest(http.MethodGet, "/api/auth/status", "", token))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var status struct {
					Authenticated bool   `json:"authenticated"`
					Profile       string `json:"profile"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
					t.Fatal(err)
				}
				if status.Authenticated != tc.wantAuth || (status.Profile == "reader") != tc.wantAuth {
					t.Errorf("auth status = %+v", status)
				}
			}
			if tc.wantClear {
				if c := authTestCookie(t, w); c.Value != "" || c.MaxAge != -1 {
					t.Errorf("invalid cookie was not cleared: %+v", c)
				}
			} else if len(w.Header().Values("Set-Cookie")) != 0 {
				t.Error("valid/no-cookie/database-failure response changed the cookie")
			}
		})
	}
}

func TestAuthProfileRoutesLifecycle(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps)
	request := func(method, path, body, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authTestRequest(method, path, body, token))
		if w.Code != want {
			t.Fatalf("%s %s: status = %d, body = %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	request(http.MethodPost, "/api/auth/clone", `{"newName":"copy"}`, "", 401)
	request(http.MethodDelete, "/api/auth/profile", `{"pin":""}`, "", 401)
	request(http.MethodPost, "/api/auth/create", `{"name":" Alice ","pin":"1234"}`, "", 201)
	request(http.MethodPost, "/api/auth/create", `{"name":"Alice","pin":"1234"}`, "", 409)
	request(http.MethodPost, "/api/auth/create", `{"name":"badpin","pin":"123"}`, "", 400)
	listed := request(http.MethodGet, "/api/auth/profiles", "", "", 200)
	if got := strings.TrimSpace(listed.Body.String()); got != `[{"name":"Alice","hasPin":true}]` {
		t.Fatalf("public profile list leaked credentials or changed shape: %s", got)
	}
	request(http.MethodPost, "/api/auth/login", `{"name":"Alice","pin":"0000"}`, "", 401)
	login := request(http.MethodPost, "/api/auth/login", `{"name":" Alice ","pin":"1234","remember":true}`, "", 200)
	token := authTestCookie(t, login).Value
	request(http.MethodPost, "/api/auth/clone", `{"newName":"Copy","pin":""}`, token, 201)
	copyRecord, err := deps.ProfilesDB.GetProfileContext(t.Context(), "Copy")
	if err != nil || copyRecord.PinHash != "" {
		t.Fatalf("clone did not use its own PIN policy: %+v, %v", copyRecord, err)
	}
	request(http.MethodDelete, "/api/auth/profile", `{"pin":"0000"}`, token, 401)
	request(http.MethodDelete, "/api/auth/profile", `{"pin":"1234"}`, token, 204)
	request(http.MethodPost, "/api/auth/create", `{"name":"Alice","pin":"4321"}`, "", 201)
	status := request(http.MethodGet, "/api/auth/status", "", token, 200)
	if !strings.Contains(status.Body.String(), `"authenticated":false`) {
		t.Fatal("deleted profile's token authenticated the replacement profile")
	}
	if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "Copy"); err != nil {
		t.Errorf("deleting the source changed the clone: %v", err)
	}
}

func TestCloneRollbackDoesNotPublishCredentials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps := newAuthTestDependencies(t)
		if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
			t.Fatal(err)
		}
		token, _, err := deps.sessions.create(t.Context(), "reader", false)
		if err != nil {
			t.Fatal(err)
		}
		deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
		unlockSource, err := deps.ProfileMgr.lockProfiles(t.Context(), "reader")
		if err != nil {
			t.Fatal(err)
		}
		defer unlockSource()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cloned := make(chan struct{})
		cloneResponse := httptest.NewRecorder()
		go func() {
			defer close(cloned)
			r := authTestRequest(http.MethodPost, "/api/auth/clone", `{"newName":"copy","pin":""}`, token).WithContext(ctx)
			cloneProfileHandler(deps)(cloneResponse, r)
		}()
		synctest.Wait()
		if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "copy"); err != nil {
			cancel()
			<-cloned
			t.Fatalf("clone did not reach provisional registration: %v", err)
		}
		loggedIn := make(chan struct{})
		loginResponse := httptest.NewRecorder()
		go func() {
			defer close(loggedIn)
			loginHandler(deps)(loginResponse, authTestRequest(http.MethodPost, "/api/auth/login", `{"name":"copy","pin":""}`, ""))
		}()
		synctest.Wait()
		cancel()
		<-cloned
		<-loggedIn
		if cloneResponse.Code != http.StatusInternalServerError || loginResponse.Code != http.StatusUnauthorized {
			t.Errorf("canceled clone/login statuses = %d/%d", cloneResponse.Code, loginResponse.Code)
		}
		if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "copy"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("canceled clone registration survived rollback: %v", err)
		}
		if _, err := os.Stat(filepath.Join(deps.LibraryRoot, "copy")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("canceled clone created a destination: %v", err)
		}
		if _, ok := deps.sessions.get(token); !ok {
			t.Error("clone rollback revoked the source session")
		}
	})
}

func TestProfileOperationRechecksSessionAfterWait(t *testing.T) {
	for _, operation := range []string{"clone", "delete"} {
		for _, revocation := range []string{"logout", "expiry"} {
			t.Run(operation+"/"+revocation, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					deps := newAuthTestDependencies(t)
					if err := deps.ProfilesDB.CreateProfileContext(t.Context(), "reader", ""); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(filepath.Join(deps.LibraryRoot, "reader"), 0o755); err != nil {
						t.Fatal(err)
					}
					token, _, err := deps.sessions.create(t.Context(), "reader", false)
					if err != nil {
						t.Fatal(err)
					}
					deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
					unlock, locked := lockProfileOperation(deps, httptest.NewRecorder(), authTestRequest(http.MethodGet, "/", "", ""))
					if !locked {
						t.Fatal("failed to hold operation gate")
					}
					handler, method, target, body := cloneProfileHandler(deps), http.MethodPost, "/api/auth/clone", `{"newName":"copy","pin":""}`
					if operation == "delete" {
						handler, method, target, body = deleteProfileHandler(deps), http.MethodDelete, "/api/auth/profile", `{"pin":""}`
					}
					done := make(chan struct{})
					w := httptest.NewRecorder()
					go func() {
						defer close(done)
						handler(w, authTestRequest(method, target, body, token))
					}()
					synctest.Wait()
					if revocation == "logout" {
						if err := deps.sessions.deleteToken(t.Context(), token); err != nil {
							t.Error(err)
						}
					} else {
						deps.sessions.mu.Lock()
						sess := deps.sessions.data[token]
						sess.expiry = time.Now().Add(-time.Second)
						deps.sessions.data[token] = sess
						deps.sessions.mu.Unlock()
					}
					unlock()
					<-done
					if w.Code != http.StatusUnauthorized {
						t.Errorf("revoked operation status = %d, body = %s", w.Code, w.Body.String())
					}
					if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "reader"); err != nil {
						t.Errorf("revoked request deleted the source: %v", err)
					}
					if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "copy"); !errors.Is(err, storage.ErrNotFound) {
						t.Errorf("revoked request created a clone: %v", err)
					}
				})
			})
		}
	}
}

func TestProfileOperationWaitCancellation(t *testing.T) {
	for _, operation := range []string{"login", "create", "clone", "delete"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				deps := newAuthTestDependencies(t)
				token, _, err := deps.sessions.create(t.Context(), "reader", false)
				if err != nil {
					t.Fatal(err)
				}
				deps.sessions.markProfileVerified(token, time.Now().Add(time.Hour))
				unlock, locked := lockProfileOperation(deps, httptest.NewRecorder(), authTestRequest(http.MethodGet, "/", "", ""))
				if !locked {
					t.Fatal("failed to hold operation gate")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				handler, method, target, body := loginHandler(deps), http.MethodPost, "/api/auth/login", `{"name":"reader","pin":""}`
				switch operation {
				case "create":
					handler, target = createProfileHandler(deps), "/api/auth/create"
				case "clone":
					handler, target, body = cloneProfileHandler(deps), "/api/auth/clone", `{"newName":"copy","pin":""}`
				case "delete":
					handler, method, target, body = deleteProfileHandler(deps), http.MethodDelete, "/api/auth/profile", `{"pin":""}`
				}
				done := make(chan struct{})
				w := httptest.NewRecorder()
				go func() {
					defer close(done)
					handler(w, authTestRequest(method, target, body, token).WithContext(ctx))
				}()
				synctest.Wait()
				cancel()
				synctest.Wait()
				returned := false
				select {
				case <-done:
					returned = true
				default:
				}
				unlock()
				<-done
				if !returned || w.Code != http.StatusInternalServerError {
					t.Errorf("canceled wait returned while locked = %v, status = %d", returned, w.Code)
				}
				if len(w.Header().Values("Set-Cookie")) != 0 {
					t.Error("canceled operation changed the cookie")
				}
				if profiles, err := deps.ProfilesDB.ListProfilesContext(t.Context()); err != nil || len(profiles) != 0 {
					t.Errorf("canceled operation changed registration: %+v, %v", profiles, err)
				}
			})
		})
	}
}

func TestDeleteProfileCompletesAfterDisconnect(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	created := httptest.NewRecorder()
	createProfileHandler(deps)(created, authTestRequest(http.MethodPost, "/api/auth/create", `{"name":"reader","pin":""}`, ""))
	if created.Code != http.StatusCreated {
		t.Fatal(created.Body.String())
	}
	token, _, err := deps.sessions.create(t.Context(), "reader", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	deps.sessions.persist = &authTestPersistence{
		sessionPersistence: deps.ProfilesDB,
		deleteProfile: func(deleteCtx context.Context, profile string) error {
			cancel()
			return deps.ProfilesDB.DeleteSessionsForProfile(deleteCtx, profile)
		},
	}
	w := httptest.NewRecorder()
	deleteProfileHandler(deps)(w, authTestRequest(http.MethodDelete, "/api/auth/profile", `{"pin":""}`, token).WithContext(ctx))
	if w.Code != http.StatusNoContent || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("delete status = %d, request error = %v", w.Code, ctx.Err())
	}
	if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "reader"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("profile row survived deletion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deps.LibraryRoot, "reader")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("profile directory survived deletion: %v", err)
	}
	if c := authTestCookie(t, w); c.Value != "" || c.MaxAge != -1 {
		t.Errorf("delete did not clear the session cookie: %+v", c)
	}
}

func TestCreateProfilePreservesOrphanedDirectory(t *testing.T) {
	t.Parallel()
	deps := newAuthTestDependencies(t)
	dir := filepath.Join(deps.LibraryRoot, "orphan")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("unowned data"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	createProfileHandler(deps)(w, authTestRequest(http.MethodPost, "/api/auth/create", `{"name":"orphan","pin":""}`, ""))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "dir_exists") {
		t.Fatalf("orphan conflict status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, err := deps.ProfilesDB.GetProfileContext(t.Context(), "orphan"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("conflicting registration survived rollback: %v", err)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "unowned data" {
		t.Errorf("creation changed an unowned directory: %q, %v", got, err)
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

// Profile names become directory names verbatim. Windows resolves DOS device
// names in every directory, and on Windows 11 os.Mkdir("…\NUL") returns nil
// while creating nothing — so the profile reported success and then failed
// every login, because openProfile stats the device and sees a non-directory.
// Rejected on all platforms: a library folder is meant to move between machines.
func TestValidateProfileNameRejectsWindowsReservedNames(t *testing.T) {
	t.Parallel()

	reserved := []string{"NUL", "nul", "Con", "CON", "aux", "PRN", "com1", "COM9", "lpt1", "LPT9"}
	for _, name := range reserved {
		if validateProfileName(name) {
			t.Errorf("reserved device name %q must be rejected", name)
		}
	}

	// Names that merely contain a reserved word are still fine.
	for _, name := range []string{"Conrad", "Nulla", "com10", "my aux", "PRNter"} {
		if !validateProfileName(name) {
			t.Errorf("legitimate name %q must be accepted", name)
		}
	}
}
