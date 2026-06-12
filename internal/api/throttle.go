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
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
	loginLockout     = time.Minute
)

type throttleEntry struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

type loginThrottle struct {
	mu      sync.Mutex
	entries map[string]*throttleEntry
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{entries: make(map[string]*throttleEntry)}
}

// retryAfter reports how long key must wait before another login attempt is
// allowed. A zero duration means the caller may proceed.
func (t *loginThrottle) retryAfter(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return 0
	}
	if d := time.Until(e.lockedUntil); d > 0 {
		return d
	}
	return 0
}

// recordFailure registers one failed attempt for key and locks it once the
// failure count reaches loginMaxFailures within loginWindow.
func (t *loginThrottle) recordFailure(key string) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		e = &throttleEntry{windowStart: now}
		t.entries[key] = e
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
func (t *loginThrottle) recordSuccess(key string) {
	t.mu.Lock()
	delete(t.entries, key)
	t.mu.Unlock()
}

// sweep drops entries that are no longer locked and have been idle for at
// least one window, bounding memory use. Call it periodically.
func (t *loginThrottle) sweep() {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, e := range t.entries {
		if now.Before(e.lockedUntil) {
			continue
		}
		if now.Sub(e.lastSeen) > loginWindow {
			delete(t.entries, key)
		}
	}
}

// throttleKey combines the profile name with the client IP so a single abusive
// client cannot lock a legitimate user out of their own profile, and so
// attempts from different hosts are counted independently. RemoteAddr is the
// direct peer; behind a reverse proxy this is the proxy's address, which is
// acceptable for the intended LAN/localhost deployment.
func throttleKey(name string, r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return name + "\x00" + host
}
