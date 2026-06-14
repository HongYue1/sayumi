package api

import (
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const resourceTokenParam = "token"

type resourceAccess struct {
	fileHash string
	filePath string
	pd       *profileDeps
}

// WONTFIX: the resource token equals the book's file hash. This is intentional:
// an iframe loading sub-resources cannot use the session cookie across origins,
// so the hash acts as a short-lived bearer. The hash is only revealed to
// authenticated clients inside chapter responses.
func resourceTokenForBook(fileHash string) string {
	return fileHash
}

func validResourceToken(fileHash, token string) bool {
	// Use constant-time comparison to avoid timing side-channels on the token.
	return fileHash != "" && subtle.ConstantTimeCompare([]byte(token), []byte(fileHash)) == 1
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

		access, ok := authorizeResourceRequest(deps, w, r, bookID)
		if !ok {
			return
		}
		if access.pd == nil {
			writeError(w, http.StatusInternalServerError, "server_error", "resource store not available")
			return
		}
		defer access.pd.release()

		if access.fileHash != "" {
			etag := `"` + access.fileHash + `"`
			w.Header().Set("ETag", etag)
			if ifNoneMatchMatches(r, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

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

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if resourceReader.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(resourceReader.Size, 10))
		}

		written, err := io.Copy(w, resourceReader)
		if err != nil {
			slog.Error("copy resource failed", "book", bookID, "resource", resourcePath, "err", err)
		} else if resourceReader.Size >= 0 && written != resourceReader.Size {
			// archive/zip enforces the central-directory size during decode and
			// net/http drops the keep-alive on a short write, so this is not a
			// security gap. But a clean copy whose length disagrees with the
			// advertised Content-Length means the client received a truncated
			// body — surface it instead of letting it pass silently.
			slog.Warn("resource size mismatch; response truncated",
				"book", bookID, "resource", resourcePath,
				"declared", resourceReader.Size, "written", written)
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

	if hasSession {
		pd, openErr := deps.ProfileMgr.Get(r.Context(), sess.profile)
		if openErr != nil {
			slog.Error("open profile for resource failed", "profile", sess.profile, "book", bookID, "err", openErr)
			writeError(w, http.StatusInternalServerError, "profile_error", "failed to open profile")
			return resourceAccess{}, false
		}

		if book, found := pd.Books.Get(bookID); found {
			return resourceAccess{
				fileHash: book.FileHash,
				filePath: book.FilePath,
				pd:       pd,
			}, true
		}

		pd.release()
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return resourceAccess{}, false
	}

	if token == "" {
		writeUnauthenticated(w)
		return resourceAccess{}, false
	}

	// Token path: only search already-open profiles. Scanning all profiles for
	// an unauthenticated request would allow any bookID to trigger full library
	// scans (ScanNow) across every profile — a DoS amplifier. If the profile was
	// evicted after the chapter was served, the client gets 404 and can reload.
	pd, found := deps.ProfileMgr.FindBook(bookID)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return resourceAccess{}, false
	}

	book, ok := pd.Books.Get(bookID)
	if !ok {
		pd.release()
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return resourceAccess{}, false
	}

	if !validResourceToken(book.FileHash, token) {
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
