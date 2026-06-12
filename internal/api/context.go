package api

import (
	"context"
	"net/http"
)

type contextKey int

const profileDepsKey contextKey = iota

func withProfileDeps(r *http.Request, pd *profileDeps) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), profileDepsKey, pd))
}

func profileDepsFromCtx(r *http.Request) *profileDeps {
	if r == nil {
		return nil
	}
	pd, _ := r.Context().Value(profileDepsKey).(*profileDeps)
	return pd
}
