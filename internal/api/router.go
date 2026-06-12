package api

import (
	"context"
	"net/http"
	"time"

	"sayumi/internal/api/middleware"
	"sayumi/internal/fonts"
	"sayumi/internal/storage"
)

type Dependencies struct {
	ProfilesDB  *storage.ProfilesDB
	ProfileMgr  *ProfileManager
	LibraryRoot string
	Fonts       *fonts.Scanner
	sessions    *sessionStore
	throttle    *loginThrottle
}

// NewDependencies constructs a Dependencies value with all internal state
// (including the session store) properly initialized.
func NewDependencies(profilesDB *storage.ProfilesDB, profileMgr *ProfileManager, libraryRoot string, fontScanner *fonts.Scanner) *Dependencies {
	return &Dependencies{
		ProfilesDB:  profilesDB,
		ProfileMgr:  profileMgr,
		LibraryRoot: libraryRoot,
		Fonts:       fontScanner,
		sessions:    newSessionStore(),
		throttle:    newLoginThrottle(),
	}
}

// StartBackgroundTasks runs periodic maintenance until ctx is canceled.
// Call it in a dedicated goroutine after the server starts.
func (d *Dependencies) StartBackgroundTasks(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.sessions.sweep()
			d.throttle.sweep()
		case <-ctx.Done():
			return
		}
	}
}

func applyAuth(deps *Dependencies, h http.HandlerFunc) http.Handler {
	return authMiddleware(deps)(h)
}

func RegisterRoutes(mux *http.ServeMux, deps *Dependencies) {
	mux.HandleFunc("GET /api/health", healthHandler)
	mux.HandleFunc("GET /api/auth/status", authStatusHandler(deps))
	mux.HandleFunc("GET /api/auth/profiles", listProfilesHandler(deps))
	mux.HandleFunc("POST /api/auth/login", loginHandler(deps))
	mux.HandleFunc("POST /api/auth/logout", logoutHandler(deps))
	mux.HandleFunc("POST /api/auth/create", createProfileHandler(deps))

	// clone and delete use inline auth — see handler comments for why
	// applyAuth cannot be used here.
	mux.HandleFunc("POST /api/auth/clone", cloneProfileHandler(deps))
	mux.HandleFunc("DELETE /api/auth/profile", deleteProfileHandler(deps))

	mux.Handle("GET /api/books", applyAuth(deps, listBooksHandler(deps)))
	mux.Handle("POST /api/books/upload", applyAuth(deps, uploadBookHandler(deps)))
	mux.Handle("POST /api/library/rescan", applyAuth(deps, rescanLibraryHandler(deps)))
	mux.Handle("GET /api/books/{id}", applyAuth(deps, getBookHandler(deps)))
	mux.Handle("DELETE /api/books/{id}", applyAuth(deps, deleteBookHandler(deps)))
	mux.Handle("GET /api/books/{id}/toc", applyAuth(deps, getTocHandler(deps)))
	mux.Handle("GET /api/books/{id}/cover", applyAuth(deps, getCoverHandler(deps)))
	mux.Handle("GET /api/books/{id}/chapters/{index}", applyAuth(deps, getChapterHandler(deps)))
	// Resource routes intentionally bypass applyAuth: the iframe cannot send
	// session cookies cross-origin, so authentication is done via the book's
	// file-hash token embedded in resource URLs by the chapter renderer.
	mux.HandleFunc("GET /api/books/{id}/resources/{path...}", getResourceHandler(deps))
	mux.HandleFunc("OPTIONS /api/books/{id}/resources/{path...}", getResourceHandler(deps))

	mux.Handle("GET /api/settings", applyAuth(deps, getSettingsHandler(deps)))
	mux.Handle("PUT /api/settings", applyAuth(deps, putSettingsHandler(deps)))

	mux.Handle("GET /api/fonts", applyAuth(deps, listFontsHandler(deps)))
	mux.Handle("POST /api/fonts/rescan", applyAuth(deps, rescanFontsHandler(deps)))

	mux.Handle("GET /api/flairs", applyAuth(deps, listFlairsHandler(deps)))
	mux.Handle("POST /api/flairs", applyAuth(deps, createFlairHandler(deps)))
	mux.Handle("DELETE /api/flairs/{id}", applyAuth(deps, deleteFlairHandler(deps)))
	mux.Handle("PUT /api/books/{id}/flair", applyAuth(deps, setBookFlairHandler(deps)))

	mux.Handle("GET /api/books/{id}/progress", applyAuth(deps, getProgressHandler(deps)))
	mux.Handle("PUT /api/books/{id}/progress", applyAuth(deps, putProgressHandler(deps)))
	mux.Handle("POST /api/books/{id}/progress/beacon", applyAuth(deps, beaconProgressHandler(deps)))

	mux.Handle("GET /api/books/{id}/search", applyAuth(deps, searchHandler(deps)))

	mux.Handle("GET /api/books/{id}/bookmarks", applyAuth(deps, listBookmarksHandler(deps)))
	mux.Handle("POST /api/books/{id}/bookmarks", applyAuth(deps, createBookmarkHandler(deps)))
	mux.Handle("PATCH /api/books/{id}/bookmarks/{bid}", applyAuth(deps, updateBookmarkHandler(deps)))
	mux.Handle("DELETE /api/books/{id}/bookmarks/{bid}", applyAuth(deps, deleteBookmarkHandler(deps)))
}

func NewHandler(deps *Dependencies, fontHandler http.Handler, staticHandler http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/fonts/", fontHandler)
	RegisterRoutes(mux, deps)

	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /api/\n"))
	})

	mux.Handle("/", staticHandler)
	return middleware.Gzip(mux)
}
