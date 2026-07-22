package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func downloadResponseETag(fileHash string) string {
	if fileHash == "" {
		return ""
	}
	return `"` + fileHash + `:file"`
}

// downloadBookHandler serves GET /api/books/{id}/file: it streams the book's
// original .epub with a Content-Disposition that makes the browser download it
// (used by the share dialog's "Download EPUB" action). Auth is the session
// cookie, which a same-origin <a download> request carries automatically.
func downloadBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")

		// Keep the cache snapshot and streamed bytes on one complete EPUB
		// generation. The read lock also prevents an in-app edit from attempting
		// an atomic rename while Windows still has this download handle open.
		pd.bookReplaceMu.RLock()
		defer pd.bookReplaceMu.RUnlock()

		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}
		if book.FilePath == "" {
			writeError(w, http.StatusNotFound, "no_file", "book has no file on disk")
			return
		}

		file, err := os.Open(book.FilePath)
		if err != nil {
			slog.Error("open book file for download failed", "book", id, "err", err)
			writeError(w, http.StatusNotFound, "no_file", "book file not found")
			return
		}
		defer func() { _ = file.Close() }()

		// Name the download after the book's title rather than the on-disk file
		// name. sanitizeFilename strips path separators / control chars and
		// guarantees a single ".epub" suffix.
		filename := sanitizeFilename(book.Title)

		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
		// The .epub can change in place (in-app metadata/cover edits rewrite it),
		// so revalidate rather than cache an out-of-date copy.
		w.Header().Set("Cache-Control", "private, no-cache")
		if etag := downloadResponseETag(book.FileHash); etag != "" {
			w.Header().Set("ETag", etag)
			if ifNoneMatchMatches(r, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		// Streaming a large book over a slow link can outlast the server
		// WriteTimeout armed at header-read time; clear the write deadline
		// (mirrors the gofile/cover handlers). ServeContent streams the file
		// without buffering it whole and supports range/resume requests. The
		// content hash above is the authoritative validator; a zero modtime avoids
		// stale same-second Last-Modified revalidation after an in-app rewrite.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear download write deadline unsupported", "err", err)
		}

		http.ServeContent(w, r, filename, time.Time{}, file)
	}
}

// contentDispositionAttachment builds an attachment Content-Disposition header
// that survives non-ASCII titles (e.g. Arabic). It emits an ASCII-only
// filename= fallback for legacy clients plus an RFC 5987 filename*=UTF-8”
// form that modern browsers prefer, so the saved file keeps the real title.
func contentDispositionAttachment(name string) string {
	var ascii strings.Builder
	for _, r := range name {
		if r < 0x20 || r >= 0x7f || r == '"' || r == '\\' {
			ascii.WriteByte('_')
		} else {
			ascii.WriteRune(r)
		}
	}
	fallback := ascii.String()
	if strings.Trim(fallback, "_") == "" {
		fallback = "book.epub"
	}

	var enc strings.Builder
	for _, b := range []byte(name) {
		if isRFC5987AttrChar(b) {
			enc.WriteByte(b)
		} else {
			fmt.Fprintf(&enc, "%%%02X", b)
		}
	}

	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", fallback, enc.String())
}

// isRFC5987AttrChar reports whether b is an RFC 5987 attr-char that may appear
// unescaped in an ext-value (filename*). All other bytes are percent-encoded.
func isRFC5987AttrChar(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
