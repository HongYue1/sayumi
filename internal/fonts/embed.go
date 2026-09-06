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
			writePlainStatus(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// User fonts: /user/<dir>/<file>
		if dir, file, ok := parseUserFontPath(r.URL.Path); ok {
			serveUserFont(w, r, scanner, dir, file)
			return
		}

		reqPath, ok := sanitizeFontRequestPath(r.URL.Path)
		if !ok {
			writePlainStatus(w, r, http.StatusNotFound, "not found")
			return
		}

		data, ok := fontData[reqPath]
		if !ok {
			writePlainStatus(w, r, http.StatusNotFound, "not found")
			return
		}

		w.Header().Set("Content-Type", epub.ContentTypeByExt(reqPath))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if etag, ok := fontETags[reqPath]; ok {
			w.Header().Set("ETag", etag)
			if etagMatches(strings.Join(r.Header.Values("If-None-Match"), ","), etag) {
				w.WriteHeader(http.StatusNotModified)
				return
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
	rest, found := strings.CutPrefix(rawPath, "/user/")
	if !found {
		return "", "", false
	}
	dir, file, found = strings.Cut(rest, "/")
	if !found || !validFontPathSegment(dir) || !validFontPathSegment(file) {
		return "", "", false
	}
	return dir, file, true
}

// Reject traversal components, not literal dots within a real filename. The
// scanner uses the same rule so every advertised name is addressable by HTTP.
func validFontPathSegment(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func serveUserFont(w http.ResponseWriter, r *http.Request, scanner *Scanner, dir, file string) {
	if scanner == nil {
		writePlainStatus(w, r, http.StatusNotFound, "not found")
		return
	}

	setUserFontHeaders := func(etag string) {
		w.Header().Set("Content-Type", epub.ContentTypeByExt(file))
		// User fonts can change on disk, so allow revalidation via ETag rather
		// than marking immutable like the embedded set.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}

	// HEAD and conditional (If-None-Match) requests are answered from a stat
	// alone, so the font bytes are never read off disk for a 304 or a HEAD. A
	// plain GET skips this pre-stat and reads exactly once below — otherwise
	// every cache-miss load would pay two os.OpenRoot+stat round-trips to serve
	// one file.
	ifNoneMatch := strings.Join(r.Header.Values("If-None-Match"), ",")
	if r.Method == http.MethodHead || ifNoneMatch != "" {
		size, etag, ok := scanner.StatUserFont(dir, file)
		if !ok {
			writePlainStatus(w, r, http.StatusNotFound, "not found")
			return
		}
		setUserFontHeaders(etag)
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))

		if etagMatches(ifNoneMatch, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Conditional GET whose ETag did not match: fall through and read the
		// body, re-syncing the headers from the bytes actually read.
	}

	data, etag, ok := scanner.ReadUserFont(dir, file)
	if !ok {
		// A file can disappear after conditional stat. The error response must
		// discard both entity metadata and the successful font's cache policy.
		writePlainStatus(w, r, http.StatusNotFound, "not found")
		return
	}
	setUserFontHeaders(etag)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))

	_, _ = w.Write(data)
}

// etagMatches applies If-None-Match's weak comparison (RFC 9110, section
// 13.1.2). Call only after establishing that the representation exists, so
// wildcard matching cannot turn a missing font into a 304. Quoted opaque tags
// can contain commas; splitting the field on every comma loses that boundary.
func etagMatches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	etag = strings.TrimPrefix(etag, "W/")
	for {
		ifNoneMatch = strings.Trim(ifNoneMatch, " \t")
		if ifNoneMatch == "" {
			return false
		}
		if ifNoneMatch == "*" {
			return true
		}
		if ifNoneMatch[0] == ',' {
			ifNoneMatch = ifNoneMatch[1:]
			continue
		}
		candidate := strings.TrimPrefix(ifNoneMatch, "W/")
		if len(candidate) == 0 || candidate[0] != '"' {
			return false
		}
		end := 1
		for end < len(candidate) && candidate[end] != '"' {
			if c := candidate[end]; c < 0x21 || c == 0x7f {
				return false
			}
			end++
		}
		if end == len(candidate) {
			return false
		}
		rest := strings.TrimLeft(candidate[end+1:], " \t")
		if rest != "" && rest[0] != ',' {
			return false
		}
		if candidate[:end+1] == etag {
			return true
		}
		if rest == "" {
			return false
		}
		ifNoneMatch = rest[1:]
	}
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

func writePlainStatus(w http.ResponseWriter, r *http.Request, status int, message string) {
	header := w.Header()
	header.Del("Content-Length")
	header.Del("ETag")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(message + "\n"))
	}
}
