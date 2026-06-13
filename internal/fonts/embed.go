package fonts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"sayumi/internal/epub"
)

//go:embed *.woff2
var fontFiles embed.FS

var fontData = loadFontData()

// fontETags holds a precomputed ETag for each embedded font file. ETags are
// derived from a SHA-256 of the file bytes at init time, so they are stable
// across requests and change only when the binary changes.
var fontETags = computeETags(fontData)

func computeETags(data map[string][]byte) map[string]string {
	tags := make(map[string]string, len(data))
	for path, b := range data {
		sum := sha256.Sum256(b)
		// Quoted as required by RFC 7232.
		// 16 hex chars = 64-bit prefix of SHA-256, sufficient for cache invalidation.
		tags[path] = `"` + hex.EncodeToString(sum[:])[:16] + `"`
	}
	return tags
}

func loadFontData() map[string][]byte {
	dataByPath := make(map[string][]byte)

	err := fs.WalkDir(fontFiles, ".", func(filePath string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk embedded font %q: %w", filePath, walkErr)
		}
		if dirEntry.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(fontFiles, filePath)
		if err != nil {
			return fmt.Errorf("read embedded font %q: %w", filePath, err)
		}
		dataByPath[filePath] = data
		return nil
	})
	if err != nil {
		panic(err)
	}

	return dataByPath
}

// Handler serves font files under /fonts/ (the caller strips that prefix).
// Embedded fonts are served by flat filename ("/Spectral-Regular.woff2");
// user fonts dropped into ./Fonts/<Family>/ are served under
// "/user/<dir>/<file>". scanner may be nil if no user-fonts directory exists.
func Handler(scanner *Scanner) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setFontCORSHeaders(w.Header())

		switch r.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodOptions:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			writePlainStatus(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// User fonts: /user/<dir>/<file>
		if dir, file, ok := parseUserFontPath(r.URL.Path); ok {
			serveUserFont(w, r, scanner, dir, file)
			return
		}

		reqPath, ok := sanitizeFontRequestPath(r.URL.Path)
		if !ok {
			writePlainStatus(w, http.StatusNotFound, "not found")
			return
		}

		data, ok := fontData[reqPath]
		if !ok {
			writePlainStatus(w, http.StatusNotFound, "not found")
			return
		}

		w.Header().Set("Content-Type", epub.ContentTypeByExt(reqPath))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if etag, ok := fontETags[reqPath]; ok {
			w.Header().Set("ETag", etag)
			if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" {
				for candidate := range strings.SplitSeq(ifNoneMatch, ",") {
					if strings.TrimSpace(candidate) == etag {
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
			}
		}

		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		_, _ = w.Write(data)
	})
}

// parseUserFontPath matches "/user/<dir>/<file>" with no further nesting or
// traversal. Returns the directory and file segments on success.
func parseUserFontPath(rawPath string) (dir, file string, ok bool) {
	if strings.Contains(rawPath, `\`) || strings.Contains(rawPath, "..") {
		return "", "", false
	}
	rest, found := strings.CutPrefix(rawPath, "/user/")
	if !found {
		return "", "", false
	}
	dir, file, found = strings.Cut(rest, "/")
	if !found || dir == "" || file == "" || strings.Contains(file, "/") {
		return "", "", false
	}
	return dir, file, true
}

func serveUserFont(w http.ResponseWriter, r *http.Request, scanner *Scanner, dir, file string) {
	if scanner == nil {
		writePlainStatus(w, http.StatusNotFound, "not found")
		return
	}
	// Stat first so conditional (If-None-Match) and HEAD requests are answered
	// without ever reading the font bytes off disk.
	size, etag, ok := scanner.StatUserFont(dir, file)
	if !ok {
		writePlainStatus(w, http.StatusNotFound, "not found")
		return
	}

	w.Header().Set("Content-Type", epub.ContentTypeByExt(file))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	// User fonts can change on disk, so allow revalidation via ETag rather than
	// marking immutable like the embedded set.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" {
		for candidate := range strings.SplitSeq(ifNoneMatch, ",") {
			if strings.TrimSpace(candidate) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	data, readETag, ok := scanner.ReadUserFont(dir, file)
	if !ok {
		// Raced with a change or removal between the stat above and this read;
		// drop the stale length/ETag so we don't emit a mismatched response.
		w.Header().Del("Content-Length")
		w.Header().Del("ETag")
		writePlainStatus(w, http.StatusNotFound, "not found")
		return
	}
	// Re-sync length and ETag from the bytes actually read, in case the file
	// changed on disk between the stat above and this read.
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("ETag", readETag)

	_, _ = w.Write(data)
}

func setFontCORSHeaders(header http.Header) {
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type")
}

func sanitizeFontRequestPath(rawPath string) (string, bool) {
	if rawPath == "" || strings.Contains(rawPath, `\`) || strings.Contains(rawPath, "..") {
		return "", false
	}

	// StripPrefix already removed "/fonts", so rawPath is like "/Foo.woff2"
	cleaned := strings.TrimPrefix(rawPath, "/")
	if cleaned == "" || cleaned == "." || strings.Contains(cleaned, "/") {
		return "", false
	}

	return cleaned, true
}

func writePlainStatus(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}
