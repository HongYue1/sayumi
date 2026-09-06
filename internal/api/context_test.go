package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProfileDepsContextIsolation(t *testing.T) {
	t.Parallel()

	type markerKey struct{}
	parent, stop := context.WithTimeout(t.Context(), time.Hour)
	defer stop()
	ctx, cancel := context.WithCancelCause(context.WithValue(parent, markerKey{}, "kept"))
	defer cancel(nil)
	base := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/books", nil)
	alice := &profileDeps{LibPath: "Alice", refs: 1}
	bob := &profileDeps{LibPath: "Bob", refs: 1}
	a := withProfileDeps(base, alice)
	b := withProfileDeps(base, bob)
	rebound := withProfileDeps(a, bob)

	if a == base || b == base || rebound == a {
		t.Fatal("binding a profile mutated the original request")
	}
	if profileDepsFromCtx(base) != nil || profileDepsFromCtx(a) != alice ||
		profileDepsFromCtx(b) != bob || profileDepsFromCtx(rebound) != bob {
		t.Fatal("profile binding leaked between requests")
	}
	deadline, _ := parent.Deadline()
	for _, r := range []*http.Request{a, b, rebound} {
		if got, ok := r.Context().Deadline(); !ok || !got.Equal(deadline) {
			t.Errorf("deadline = %v, %v; want %v", got, ok, deadline)
		}
		if r.Context().Value(markerKey{}) != "kept" || r.Method != base.Method || r.URL != base.URL {
			t.Error("binding dropped parent context or request metadata")
		}
	}
	cause := errors.New("request canceled")
	cancel(cause)
	for _, r := range []*http.Request{a, b, rebound} {
		if !errors.Is(r.Context().Err(), context.Canceled) || !errors.Is(context.Cause(r.Context()), cause) {
			t.Error("binding lost cancellation or its cause")
		}
	}
	// Auth middleware owns the acquired reference; context helpers only borrow it.
	if alice.refs != 1 || bob.refs != 1 {
		t.Fatal("context binding changed profile reference ownership")
	}
}

func TestProfileDepsContextMissing(t *testing.T) {
	t.Parallel()

	base := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	type otherContextKey int
	pd := &profileDeps{}
	tests := []struct {
		name string
		req  *http.Request
	}{
		{name: "nil request"},
		{name: "absent", req: base},
		{name: "typed nil", req: withProfileDeps(base, nil)},
		{name: "wrong value type", req: base.WithContext(context.WithValue(base.Context(), profileDepsKey, "not a profile"))},
		{name: "different key type", req: base.WithContext(context.WithValue(base.Context(), otherContextKey(profileDepsKey), pd))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := profileDepsFromCtx(tc.req); got != nil {
				t.Fatalf("missing profile = %p, want nil", got)
			}
		})
	}
}
