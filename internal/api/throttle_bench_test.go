package api

import (
	"strconv"
	"testing"
)

// These benchmarks time only in-memory throttle operations, not HTTP, bcrypt,
// SQLite, or parallel clients. Setup is outside b.Loop; each admitted cycle
// includes releaseAttempt, as loginHandler does. Rejection fixtures must remain
// within their real-time lock/window, so use short runs (for example 200ms).
func BenchmarkLoginThrottle(b *testing.B) {
	key := loginThrottleKey{profile: "Alice", client: "192.0.2.1"}
	b.Run("Success", func(b *testing.B) {
		throttle := newLoginThrottle()
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait != 0 {
				b.Fatalf("unexpected admission wait: %v", wait)
			}
			throttle.recordSuccess(key)
			throttle.releaseAttempt(key)
		}
	})
	b.Run("FailureThenSuccess", func(b *testing.B) {
		throttle := newLoginThrottle()
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait != 0 {
				b.Fatalf("unexpected failure admission wait: %v", wait)
			}
			throttle.recordFailure(key)
			throttle.releaseAttempt(key)
			if wait := throttle.beginAttempt(key); wait != 0 {
				b.Fatalf("unexpected success admission wait: %v", wait)
			}
			throttle.recordSuccess(key)
			throttle.releaseAttempt(key)
		}
	})
	b.Run("InternalError", func(b *testing.B) {
		throttle := newLoginThrottle()
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait != 0 {
				b.Fatalf("unexpected admission wait: %v", wait)
			}
			throttle.releaseAttempt(key)
		}
	})
	b.Run("InFlight", func(b *testing.B) {
		throttle := newLoginThrottle()
		if wait := throttle.beginAttempt(key); wait != 0 {
			b.Fatal(wait)
		}
		defer throttle.releaseAttempt(key)
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait != loginConcurrentRetry {
				b.Fatalf("unexpected concurrent rejection: %v", wait)
			}
		}
	})
	b.Run("Locked", func(b *testing.B) {
		throttle := newLoginThrottle()
		for range loginMaxFailures {
			if wait := throttle.beginAttempt(key); wait != 0 {
				b.Fatal(wait)
			}
			throttle.recordFailure(key)
			throttle.releaseAttempt(key)
		}
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait <= 0 || wait > loginLockout {
				b.Fatalf("unexpected lockout rejection: %v", wait)
			}
		}
	})
	b.Run("Full", func(b *testing.B) {
		throttle := newLoginThrottle()
		for i := range loginMaxEntries {
			other := loginThrottleKey{profile: "Profile", client: "client" + strconv.Itoa(i)}
			if wait := throttle.beginAttempt(other); wait != 0 {
				b.Fatal(wait)
			}
			throttle.recordFailure(other)
			throttle.releaseAttempt(other)
		}
		b.ReportAllocs()
		for b.Loop() {
			if wait := throttle.beginAttempt(key); wait != loginWindow {
				b.Fatalf("unexpected capacity rejection: %v", wait)
			}
		}
	})
}
