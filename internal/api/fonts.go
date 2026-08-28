package api

import (
	"net/http"

	"sayumi/internal/fonts"
)

// fontsResponse is the JSON shape returned by the font-discovery endpoints.
// Only user-supplied families are reported as families; the embedded catalog
// stays a static constant on the client side.
//
// What the server does report for the embedded set is measurements, keyed by
// the flat filename each face is served under. Those let the client size-
// normalize embedded and user families against one set of numbers, instead of
// carrying a hand-copied table that goes stale whenever a face is updated.
type fontsResponse struct {
	User      []fonts.Family           `json:"user"`
	Embedded  map[string]fonts.Metrics `json:"embedded"`
	UserToken string                   `json:"userToken"`
}

func newFontsResponse(deps *Dependencies, families []fonts.Family) fontsResponse {
	return fontsResponse{
		User:      families,
		Embedded:  fonts.EmbeddedFaceMetrics(),
		UserToken: deps.fontToken,
	}
}

func listFontsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, newFontsResponse(deps, userFamilies(deps)))
	}
}

func rescanFontsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		var families []fonts.Family
		if deps.Fonts != nil {
			families = deps.Fonts.Rescan()
		} else {
			families = []fonts.Family{}
		}
		writeJSON(w, http.StatusOK, newFontsResponse(deps, families))
	}
}

func userFamilies(deps *Dependencies) []fonts.Family {
	if deps.Fonts == nil {
		return []fonts.Family{}
	}
	return deps.Fonts.Families()
}
