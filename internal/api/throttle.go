package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Login brute-force throttle. Sayumi can be exposed on a LAN (the -network
// flag binds 0.0.0.0), so a per-(profile, client) failed-attempt limiter
// raises the cost of guessing a profile PIN. bcrypt already makes each guess
// slow; this caps the sustained rate on top of that. State is in-memory only,
// which is sufficient for a single-binary self-hosted server.
const (
	loginMaxFailures          = 5
	loginMaxEntries           = 4096
	loginMaxProfilesPerClient = 64
	loginWindow               = 15 * time.Minute
	loginLockout              = time.Minute
	loginConcurrentRetry      = time.Second
)

type throttleEntry struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
	inFlight    bool
}

type loginThrottleKey struct {
	profile string
	client  string
}

type loginThrottle struct {
	mu            sync.Mutex
	entries       map[loginThrottleKey]*throttleEntry
	clientEntries map[string]int
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{
		entries:       make(map[loginThrottleKey]*throttleEntry),
		clientEntries: make(map[string]int),
	}
}

// beginAttempt atomically admits one login attempt for key. Only one bcrypt
// comparison per (profile, client) may be in flight, so a parallel burst cannot
// pass the failure threshold before any result has been recorded. A zero
// duration means the caller owns the reservation and must call releaseAttempt.
func (t *loginThrottle) beginAttempt(key loginThrottleKey) time.Duration {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[key]
	if ok {
		if d := e.lockedUntil.Sub(now); d > 0 {
			return d
		}
		if e.inFlight {
			return loginConcurrentRetry
		}
	} else {
		if len(t.entries) >= loginMaxEntries ||
			t.clientEntries[key.client] >= loginMaxProfilesPerClient {
			t.pruneLocked(now)
			if len(t.entries) >= loginMaxEntries ||
				t.clientEntries[key.client] >= loginMaxProfilesPerClient {
				return loginWindow
			}
		}
		e = &throttleEntry{windowStart: now}
		t.entries[key] = e
		t.clientEntries[key.client]++
	}

	e.inFlight = true
	e.lastSeen = now
	return 0
}

// releaseAttempt gives up an admission reservation without recording an
// authentication result. It is safe to call after recordFailure or
// recordSuccess, and removes zero-failure entries left by internal errors.
func (t *loginThrottle) releaseAttempt(key loginThrottleKey) {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[key]
	if !ok {
		return
	}
	e.inFlight = false
	if e.failures == 0 && e.lockedUntil.IsZero() {
		t.deleteEntryLocked(key)
	}
}

// recordFailure registers one failed attempt for key and locks it once the
// failure count reaches loginMaxFailures within loginWindow.
func (t *loginThrottle) recordFailure(key loginThrottleKey) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return
	}
	// Decay: start a fresh counting window once the previous one has elapsed.
	if now.Sub(e.windowStart) > loginWindow {
		e.failures = 0
		e.windowStart = now
	}
	e.failures++
	e.lastSeen = now
	if e.failures >= loginMaxFailures {
		e.lockedUntil = now.Add(loginLockout)
	}
}

// recordSuccess clears any throttle state for key after a successful login.
func (t *loginThrottle) recordSuccess(key loginThrottleKey) {
	t.mu.Lock()
	t.deleteEntryLocked(key)
	t.mu.Unlock()
}

// sweep drops entries that are no longer locked and have been idle for at
// least one window, bounding memory use. Call it periodically.
func (t *loginThrottle) sweep() {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
}

func (t *loginThrottle) pruneLocked(now time.Time) {
	for key, e := range t.entries {
		if e.inFlight || now.Before(e.lockedUntil) {
			continue
		}
		if now.Sub(e.lastSeen) > loginWindow {
			t.deleteEntryLocked(key)
		}
	}
}

func (t *loginThrottle) deleteEntryLocked(key loginThrottleKey) {
	if _, ok := t.entries[key]; !ok {
		return
	}
	delete(t.entries, key)
	if t.clientEntries[key.client] <= 1 {
		delete(t.clientEntries, key.client)
		return
	}
	t.clientEntries[key.client]--
}

// throttleKey combines the profile name with the client IP so a single abusive
// client cannot lock a legitimate user out of their own profile, and so
// attempts from different hosts are counted independently. RemoteAddr is the
// direct peer; behind a reverse proxy this is the proxy's address, which is
// acceptable for the intended LAN/localhost deployment.
func throttleKey(name string, r *http.Request) loginThrottleKey {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return loginThrottleKey{profile: name, client: host}
}
