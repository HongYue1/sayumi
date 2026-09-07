package api

import (
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const resourceTokenParam = "token"

type resourceAccess struct {
	fileHash string
	filePath string
	pd       *profileDeps
}

// WONTFIX: the resource token equals the book's file hash. This is intentional:
// an iframe loading sub-resources cannot use the session cookie across origins,
// so the hash acts as a content-scoped bearer, not an independently expiring
// token. The hash is only revealed to authenticated clients inside chapter responses.
func resourceTokenForBook(fileHash string) string {
	return fileHash
}

func validResourceToken(fileHash, token string) bool {
	// Use constant-time comparison to avoid timing side-channels on the token.
	return fileHash != "" && subtle.ConstantTimeCompare([]byte(token), []byte(fileHash)) == 1
}

// resourceETag derives a validator unique to (book, resource). The bare file
// hash is shared by every resource in a book, so a client revalidating one
// asset could be told 304 for a different one; folding the resource path in
// fixes that. The path is hashed rather than concatenated so the tag stays a
// fixed-width, quote-free token — a raw request path may contain a double
// quote, which would otherwise break the ETag header grammar.
func resourceETag(fileHash, resourcePath string) string {
	// Inline FNV-1a over the path so this allocates nothing on the per-resource
	// request path: hash/fnv's constructor escapes a hasher to the heap and
	// h.Write([]byte(resourcePath)) copies the string, both avoidable here.
	const (
		fnvOffset64 = 14695981039346656037
		fnvPrime64  = 1099511628211
	)
	var h uint64 = fnvOffset64
	for i := range len(resourcePath) {
		h ^= uint64(resourcePath[i])
		h *= fnvPrime64
	}
	return `"` + fileHash + ":" + strconv.FormatUint(h, 16) + `"`
}

func getResourceHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookID := r.PathValue("id")
		resourcePath, ok := cleanResourcePath(r.PathValue("path"))
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}

		setResourceCORSHeaders(w.Header(), resourcePath)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// A full-page illustration or an embedded font over a slow link can
		// outlast the server WriteTimeout, which net/http arms at header-read
		// time. Without this the body is cut off mid-stream and the client sees a
		// truncated image under a 200.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear resource write deadline unsupported", "err", err)
		}

		access, ok := authorizeResourceRequest(deps, w, r, bookID)
		if !ok {
			return
		}
		if access.pd == nil {
			writeError(w, http.StatusInternalServerError, "server_error", "resource store not available")
			return
		}
		defer access.pd.release()
		defer access.pd.bookReplaceMu.RUnlock()

		resourceReader, err := access.pd.Store.OpenResource(access.filePath, resourcePath)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		defer func() {
			if err := resourceReader.Close(); err != nil {
				slog.Error("close resource failed", "book", bookID, "resource", resourcePath, "err", err)
			}
		}()

		contentType := resourceReader.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// Block content types that execute under the app origin when opened
		// directly. Chapter HTML is served via processChapterHTML which
		// sanitizes and rewrites the content; raw EPUB assets that are
		// HTML, XHTML, or script must never be served as-is.
		if isExecutableContentType(contentType) {
			writeError(w, http.StatusForbidden, "forbidden", "resource type not served directly")
			return
		}

		// Preconditions apply only after existence and MIME checks succeed. A
		// matching tag (including *) must not turn a missing or forbidden asset
		// into a 304, nor attach a successful-asset validator to an error body.
		etag := ""
		if access.fileHash != "" {
			etag = resourceETag(access.fileHash, resourcePath)
			w.Header().Set("ETag", etag)
		}
		// "private": this is profile-scoped book content behind a session cookie
		// or a per-book token, so a shared cache must not keep a copy and hand it
		// to another client. The ?token=<fileHash> in the URL still busts the
		// year-long browser cache whenever the book is replaced.
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// A book's own SVG is served as image/svg+xml so <img> and CSS can render
		// it, but an SVG opened as a top-level document executes its scripts under
		// the app origin. A response CSP does not affect subresource rendering and
		// sandboxes exactly that navigation case.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		if ifNoneMatchMatches(r, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", contentType)
		// Size is -1 from OpenResource (zip declared sizes are untrusted), so we
		// omit Content-Length and let the transport chunk the body.
		if resourceReader.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(resourceReader.Size, 10))
		}

		if _, err := io.Copy(w, resourceReader); err != nil {
			slog.Error("copy resource failed", "book", bookID, "resource", resourcePath, "err", err)
		}
	}
}

func setResourceCORSHeaders(header http.Header, resourcePath string) {
	switch strings.ToLower(path.Ext(resourcePath)) {
	case ".woff", ".woff2", ".ttf", ".otf", ".eot":
		header.Set("Access-Control-Allow-Origin", "*")
		header.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		header.Set("Access-Control-Allow-Headers", "Content-Type")
	}
}

// authorizeResourceRequest returns a snapshot with its profile reference and
// bookReplaceMu read lock held. On success the caller releases both, after
// closing any resource reader. On failure it retains neither.
func authorizeResourceRequest(
	deps *Dependencies,
	w http.ResponseWriter,
	r *http.Request,
	bookID string,
) (resourceAccess, bool) {
	token := r.URL.Query().Get(resourceTokenParam)

	sess, hasSession, err := validateSession(deps, w, r)
	if err != nil {
		slog.Error("session verification for resource failed", "book", bookID, "err", err)
		writeError(w, http.StatusInternalServerError, "db_error", "failed to load profile")
		return resourceAccess{}, false
	}

	var pd *profileDeps
	if hasSession {
		pd, err = deps.ProfileMgr.Get(r.Context(), sess.profile)
		if err != nil {
			slog.Error("open profile for resource failed", "profile", sess.profile, "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "profile_error", "failed to open profile")
			return resourceAccess{}, false
		}
	} else {
		if token == "" {
			writeUnauthenticated(w)
			return resourceAccess{}, false
		}

		// Token path: only search already-open profiles. Scanning all profiles for
		// an unauthenticated request would allow any bookID to trigger full library
		// scans (ScanNow) across every profile — a DoS amplifier. If the profile was
		// evicted after the chapter was served, the client gets 404 and can reload.
		var found bool
		pd, found = deps.ProfileMgr.FindBook(bookID)
		if !found {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return resourceAccess{}, false
		}
	}

	// Validate the token and snapshot the file under the same generation gate.
	// Checking before locking lets a replacement authorize new bytes with an old
	// token; re-reading the snapshot afterwards does not revalidate that token.
	// Retain the gate through streaming and reader cleanup so replacement cannot
	// mix bytes and ETags, and deletion cannot strand a still-pinned ZIP on Windows.
	pd.bookReplaceMu.RLock()
	book, ok := pd.Books.Get(bookID)
	if !ok {
		pd.bookReplaceMu.RUnlock()
		pd.release()
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return resourceAccess{}, false
	}

	if !hasSession && !validResourceToken(book.FileHash, token) {
		pd.bookReplaceMu.RUnlock()
		pd.release()
		writeUnauthenticated(w)
		return resourceAccess{}, false
	}

	return resourceAccess{
		fileHash: book.FileHash,
		filePath: book.FilePath,
		pd:       pd,
	}, true
}

// isExecutableContentType reports whether ct would be executed by a browser
// when opened under the app origin. CSS, images, fonts, and binary formats
// are safe to serve; HTML, XHTML, XML, and script types are not.
func isExecutableContentType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i != -1 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "text/html", "application/xhtml+xml",
		"text/javascript", "application/javascript", "application/x-javascript",
		"text/xml", "application/xml":
		return true
	}
	return false
}

func cleanResourcePath(rawPath string) (string, bool) {
	if rawPath == "" || strings.Contains(rawPath, `\`) {
		return "", false
	}

	cleaned := path.Clean(rawPath)
	if cleaned == "." || cleaned == "" || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}

	return cleaned, true
}
