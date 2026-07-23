package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoginThrottleSerializesKey(t *testing.T) {
	t.Parallel()

	throttle := newLoginThrottle()
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("first beginAttempt wait = %v, want zero", wait)
	}
	if wait := throttle.beginAttempt(key); wait != loginConcurrentRetry {
		t.Fatalf("concurrent beginAttempt wait = %v, want %v", wait, loginConcurrentRetry)
	}

	throttle.releaseAttempt(key)
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("beginAttempt after release wait = %v, want zero", wait)
	}
	throttle.releaseAttempt(key)
}

func TestLoginThrottleLockout(t *testing.T) {
	t.Parallel()

	throttle := newLoginThrottle()
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	for range loginMaxFailures {
		if wait := throttle.beginAttempt(key); wait != 0 {
			t.Fatalf("beginAttempt wait = %v before threshold", wait)
		}
		throttle.recordFailure(key)
		throttle.releaseAttempt(key)
	}
	if wait := throttle.beginAttempt(key); wait <= 0 || wait > loginLockout {
		t.Fatalf("lockout wait = %v, want within (0, %v]", wait, loginLockout)
	}
}

func TestLoginThrottleSuccessClearsAccounting(t *testing.T) {
	t.Parallel()

	throttle := newLoginThrottle()
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("beginAttempt wait = %v, want zero", wait)
	}
	throttle.recordFailure(key)
	throttle.releaseAttempt(key)
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("second beginAttempt wait = %v, want zero", wait)
	}
	throttle.recordSuccess(key)
	throttle.releaseAttempt(key)

	throttle.mu.Lock()
	_, entryExists := throttle.entries[key]
	_, clientExists := throttle.clientEntries[key.client]
	throttle.mu.Unlock()
	if entryExists || clientExists {
		t.Fatalf("success left entry=%v client count=%v", entryExists, clientExists)
	}
}

func TestLoginThrottleBoundsClientEntries(t *testing.T) {
	t.Parallel()

	throttle := newLoginThrottle()
	const client = "192.0.2.1"
	for i := range loginMaxProfilesPerClient {
		key := loginThrottleKey{profile: "Profile" + strconv.Itoa(i), client: client}
		if wait := throttle.beginAttempt(key); wait != 0 {
			t.Fatalf("beginAttempt(%d) wait = %v, want zero", i, wait)
		}
		throttle.recordFailure(key)
		throttle.releaseAttempt(key)
	}

	existing := loginThrottleKey{profile: "Profile0", client: client}
	if wait := throttle.beginAttempt(existing); wait != 0 {
		t.Fatalf("existing key wait = %v, want zero", wait)
	}
	throttle.releaseAttempt(existing)

	newKey := loginThrottleKey{profile: "Overflow", client: client}
	if wait := throttle.beginAttempt(newKey); wait != loginWindow {
		t.Fatalf("overflow key wait = %v, want %v", wait, loginWindow)
	}
	otherClient := loginThrottleKey{profile: "Overflow", client: "192.0.2.2"}
	if wait := throttle.beginAttempt(otherClient); wait != 0 {
		t.Fatalf("other client wait = %v, want zero", wait)
	}
	throttle.releaseAttempt(otherClient)
}

func TestLoginThrottleSweepReleasesCapacity(t *testing.T) {
	t.Parallel()

	throttle := newLoginThrottle()
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("beginAttempt wait = %v, want zero", wait)
	}
	throttle.recordFailure(key)
	throttle.releaseAttempt(key)

	throttle.mu.Lock()
	throttle.entries[key].lastSeen = time.Now().Add(-loginWindow - time.Second)
	throttle.mu.Unlock()
	throttle.sweep()

	throttle.mu.Lock()
	_, entryExists := throttle.entries[key]
	_, clientExists := throttle.clientEntries[key.client]
	throttle.mu.Unlock()
	if entryExists || clientExists {
		t.Fatalf("sweep left entry=%v client count=%v", entryExists, clientExists)
	}
}

func TestThrottleKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		wantClient string
	}{
		{name: "IPv4", remoteAddr: "192.0.2.1:1234", wantClient: "192.0.2.1"},
		{name: "IPv6", remoteAddr: "[2001:db8::1]:1234", wantClient: "2001:db8::1"},
		{name: "missing port", remoteAddr: "local-peer", wantClient: "local-peer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			req.RemoteAddr = tc.remoteAddr
			got := throttleKey("Alice", req)
			if got.profile != "Alice" || got.client != tc.wantClient {
				t.Fatalf("throttleKey() = %+v, want profile Alice and client %q", got, tc.wantClient)
			}
		})
	}
}

func TestLoginRejectsInvalidNameBeforeThrottle(t *testing.T) {
	t.Parallel()

	deps := &Dependencies{throttle: newLoginThrottle()}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"name":"`+strings.Repeat("x", 33)+`","pin":"0000"}`),
	)
	recorder := httptest.NewRecorder()
	loginHandler(deps)(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"invalid_name"`) {
		t.Fatalf("body = %s, want invalid_name error", recorder.Body.String())
	}
	deps.throttle.mu.Lock()
	entryCount := len(deps.throttle.entries)
	deps.throttle.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("invalid login name created %d throttle entries", entryCount)
	}
}
