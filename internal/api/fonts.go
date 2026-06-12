package api

import (
	"net/http"

	"sayumi/internal/fonts"
)

// fontsResponse is the JSON shape returned by the font-discovery endpoints.
// Only user-supplied families are reported; the embedded catalogue is a static
// constant on the client side.
type fontsResponse struct {
	User []fonts.Family `json:"user"`
}

func listFontsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fontsResponse{User: userFamilies(deps)})
	}
}

func rescanFontsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var families []fonts.Family
		if deps.Fonts != nil {
			families = deps.Fonts.Rescan()
		} else {
			families = []fonts.Family{}
		}
		writeJSON(w, http.StatusOK, fontsResponse{User: families})
	}
}

func userFamilies(deps *Dependencies) []fonts.Family {
	if deps.Fonts == nil {
		return []fonts.Family{}
	}
	return deps.Fonts.Families()
}
