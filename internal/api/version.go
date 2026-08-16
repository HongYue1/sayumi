package api

import "net/http"

// BuildInfo is the build metadata cmd/sayumi receives from the linker via
// -ldflags "-X main.version=… -X main.buildDate=…".
//
// It is handed to this package rather than read back from
// runtime/debug.BuildInfo because release.sh builds with -buildvcs=false on
// purpose: a vcs stamp changes with the git state and would break the
// reproducible artifacts. These two strings are therefore the only description
// of the running binary that exists at runtime.
type BuildInfo struct {
	Version   string
	BuildDate string
}

// Fallbacks mirroring cmd/sayumi's own defaults, so a Dependencies assembled
// without build metadata (tests, or `go run` outside the build scripts) still
// names itself instead of answering with empty strings.
const (
	unsetVersion   = "dev"
	unsetBuildDate = "unknown"
)

type versionResponse struct {
	Version   string `json:"version"`
	BuildDate string `json:"buildDate"`
}

// versionHandler reports which build is serving this library, for the About
// sheet. Cache-Control is private/no-cache rather than no-store: the answer is
// fixed for the life of the process, but it must not outlive a restart onto a
// newer binary under a tab that never reloads.
func versionHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		build := deps.Build
		if build.Version == "" {
			build.Version = unsetVersion
		}
		if build.BuildDate == "" {
			build.BuildDate = unsetBuildDate
		}

		w.Header().Set("Cache-Control", "private, no-cache")
		writeJSON(w, http.StatusOK, versionResponse(build))
	}
}
