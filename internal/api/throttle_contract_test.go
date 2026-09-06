package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestLoginThrottleSuccessKeepsReservation(t *testing.T) {
	t.Parallel()
	for _, previousLockout := range []bool{false, true} {
		t.Run(strconv.FormatBool(previousLockout), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				throttle := newLoginThrottle()
				key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
				if previousLockout {
					for range loginMaxFailures {
						failThrottleAttempt(t, throttle, key)
					}
					time.Sleep(loginLockout)
				}
				if wait := throttle.beginAttempt(key); wait != 0 {
					t.Fatalf("begin wait = %v, want zero", wait)
				}
				throttle.recordSuccess(key)

				// loginHandler records success before writing the response and
				// defers release. Another request must not replace its entry in
				// that interval: the old defer would release the new reservation.
				if wait := throttle.beginAttempt(key); wait != loginConcurrentRetry {
					t.Fatalf("begin before successful owner releases = %v, want %v", wait, loginConcurrentRetry)
				}
				time.Sleep(2 * loginWindow)
				throttle.sweep()
				if wait := throttle.beginAttempt(key); wait != loginConcurrentRetry {
					t.Fatalf("sweep lost successful owner's reservation: wait = %v", wait)
				}
				checkThrottleAccounting(t, throttle, 1)
				throttle.releaseAttempt(key)
				checkThrottleAccounting(t, throttle, 0)

				// Both the failure history and any expired lockout were cleared.
				for range loginMaxFailures {
					failThrottleAttempt(t, throttle, key)
				}
				if wait := throttle.beginAttempt(key); wait != loginLockout {
					t.Fatalf("new failure history wait = %v, want %v", wait, loginLockout)
				}
			})
		})
	}
}

func TestLoginThrottleFailureWindowBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		age  time.Duration
		want time.Duration
	}{
		{name: "before", age: loginWindow - time.Nanosecond, want: loginLockout},
		{name: "exact", age: loginWindow},
		{name: "after", age: loginWindow + time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				throttle := newLoginThrottle()
				key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
				for range loginMaxFailures - 1 {
					failThrottleAttempt(t, throttle, key)
				}
				time.Sleep(tc.age)
				failThrottleAttempt(t, throttle, key)
				if wait := throttle.beginAttempt(key); wait != tc.want {
					t.Fatalf("begin after boundary failure = %v, want %v", wait, tc.want)
				}
				if tc.want == 0 {
					throttle.releaseAttempt(key)
				}
				checkThrottleAccounting(t, throttle, 1)
			})
		})
	}
}

func TestLoginThrottleLockoutExpiryDoesNotEraseFailures(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		throttle := newLoginThrottle()
		key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
		for range loginMaxFailures {
			failThrottleAttempt(t, throttle, key)
		}
		time.Sleep(loginLockout - time.Nanosecond)
		if wait := throttle.beginAttempt(key); wait != time.Nanosecond {
			t.Fatalf("before expiry = %v, want 1ns", wait)
		}
		time.Sleep(time.Nanosecond)
		// A denied probe does not extend the lock. An admitted internal error
		// also must not clear the accumulated failures in the current window.
		if wait := throttle.beginAttempt(key); wait != 0 {
			t.Fatalf("at expiry = %v, want zero", wait)
		}
		throttle.releaseAttempt(key)
		failThrottleAttempt(t, throttle, key)
		if wait := throttle.beginAttempt(key); wait != loginLockout {
			t.Fatalf("next failure = %v, want renewed lockout", wait)
		}
	})
}

func TestLoginThrottleSweepBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		age  time.Duration
		want int
	}{
		{name: "before", age: loginWindow - time.Nanosecond, want: 1},
		{name: "exact", age: loginWindow},
		{name: "after", age: loginWindow + time.Nanosecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				throttle := newLoginThrottle()
				failThrottleAttempt(t, throttle, loginThrottleKey{profile: "Alice", client: "192.0.2.1"})
				time.Sleep(tc.age)
				throttle.sweep()
				checkThrottleAccounting(t, throttle, tc.want)
				throttle.sweep()
				checkThrottleAccounting(t, throttle, tc.want)
			})
		})
	}
}

func TestLoginThrottleCapacityBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		count int
	}{
		{name: "client", count: loginMaxProfilesPerClient},
		{name: "global", count: loginMaxEntries},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				throttle := newLoginThrottle()
				for i := range tc.count {
					key := loginThrottleKey{profile: "Profile" + strconv.Itoa(i), client: "client"}
					if tc.name == "global" {
						key.client = "client" + strconv.Itoa(i)
					}
					failThrottleAttempt(t, throttle, key)
				}
				key := loginThrottleKey{profile: "Overflow", client: "client"}
				if wait := throttle.beginAttempt(key); wait != loginWindow {
					t.Fatalf("full table wait = %v, want %v", wait, loginWindow)
				}
				checkThrottleAccounting(t, throttle, tc.count)
				time.Sleep(loginWindow - time.Nanosecond)
				if wait := throttle.beginAttempt(key); wait != loginWindow {
					t.Fatalf("before reclamation boundary = %v, want full-table rejection", wait)
				}
				time.Sleep(time.Nanosecond)
				if wait := throttle.beginAttempt(key); wait != 0 {
					t.Fatalf("at reclamation boundary = %v, want zero", wait)
				}
				checkThrottleAccounting(t, throttle, 1)
				throttle.releaseAttempt(key)
				checkThrottleAccounting(t, throttle, 0)
			})
		})
	}
}

func TestLoginThrottleSweepKeepsInFlightAndLocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		throttle := newLoginThrottle()
		active := loginThrottleKey{profile: "Active", client: "192.0.2.1"}
		locked := loginThrottleKey{profile: "Locked", client: active.client}
		if wait := throttle.beginAttempt(active); wait != 0 {
			t.Fatalf("begin = %v, want zero", wait)
		}
		time.Sleep(2 * loginWindow)
		for range loginMaxFailures {
			failThrottleAttempt(t, throttle, locked)
		}
		throttle.sweep()
		checkThrottleAccounting(t, throttle, 2)
		if wait := throttle.beginAttempt(active); wait != loginConcurrentRetry {
			t.Fatalf("active reservation = %v, want concurrent rejection", wait)
		}
		if wait := throttle.beginAttempt(locked); wait != loginLockout {
			t.Fatalf("locked reservation = %v, want lockout", wait)
		}
		throttle.releaseAttempt(active)
		checkThrottleAccounting(t, throttle, 1)
		time.Sleep(loginWindow)
		throttle.sweep()
		checkThrottleAccounting(t, throttle, 0)
	})
}

func TestLoginThrottleParallelAdmission(t *testing.T) {
	t.Parallel()
	throttle := newLoginThrottle()
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	const contenders = 32
	for range loginMaxFailures {
		waits := make([]time.Duration, contenders)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range contenders {
			wg.Go(func() {
				<-start
				waits[i] = throttle.beginAttempt(key)
			})
		}
		wg.Go(func() {
			<-start
			throttle.sweep()
		})
		close(start)
		wg.Wait()
		var admitted int
		for _, wait := range waits {
			if wait == 0 {
				admitted++
			} else if wait != loginConcurrentRetry {
				t.Fatalf("burst wait = %v, want concurrent rejection", wait)
			}
		}
		if admitted != 1 {
			t.Fatalf("burst admitted %d attempts, want one", admitted)
		}
		throttle.recordFailure(key)
		throttle.releaseAttempt(key)
	}
	if wait := throttle.beginAttempt(key); wait <= 0 || wait > loginLockout {
		t.Fatalf("parallel failures did not lock out: %v", wait)
	}
	checkThrottleAccounting(t, throttle, 1)
}

func TestLoginThrottleMissingCompletionIsNoop(t *testing.T) {
	t.Parallel()
	throttle := newLoginThrottle()
	missing := loginThrottleKey{profile: "Missing", client: "192.0.2.1"}
	other := loginThrottleKey{profile: "Other", client: missing.client}
	if wait := throttle.beginAttempt(other); wait != 0 {
		t.Fatalf("begin = %v, want zero", wait)
	}
	throttle.recordFailure(missing)
	throttle.recordSuccess(missing)
	throttle.releaseAttempt(missing)
	checkThrottleAccounting(t, throttle, 1)
	if wait := throttle.beginAttempt(other); wait != loginConcurrentRetry {
		t.Fatalf("missing completion released another profile: %v", wait)
	}
	throttle.releaseAttempt(other)
	checkThrottleAccounting(t, throttle, 0)
}

func TestThrottleKeyIgnoresForwardedHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		remote string
		client string
	}{
		{name: "IPv4 port", remote: "192.0.2.1:1234", client: "192.0.2.1"},
		{name: "other port", remote: "192.0.2.1:9876", client: "192.0.2.1"},
		{name: "IPv6 zone", remote: "[fe80::1%eth0]:1234", client: "fe80::1%eth0"},
		{name: "bare IPv6", remote: "2001:db8::1", client: "2001:db8::1"},
		{name: "unknown peer", remote: "local-peer", client: "local-peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-For", "198.51.100.9")
			r.Header.Set("X-Real-IP", "198.51.100.10")
			r.Header.Set("Forwarded", "for=198.51.100.11")
			if got := throttleKey("Alice", r); got != (loginThrottleKey{profile: "Alice", client: tc.client}) {
				t.Fatalf("key = %+v, want Alice and direct peer %q", got, tc.client)
			}
		})
	}
}

func TestLoginThrottleHTTPRejection(t *testing.T) {
	t.Parallel()
	for _, locked := range []bool{false, true} {
		t.Run(strconv.FormatBool(locked), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				throttle := newLoginThrottle()
				r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"name":" Alice ","pin":"0000"}`))
				key := throttleKey("Alice", r)
				wantRetry := int(loginConcurrentRetry/time.Second) + 1
				if locked {
					for range loginMaxFailures {
						failThrottleAttempt(t, throttle, key)
					}
					wantRetry = int(loginLockout/time.Second) + 1
				} else if wait := throttle.beginAttempt(key); wait != 0 {
					t.Fatalf("begin = %v, want zero", wait)
				}
				w := httptest.NewRecorder()
				// A rejection must return before any database or PIN work.
				loginHandler(&Dependencies{throttle: throttle})(w, r)
				if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != strconv.Itoa(wantRetry) {
					t.Fatalf("status/retry = %d/%q, want 429/%d", w.Code, w.Header().Get("Retry-After"), wantRetry)
				}
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != "rate_limited" {
					t.Fatalf("rejection body = %s, err = %v", w.Body.String(), err)
				}
				if !locked {
					if wait := throttle.beginAttempt(key); wait != loginConcurrentRetry {
						t.Fatalf("rejected handler released active owner: %v", wait)
					}
					throttle.releaseAttempt(key)
				}
			})
		})
	}
}

func failThrottleAttempt(t *testing.T, throttle *loginThrottle, key loginThrottleKey) {
	t.Helper()
	if wait := throttle.beginAttempt(key); wait != 0 {
		t.Fatalf("begin(%+v) = %v, want zero", key, wait)
	}
	throttle.recordFailure(key)
	throttle.releaseAttempt(key)
}

func checkThrottleAccounting(t *testing.T, throttle *loginThrottle, want int) {
	t.Helper()
	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	if len(throttle.entries) != want || len(throttle.entries) > loginMaxEntries {
		t.Fatalf("entry count = %d, want %d within global cap", len(throttle.entries), want)
	}
	counts := make(map[string]int)
	for key := range throttle.entries {
		counts[key.client]++
	}
	if len(counts) != len(throttle.clientEntries) {
		t.Fatalf("client index has %d keys, want %d", len(throttle.clientEntries), len(counts))
	}
	for client, count := range counts {
		if got := throttle.clientEntries[client]; got != count || got > loginMaxProfilesPerClient {
			t.Fatalf("client %q count = %d, want %d within client cap", client, got, count)
		}
	}
}

func FuzzLoginThrottleLifecycle(f *testing.F) {
	f.Add([]byte{0, 2, 0, 3, 0, 1, 3})
	f.Add([]byte{0, 1, 3, 0x10, 0x11, 0x13, 4, 5})
	f.Add([]byte{0, 4, 5, 2, 5, 3, 5})
	f.Fuzz(func(t *testing.T, actions []byte) {
		if len(actions) > 256 {
			t.Skip()
		}
		synctest.Test(t, func(t *testing.T) {
			throttle := newLoginThrottle()
			keys := []loginThrottleKey{
				{profile: "Alice", client: "192.0.2.1"},
				{profile: "Bob", client: "192.0.2.1"},
				{profile: "Alice", client: "192.0.2.2"},
				{profile: "Bob", client: "192.0.2.2"},
			}
			var owned, resolved [4]bool
			for _, action := range actions {
				i := int(action>>4) % len(keys)
				key := keys[i]
				switch action & 7 {
				case 0:
					wait := throttle.beginAttempt(key)
					if wait < 0 || wait > loginWindow || (owned[i] && wait == 0) {
						t.Fatalf("invalid admission wait %v (owned=%v)", wait, owned[i])
					}
					if wait == 0 {
						owned[i] = true
					}
				case 1, 2:
					if owned[i] && !resolved[i] {
						if action&7 == 1 {
							throttle.recordFailure(key)
						} else {
							throttle.recordSuccess(key)
						}
						resolved[i] = true
					}
				case 3:
					if owned[i] {
						throttle.releaseAttempt(key)
						owned[i], resolved[i] = false, false
					}
				case 4:
					time.Sleep([]time.Duration{time.Nanosecond, loginLockout, loginWindow}[int(action>>3)%3])
				case 5:
					throttle.sweep()
				}
				checkThrottleAccounting(t, throttle, len(throttle.entries))
				for j, k := range keys {
					e := throttle.entries[k]
					if got := e != nil && e.inFlight; got != owned[j] {
						t.Fatalf("key %+v reservation = %v, want %v", k, got, owned[j])
					}
					if e != nil && e.failures == 0 && !e.inFlight {
						t.Fatalf("unowned zero-failure entry retained: %+v", k)
					}
				}
			}
		})
	})
}
