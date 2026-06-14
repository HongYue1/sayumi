package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"golang.org/x/crypto/bcrypt"

	"sayumi/internal/storage"
)

const (
	sessionCookie   = "sayumi_session"
	sessionRemember = 30 * 24 * time.Hour
	sessionDuration = 24 * time.Hour // server-side expiry for non-remember sessions
	tokenLen        = 32

	// sessionProfileCheckTTL bounds how long validateSession trusts a cached
	// profile-existence result before re-checking ProfilesDB. See
	// session.verifiedUntil.
	sessionProfileCheckTTL = 60 * time.Second
)

type session struct {
	profile  string
	expiry   time.Time
	remember bool
	// verifiedUntil is the wall-clock time until which sess.profile is known to
	// exist in ProfilesDB. validateSession re-confirms existence only once this
	// passes. ProfilesDB runs at maxOpenConns=1, so an unconditional per-request
	// check serializes all authenticated traffic (a library page fires one
	// request per cover) behind a single connection; the cache collapses that
	// burst into one lookup. Out-of-band profile deletion stays visible within
	// sessionProfileCheckTTL (in-band deletes call deleteAllForProfile).
	verifiedUntil time.Time
}

// sessionStore holds active sessions in memory. The map is unbounded between
// sweep cycles (every 5 minutes via StartBackgroundTasks). For a self-hosted
// single-user server the accumulation between sweeps is negligible.
type sessionStore struct {
	mu   sync.Mutex
	data map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{data: make(map[string]session)}
}

func (ss *sessionStore) create(profile string, remember bool) (string, session, error) {
	tokenBytes := make([]byte, tokenLen)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", session{}, fmt.Errorf("generate session token: %w", err)
	}

	token := hex.EncodeToString(tokenBytes)
	sess := session{profile: profile, remember: remember}
	if remember {
		sess.expiry = time.Now().Add(sessionRemember)
	} else {
		sess.expiry = time.Now().Add(sessionDuration)
	}

	ss.mu.Lock()
	ss.data[token] = sess
	ss.mu.Unlock()

	return token, sess, nil
}

func (ss *sessionStore) get(token string) (session, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	sess, ok := ss.data[token]
	if !ok {
		return session{}, false
	}
	if time.Now().After(sess.expiry) {
		delete(ss.data, token)
		return session{}, false
	}
	return sess, true
}

func (ss *sessionStore) deleteToken(token string) {
	ss.mu.Lock()
	delete(ss.data, token)
	ss.mu.Unlock()
}

// markProfileVerified records that token's profile was just confirmed to exist,
// suppressing the per-request existence check until "until". It is a no-op if
// the token is gone (logged out or swept) so it never resurrects a dead session.
func (ss *sessionStore) markProfileVerified(token string, until time.Time) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	sess, ok := ss.data[token]
	if !ok {
		return
	}
	sess.verifiedUntil = until
	ss.data[token] = sess
}

func (ss *sessionStore) deleteAllForProfile(profile string) {
	ss.mu.Lock()
	for token, sess := range ss.data {
		if sess.profile == profile {
			delete(ss.data, token)
		}
	}
	ss.mu.Unlock()
}

// sweep removes all sessions whose expiry has passed. Called periodically
// by Dependencies.StartBackgroundTasks.
func (ss *sessionStore) sweep() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for token, sess := range ss.data {
		if now.After(sess.expiry) {
			delete(ss.data, token)
		}
	}
}

func sessionFromRequest(r *http.Request, store *sessionStore) (string, session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", session{}, false
	}
	sess, ok := store.get(cookie.Value)
	return cookie.Value, sess, ok
}

func setCookie(w http.ResponseWriter, r *http.Request, token string, sess session) {
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is set only when the server itself terminated TLS (r.TLS != nil).
		// When running behind a TLS-terminating reverse proxy (nginx, Caddy, etc.)
		// the proxy-to-server leg is plain HTTP, so r.TLS is nil and the cookie
		// will be sent without Secure. In that deployment the operator should
		// either configure the proxy to set Secure via a Set-Cookie rewrite rule,
		// or run sayumi with TLS directly. For the intended single-user localhost
		// use-case this is acceptable: the cookie never crosses the network.
		Secure: r.TLS != nil,
	}
	// Only persist the cookie across browser restarts for "remember me" sessions.
	// Non-remember sessions use a session cookie (no MaxAge/Expires) so the
	// browser discards it on exit; the server-side store expires it after 24 h.
	if sess.remember && !sess.expiry.IsZero() {
		cookie.Expires = sess.expiry
		cookie.MaxAge = int(time.Until(sess.expiry).Seconds())
	}
	http.SetCookie(w, cookie)
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func clearInvalidSessionCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(sessionCookie); err == nil {
		clearCookie(w, r)
	}
}

func writeUnauthenticated(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "unauthenticated", "not logged in")
}

func sessionProfileExists(ctx context.Context, deps *Dependencies, sess session) error {
	_, err := deps.ProfilesDB.GetProfileContext(ctx, sess.profile)
	return err
}

func validateSession(deps *Dependencies, w http.ResponseWriter, r *http.Request) (session, bool, error) {
	token, sess, ok := sessionFromRequest(r, deps.sessions)
	if !ok {
		clearInvalidSessionCookie(w, r)
		return session{}, false, nil
	}

	// Fast path: existence was confirmed within the TTL, so skip the DB hit.
	// This is the hot path — every authenticated request (including each cover
	// and chapter asset) lands here, and ProfilesDB serializes at maxOpenConns=1.
	if time.Now().Before(sess.verifiedUntil) {
		return sess, true, nil
	}

	if err := sessionProfileExists(r.Context(), deps, sess); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			deps.sessions.deleteToken(token)
			clearCookie(w, r)
			return session{}, false, nil
		}
		return session{}, false, fmt.Errorf("verify session profile %q: %w", sess.profile, err)
	}

	deps.sessions.markProfileVerified(token, time.Now().Add(sessionProfileCheckTTL))
	return sess, true, nil
}

func requireAuthenticatedSession(
	deps *Dependencies,
	w http.ResponseWriter,
	r *http.Request,
	action string,
) (session, bool) {
	sess, ok, err := validateSession(deps, w, r)
	if err != nil {
		slog.Error("session validation failed", "action", action, "err", err)
		writeError(w, http.StatusInternalServerError, "db_error", "failed to load profile")
		return session{}, false
	}
	if !ok {
		writeUnauthenticated(w)
		return session{}, false
	}
	return sess, true
}

func isUniqueConstraint(err error) bool {
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}

	switch sqliteErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
		return true
	default:
		return false
	}
}

func authMiddleware(deps *Dependencies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, ok := requireAuthenticatedSession(deps, w, r, "auth middleware")
			if !ok {
				return
			}

			pd, err := deps.ProfileMgr.Get(r.Context(), sess.profile)
			if err != nil {
				slog.Error("failed to open profile", "profile", sess.profile, "err", err)
				writeError(w, http.StatusInternalServerError, "profile_error", "failed to open profile")
				return
			}
			defer pd.release()

			pd.ProfileName = sess.profile
			next.ServeHTTP(w, withProfileDeps(r, pd))
		})
	}
}

var validProfileName = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9 _\-]{0,30}[a-zA-Z0-9])?$`,
)

func validateProfileName(name string) bool {
	if strings.ContainsAny(name, `/\:*?"<>|`) || strings.Contains(name, "..") {
		return false
	}
	return validProfileName.MatchString(name)
}

const (
	pinMinLen = 4
	pinMaxLen = 12
)

var pinPattern = regexp.MustCompile(`^[0-9]+$`)

// validatePIN reports whether pin is acceptable. An empty PIN is allowed and
// denotes a PIN-less (open) profile. A non-empty PIN must be 4–12 digits. The
// returned string is a user-facing reason when ok is false.
func validatePIN(pin string) (msg string, ok bool) {
	if pin == "" {
		return "", true
	}
	if len(pin) < pinMinLen || len(pin) > pinMaxLen {
		return "PIN must be 4–12 digits", false
	}
	if !pinPattern.MatchString(pin) {
		return "PIN must contain digits only", false
	}
	return "", true
}

// hashPIN returns the bcrypt hash of pin, or "" for an empty (PIN-less) profile.
func hashPIN(pin string) (string, error) {
	if pin == "" {
		return "", nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// verifyPIN checks pin against a stored hash. A stored empty hash denotes an
// open profile and always succeeds (the supplied pin is ignored). A non-empty
// hash requires a matching pin. ok reports a successful match; a non-nil error
// indicates an internal fault (e.g. a malformed hash), not a wrong PIN.
func verifyPIN(hash, pin string) (ok bool, err error) {
	if hash == "" {
		return true, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func authStatusHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok, err := validateSession(deps, w, r)
		if err != nil {
			slog.Error("auth status check failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load profile")
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": false,
				"profile":       "",
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": true,
			"profile":       sess.profile,
		})
	}
}

func listProfilesHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profiles, err := deps.ProfilesDB.ListProfilesContext(r.Context())
		if err != nil {
			slog.Error("list profiles failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to list profiles")
			return
		}

		type item struct {
			Name   string `json:"name"`
			HasPin bool   `json:"hasPin"`
		}

		out := make([]item, 0, len(profiles))
		for _, profile := range profiles {
			out = append(out, item{Name: profile.Name, HasPin: profile.PinHash != ""})
		}

		writeJSON(w, http.StatusOK, out)
	}
}

// dummyPINHash is a valid bcrypt hash computed once on first use. The login
// handler runs a throwaway CompareHashAndPassword against it on the
// profile-not-found path so a non-existent profile costs roughly the same
// wall-clock time as an existing PIN-protected one. Without this, response
// latency leaks whether a profile name exists (username enumeration). The
// comparison result is intentionally discarded.
var dummyPINHash = sync.OnceValue(func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("sayumi timing equalizer"), bcrypt.DefaultCost)
	if err != nil {
		return nil
	}
	return h
})

func loginHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Pin      string `json:"pin"`
			Remember bool   `json:"remember"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}

		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" {
			writeError(w, http.StatusBadRequest, "invalid", "name is required")
			return
		}

		key := throttleKey(body.Name, r)
		if wait := deps.throttle.retryAfter(key); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited",
				"too many failed attempts; please wait and try again")
			return
		}

		profile, err := deps.ProfilesDB.GetProfileContext(r.Context(), body.Name)
		if errors.Is(err, storage.ErrNotFound) {
			// Equalize timing with the existing-profile path below (which runs
			// bcrypt) so response latency can't be used to enumerate valid
			// profile names. The result is intentionally discarded.
			if h := dummyPINHash(); h != nil {
				_ = bcrypt.CompareHashAndPassword(h, []byte(body.Pin))
			}
			deps.throttle.recordFailure(key)
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid name or PIN")
			return
		}
		if err != nil {
			slog.Error("login failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "authentication failed")
			return
		}

		ok, err := verifyPIN(profile.PinHash, body.Pin)
		if err != nil {
			// A non-mismatch bcrypt error (malformed hash, cost out of range, etc.)
			// is an internal fault, not a wrong-PIN signal.
			slog.Error("pin comparison failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "authentication failed")
			return
		}
		if !ok {
			deps.throttle.recordFailure(key)
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid name or PIN")
			return
		}

		token, sess, err := deps.sessions.create(body.Name, body.Remember)
		if err != nil {
			slog.Error("session creation failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to create session")
			return
		}

		deps.throttle.recordSuccess(key)
		setCookie(w, r, token, sess)

		// Warm the profile in the background so the first authenticated request
		// (the library page load) doesn't pay the cold open cost: DB open +
		// library scan + book-cache build. The warm-up must outlive this handler,
		// so it runs on a goroutine with a context detached from the request
		// (WithoutCancel) but bounded by a timeout so a stuck scan can't leak the
		// goroutine. ProfileMgr.Get caches the opened profile and returns a ref we
		// must release; errors are non-fatal because the real request will
		// re-attempt the open and surface any genuine failure.
		warmCtx, cancelWarm := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
		go func() {
			defer cancelWarm()
			pd, err := deps.ProfileMgr.Get(warmCtx, body.Name)
			if err != nil {
				slog.Debug("profile warm-up failed", "profile", body.Name, "err", err)
				return
			}
			pd.release()
		}()

		writeJSON(w, http.StatusOK, map[string]string{"profile": body.Name})
	}
}

func logoutHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			deps.sessions.deleteToken(cookie.Value)
		}
		clearCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}

func createProfileHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
			Pin  string `json:"pin"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}

		body.Name = strings.TrimSpace(body.Name)
		if !validateProfileName(body.Name) {
			writeError(w, http.StatusBadRequest, "invalid_name",
				"profile name must be 1–32 characters: letters, digits, spaces, dashes, underscores")
			return
		}
		if msg, ok := validatePIN(body.Pin); !ok {
			writeError(w, http.StatusBadRequest, "invalid_pin", msg)
			return
		}

		hash, err := hashPIN(body.Pin)
		if err != nil {
			slog.Error("pin hash failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to hash PIN")
			return
		}

		if err := deps.ProfilesDB.CreateProfileContext(r.Context(), body.Name, hash); err != nil {
			if isUniqueConstraint(err) {
				writeError(w, http.StatusConflict, "name_taken",
					"a profile with that name already exists")
				return
			}
			slog.Error("create profile failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to create profile")
			return
		}

		// os.Mkdir (not MkdirAll) ensures we do not silently adopt an orphaned
		// directory that already exists on disk from a previous failed creation.
		// If the directory is already there, return a conflict so the operator
		// can investigate rather than inheriting stray files.
		if err := os.Mkdir(deps.ProfileMgr.profileDir(body.Name), 0o755); err != nil {
			if errors.Is(err, os.ErrExist) {
				if rollbackErr := deps.ProfilesDB.DeleteProfileContext(r.Context(), body.Name); rollbackErr != nil {
					slog.Error("rollback profile after dir conflict", "profile", body.Name, "err", rollbackErr)
				}
				writeError(w, http.StatusConflict, "dir_exists",
					"a profile directory with that name already exists on disk")
				return
			}
			if rollbackErr := deps.ProfilesDB.DeleteProfileContext(r.Context(), body.Name); rollbackErr != nil {
				slog.Error("rollback profile after dir failure", "profile", body.Name, "err", rollbackErr)
			}
			slog.Error("create profile dir failed", "profile", body.Name, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to create profile")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})
	}
}

// cloneProfileHandler intentionally uses inline auth rather than the applyAuth
// wrapper. applyAuth opens the source profile (acquires a ref), but
// CloneProfile calls lockProfiles which waits for all refs to reach zero before
// proceeding — creating a deadlock if the middleware ref is still held.
func cloneProfileHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := requireAuthenticatedSession(deps, w, r, "clone profile")
		if !ok {
			return
		}

		var body struct {
			NewName string `json:"newName"`
			Pin     string `json:"pin"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}

		body.NewName = strings.TrimSpace(body.NewName)
		if !validateProfileName(body.NewName) {
			writeError(w, http.StatusBadRequest, "invalid_name",
				"profile name must be 1–32 characters: letters, digits, spaces, dashes, underscores")
			return
		}
		if msg, ok := validatePIN(body.Pin); !ok {
			writeError(w, http.StatusBadRequest, "invalid_pin", msg)
			return
		}
		if body.NewName == sess.profile {
			writeError(w, http.StatusBadRequest, "invalid_name",
				"new name must differ from the current profile name")
			return
		}

		hash, err := hashPIN(body.Pin)
		if err != nil {
			slog.Error("pin hash failed for clone", "profile", body.NewName, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to hash PIN")
			return
		}

		if err := deps.ProfilesDB.CreateProfileContext(r.Context(), body.NewName, hash); err != nil {
			if isUniqueConstraint(err) {
				writeError(w, http.StatusConflict, "name_taken",
					"a profile with that name already exists")
				return
			}
			slog.Error("create cloned profile record failed", "profile", body.NewName, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to register profile")
			return
		}

		if err := deps.ProfileMgr.CloneProfile(r.Context(), sess.profile, body.NewName); err != nil {
			if rollbackErr := deps.ProfilesDB.DeleteProfileContext(r.Context(), body.NewName); rollbackErr != nil {
				slog.Error("rollback cloned profile record", "profile", body.NewName, "err", rollbackErr)
			}
			if removeErr := os.RemoveAll(deps.ProfileMgr.profileDir(body.NewName)); removeErr != nil {
				slog.Error("remove cloned profile dir", "profile", body.NewName, "err", removeErr)
			}
			slog.Error("clone profile failed", "src", sess.profile, "dst", body.NewName, "err", err)
			writeError(w, http.StatusInternalServerError, "clone_error", "failed to clone profile")
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"name": body.NewName})
	}
}

// deleteProfileHandler intentionally uses inline auth rather than the applyAuth
// wrapper. applyAuth opens the profile (acquires a ref), but deleteProfileHandler
// calls lockProfiles which waits for all refs to reach zero — deadlock if the
// middleware ref is still held.
func deleteProfileHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := requireAuthenticatedSession(deps, w, r, "delete profile")
		if !ok {
			return
		}

		var body struct {
			Pin string `json:"pin"`
		}
		if !decodeJSONBody(w, r, &body) {
			return
		}

		profile, err := deps.ProfilesDB.GetProfileContext(r.Context(), sess.profile)
		if errors.Is(err, storage.ErrNotFound) {
			deps.sessions.deleteAllForProfile(sess.profile)
			clearCookie(w, r)
			writeError(w, http.StatusNotFound, "not_found", "profile not found")
			return
		}
		if err != nil {
			slog.Error("load profile for deletion failed", "profile", sess.profile, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to verify profile")
			return
		}

		ok, err = verifyPIN(profile.PinHash, body.Pin)
		if err != nil {
			slog.Error("pin comparison failed", "profile", sess.profile, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "authentication failed")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "incorrect PIN")
			return
		}

		deps.sessions.deleteAllForProfile(sess.profile)
		unlockProfile := deps.ProfileMgr.lockProfiles(r.Context(), sess.profile)
		defer unlockProfile()

		if err := deps.ProfilesDB.DeleteProfileContext(r.Context(), sess.profile); err != nil {
			slog.Error("delete profile record failed", "profile", sess.profile, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete profile record")
			return
		}

		if err := os.RemoveAll(deps.ProfileMgr.profileDir(sess.profile)); err != nil {
			slog.Error("remove profile dir failed", "profile", sess.profile, "err", err)
		}

		clearCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	}
}
