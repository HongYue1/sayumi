package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var gzPool = sync.Pool{
	New: func() any {
		writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return writer
	},
}

var copyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32<<10)
		return &buf
	},
}

func compressibleType(contentType string) bool {
	if i := strings.IndexByte(contentType, ';'); i != -1 {
		contentType = contentType[:i]
	}
	contentType = strings.TrimSpace(contentType)

	if contentType == "text/event-stream" {
		return false
	}

	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case contentType == "application/json",
		contentType == "application/javascript",
		contentType == "application/xml",
		contentType == "application/xhtml+xml",
		contentType == "application/x-javascript",
		contentType == "application/manifest+json",
		contentType == "image/svg+xml":
		return true
	}

	return false
}

// acceptsGzip reports whether the Accept-Encoding header value includes gzip
// with a non-zero q-value. It parses tokens of the form "encoding[;q=value]"
// directly, because mime.ParseMediaType requires a "type/subtype" slash and
// always errors on bare encoding tokens like "gzip" or "*".
func acceptsGzip(headerValue string) bool {
	sawGzip := false
	gzipAllowed := false
	sawStar := false
	starAllowed := false

	for part := range strings.SplitSeq(headerValue, ",") {
		encoding, q := parseEncodingToken(strings.TrimSpace(part))

		switch encoding {
		case "gzip":
			sawGzip = true
			gzipAllowed = q > 0
		case "*":
			sawStar = true
			starAllowed = q > 0
		}
	}

	if sawGzip {
		return gzipAllowed
	}
	return sawStar && starAllowed
}

// parseEncodingToken splits "encoding[;param=value;...]" into the lowercased
// encoding name and its quality value (1.0 when absent or unparseable).
func parseEncodingToken(s string) (encoding string, q float64) {
	q = 1.0
	before, after, ok := strings.Cut(s, ";")
	if !ok {
		encoding = strings.ToLower(s)
		return
	}
	encoding = strings.ToLower(strings.TrimSpace(before))
	for param := range strings.SplitSeq(after, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
		if ok && strings.TrimSpace(k) == "q" {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				q = parsed
			}
		}
	}
	return
}

func addVaryValue(header http.Header, value string) {
	existing := header.Values("Vary")
	for _, vary := range existing {
		for part := range strings.SplitSeq(vary, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func cacheControlNoTransform(value string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "no-transform") {
			return true
		}
	}
	return false
}

func shouldBypassGzipRequest(r *http.Request) bool {
	if r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.Header.Get("Upgrade") != "" {
		return true
	}

	for part := range strings.SplitSeq(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}

func copyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	bufPtr := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufPtr)
	return io.CopyBuffer(dst, src, *bufPtr)
}

// minGzipSize is the response-body floor below which we skip gzip entirely.
// Compressing a tiny body (e.g. a 31-byte JSON response) only adds ~18 bytes of
// gzip framing plus CPU and can make the response larger. 1400 bytes is roughly
// the payload of a single ~1500-byte TCP segment, so a response at or below it
// already fits in one packet and gains little from being compressed; it also
// matches the de-facto Go default (nytimes/gziphandler's DefaultMinSize) and its
// TCP-segment rationale.
const minGzipSize = 1400

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	statusCode  int
	headerSent  bool
	compress    bool
	decided     bool
	wroteHeader bool

	// pending buffers the first body bytes of a compression-eligible response
	// until we know whether it crosses minGzipSize. buffering is true while we are
	// accumulating into pending and have not yet committed to a decision.
	pending   []byte
	buffering bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.statusCode = code

	if code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent {
		g.decided = true
		g.compress = false
		g.ResponseWriter.WriteHeader(code)
		g.headerSent = true
	}
}

// eligible reports whether the response may be gzip-compressed based on its
// headers (set by the handler before the first write).
func (g *gzipResponseWriter) eligible() bool {
	header := g.Header()
	contentType := header.Get("Content-Type")
	return contentType != "" &&
		header.Get("Content-Encoding") == "" &&
		!cacheControlNoTransform(header.Get("Cache-Control")) &&
		compressibleType(contentType)
}

// writeHeaderOnce flushes the status line to the underlying writer exactly once.
func (g *gzipResponseWriter) writeHeaderOnce() {
	if g.headerSent {
		return
	}
	statusCode := g.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	g.ResponseWriter.WriteHeader(statusCode)
	g.headerSent = true
}

func (g *gzipResponseWriter) decide() {
	if g.decided {
		return
	}
	g.decided = true
	g.buffering = false

	g.compress = g.eligible()
	if g.compress {
		header := g.Header()
		header.Del("Content-Length")
		header.Set("Content-Encoding", "gzip")
	}
	g.writeHeaderOnce()
}

// beginCompressed transitions the writer into committed gzip mode: it sets the
// encoding headers, flushes the status line, and prepares the gzip writer. It
// writes no body bytes, so callers stream the body themselves afterwards.
func (g *gzipResponseWriter) beginCompressed() {
	g.decided = true
	g.buffering = false
	g.compress = true

	header := g.Header()
	header.Del("Content-Length")
	header.Set("Content-Encoding", "gzip")
	g.writeHeaderOnce()
	g.ensureWriter()
}

// commitCompressed commits a buffered, compression-eligible response to gzip: it
// sets the encoding headers, flushes the status line, and writes any pending
// bytes through the gzip writer.
func (g *gzipResponseWriter) commitCompressed() error {
	g.beginCompressed()

	if len(g.pending) > 0 {
		_, err := g.gz.Write(g.pending)
		g.pending = nil
		return err
	}
	return nil
}

// flushPending emits a buffered response that ended below minGzipSize: it is
// written uncompressed, with an explicit Content-Length since the full body is
// known.
func (g *gzipResponseWriter) flushPending() {
	g.decided = true
	g.buffering = false
	g.compress = false

	if g.Header().Get("Content-Length") == "" {
		g.Header().Set("Content-Length", strconv.Itoa(len(g.pending)))
	}
	g.writeHeaderOnce()
	if len(g.pending) > 0 {
		_, _ = g.ResponseWriter.Write(g.pending)
		g.pending = nil
	}
}

// finish settles a response after the handler returns: flush a still-buffered
// small body uncompressed, or make a decision for a handler that wrote nothing.
func (g *gzipResponseWriter) finish() {
	switch {
	case g.buffering:
		g.flushPending()
	case !g.headerSent:
		g.decide()
	}
}

func (g *gzipResponseWriter) ensureWriter() {
	if g.gz != nil {
		return
	}
	writer := gzPool.Get().(*gzip.Writer)
	writer.Reset(g.ResponseWriter)
	g.gz = writer
}

func (g *gzipResponseWriter) Write(data []byte) (int, error) {
	if g.decided {
		if !g.compress {
			return g.ResponseWriter.Write(data)
		}
		g.ensureWriter()
		return g.gz.Write(data)
	}

	if g.Header().Get("Content-Type") == "" {
		g.Header().Set("Content-Type", http.DetectContentType(data))
	}

	// Not compressible at all: decide now and stream straight through.
	if !g.eligible() {
		g.decide()
		return g.ResponseWriter.Write(data)
	}

	// Fast path: a single first write that already clears the size floor commits
	// to compression and streams straight through, skipping the pending copy. This
	// is the common case for writeJSON, which marshals the whole body and issues a
	// single Write -- buffering it would memcpy the entire body into pending for no
	// reason. Only valid when nothing is buffered yet; otherwise earlier chunks
	// must be flushed in order via the buffering path below.
	if !g.buffering && len(data) >= minGzipSize {
		g.beginCompressed()
		return g.gz.Write(data)
	}

	// Compressible: buffer until we cross the size floor (or the response ends
	// via finish/Flush). This defers the compress/skip decision until the body is
	// known to be worth compressing. We report the full length as written so the
	// caller (and io.Copy) sees no short write.
	g.buffering = true
	g.pending = append(g.pending, data...)
	if len(g.pending) >= minGzipSize {
		if err := g.commitCompressed(); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (g *gzipResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	// If a prior Write left bytes buffered, commit them (a ReadFrom stream implies
	// a body large enough to compress) before streaming the rest.
	if g.buffering {
		if err := g.commitCompressed(); err != nil {
			return 0, err
		}
	}

	if !g.decided && g.Header().Get("Content-Type") == "" {
		// Route through Write (not io.Copy) so content-type sniffing and the
		// buffering decision run. copyBuffered(g, …) cannot be used here: g
		// implements io.ReaderFrom, so io.CopyBuffer would re-dispatch into this
		// same ReadFrom and recurse indefinitely for any reader that does not
		// implement io.WriterTo.
		return g.writeFrom(reader)
	}

	if !g.decided {
		g.decide()
	}

	if !g.compress {
		if rf, ok := g.ResponseWriter.(io.ReaderFrom); ok {
			return rf.ReadFrom(reader)
		}
		return copyBuffered(g.ResponseWriter, reader)
	}

	g.ensureWriter()
	return copyBuffered(g.gz, reader)
}

// writeFrom drains reader into g via Write using a pooled buffer. It exists so
// ReadFrom can run the content-type-sniffing path without calling io.Copy,
// which would re-enter ReadFrom (g is an io.ReaderFrom) and recurse forever for
// readers lacking io.WriterTo. Write reports the full slice length on each call,
// so the returned total matches the bytes consumed from reader.
func (g *gzipResponseWriter) writeFrom(reader io.Reader) (int64, error) {
	bufPtr := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufPtr)
	buf := *bufPtr

	var total int64
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			written, writeErr := g.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func (g *gzipResponseWriter) Flush() {
	// An explicit flush means the handler wants bytes on the wire now, so a
	// still-buffered eligible response commits to compression rather than waiting
	// to cross the size floor.
	if g.buffering {
		_ = g.commitCompressed()
	} else if !g.decided {
		g.decide()
	}
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if flusher, ok := g.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (g *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := g.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipResponseWriter) close() {
	if g.gz == nil {
		return
	}
	// Only return the writer to the pool when Close succeeds. A failed Close
	// leaves the deflate stream in an undefined state; recycling it would
	// silently corrupt the next response that borrows it.
	if err := g.gz.Close(); err != nil {
		slog.Error("gzip close failed", "err", err)
		g.gz = nil
		return
	}
	gzPool.Put(g.gz)
	g.gz = nil
}

func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addVaryValue(w.Header(), "Accept-Encoding")

		if shouldBypassGzipRequest(r) || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}

		grw := &gzipResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}
		defer grw.close()

		next.ServeHTTP(grw, r)

		grw.finish()
	})
}
