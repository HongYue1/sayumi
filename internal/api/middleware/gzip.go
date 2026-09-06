package middleware

import (
	"bufio"
	"compress/gzip"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
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
	contentType = strings.ToLower(strings.TrimSpace(contentType))

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

// acceptsGzip parses the combined Accept-Encoding field, not a media type.
// Explicit gzip preferences take precedence over a wildcard. For conflicting
// duplicate codings, a refusal wins regardless of field order. Compression is
// optional here; malformed weights must not accidentally enable it.
func acceptsGzip(headerValue string) bool {
	sawGzip := false
	gzipAllowed := true
	sawStar := false
	starAllowed := true

	for part := range strings.SplitSeq(headerValue, ",") {
		encoding, q := parseEncodingToken(strings.TrimSpace(part))
		switch encoding {
		case "gzip":
			sawGzip = true
			gzipAllowed = gzipAllowed && q > 0
		case "*":
			sawStar = true
			starAllowed = starAllowed && q > 0
		}
	}
	if sawGzip {
		return gzipAllowed
	}
	return sawStar && starAllowed
}

// parseEncodingToken accepts one optional weight. Unknown/multiple parameters
// and invalid q-values conservatively disable that coding, including when a
// wildcard would otherwise allow it.
func parseEncodingToken(s string) (encoding string, q float64) {
	before, after, weighted := strings.Cut(s, ";")
	// Coding names are ASCII tokens; Unicode case conversion must not turn
	// a different, invalid name into an accepted gzip token.
	for i := range len(before) {
		if before[i] >= 0x80 {
			return "", 0
		}
	}
	encoding = strings.ToLower(strings.TrimSpace(before))
	if !weighted {
		return encoding, 1
	}
	key, value, ok := strings.Cut(strings.TrimSpace(after), "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
		return encoding, 0
	}
	value = strings.TrimSpace(value)
	// RFC weights are 0..1 with at most three decimal places. ParseFloat alone
	// would also accept signs, exponents, infinities, and out-of-range weights.
	if value == "" || (value[0] != '0' && value[0] != '1') {
		return encoding, 0
	}
	quality := int(value[0]-'0') * 1000
	if len(value) > 1 {
		if len(value) > 5 || value[1] != '.' {
			return encoding, 0
		}
		place := 100
		for i := 2; i < len(value); i++ {
			digit := value[i]
			if digit < '0' || digit > '9' || (value[0] == '1' && digit != '0') {
				return encoding, 0
			}
			quality += int(digit-'0') * place
			place /= 10
		}
	}
	return encoding, float64(quality) / 1000
}

// addVaryAcceptEncoding preserves both other selectors and Vary: *. Add it again when
// committing: a handler may replace the initial Vary field before its first
// write. Identity responses need the same selector as compressed responses.
func addVaryAcceptEncoding(header http.Header) {
	for _, vary := range header.Values("Vary") {
		for part := range strings.SplitSeq(vary, ",") {
			part = strings.TrimSpace(part)
			if part == "*" || strings.EqualFold(part, "Accept-Encoding") {
				return
			}
		}
	}
	header.Add("Vary", "Accept-Encoding")
}

func cacheControlNoTransform(value string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "no-transform") {
			return true
		}
	}
	return false
}

func hasHeaderValue(header http.Header, name string) bool {
	for _, value := range header.Values(name) {
		if value != "" {
			return true
		}
	}
	return false
}

func shouldBypassGzipRequest(r *http.Request) bool {
	if r.Method == http.MethodHead || hasHeaderValue(r.Header, "Range") || hasHeaderValue(r.Header, "Upgrade") {
		return true
	}
	for _, value := range r.Header.Values("Connection") {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
				return true
			}
		}
	}
	return false
}

func copyBuffered(dst io.Writer, src io.Reader) (int64, error) {
	bufPtr := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufPtr)
	return io.CopyBuffer(dst, src, *bufPtr)
}

// minGzipSize preserves the existing small-response policy. The 1400-byte
// threshold is not a guarantee of one TCP packet (headers/TLS also take space).
// An explicit Flush with pending bytes is the latency-driven exception.
const minGzipSize = 1400

type gzipResponseWriter struct {
	http.ResponseWriter
	gz             *gzip.Writer
	statusCode     int
	headerSent     bool
	compress       bool
	decided        bool
	wroteHeader    bool
	bypass         bool
	hijacked       bool
	headerSnapshot http.Header

	// pending holds fewer than minGzipSize bytes. A write that crosses the
	// threshold streams directly after this prefix; never copy a large later
	// write into pending just because a small earlier write was buffered.
	pending   []byte
	buffering bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.hijacked {
		return
	}
	if g.bypass {
		addVaryAcceptEncoding(g.Header())
		g.ResponseWriter.WriteHeader(code)
		return
	}
	if g.wroteHeader {
		return
	}
	// Match net/http's three-digit validation at the call site, not later in
	// finish. Informational responses do not commit the final status/headers.
	if code < 100 || code > 999 {
		panic("invalid WriteHeader code " + strconv.Itoa(code))
	}
	if code < 200 && code != http.StatusSwitchingProtocols {
		addVaryAcceptEncoding(g.Header())
		g.ResponseWriter.WriteHeader(code)
		return
	}
	g.wroteHeader = true
	g.statusCode = code
	if code == http.StatusNotModified {
		header := g.Header()
		// A 304 updates cached metadata. Do not turn a previously compressed
		// representation's weak validator back into a strong one. Handlers
		// often omit Content-Type on 304, so unknown types are conservative.
		if header.Get("Content-Type") == "" || g.eligible() {
			weakenETag(header)
			header.Del("Content-Length")
		}
	}
	if code == http.StatusSwitchingProtocols || code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent {
		g.decided = true
		g.writeHeaderOnce()
		return
	}
	g.snapshotHeader()
}

func (g *gzipResponseWriter) snapshotHeader() {
	if g.headerSnapshot == nil {
		// Clone the slices as well: handlers can retain and mutate Header values
		// while the size-floor decision is still pending.
		g.headerSnapshot = g.Header().Clone()
	}
}

func (g *gzipResponseWriter) responseHeader() http.Header {
	if g.headerSnapshot != nil {
		return g.headerSnapshot
	}
	return g.Header()
}

func (g *gzipResponseWriter) eligible() bool {
	header := g.responseHeader()
	if hasHeaderValue(header, "Content-Encoding") || hasHeaderValue(header, "Content-Range") || !compressibleType(header.Get("Content-Type")) {
		return false
	}
	return !slices.ContainsFunc(header.Values("Cache-Control"), cacheControlNoTransform)
}

func (g *gzipResponseWriter) writeHeaderOnce() {
	if g.headerSent {
		return
	}
	if !g.wroteHeader {
		g.wroteHeader = true
		g.statusCode = http.StatusOK
	}
	header := g.responseHeader()
	addVaryAcceptEncoding(header)
	if g.headerSnapshot == nil {
		g.ResponseWriter.WriteHeader(g.statusCode)
	} else {
		// Temporarily expose the committed snapshot to the underlying writer.
		// Restore the live map afterwards so late trailer values (including
		// TrailerPrefix keys) and retained Header references remain usable.
		live := g.Header()
		saved := maps.Clone(live)
		clear(live)
		maps.Copy(live, header)
		g.ResponseWriter.WriteHeader(g.statusCode)
		clear(live)
		maps.Copy(live, saved)
		g.headerSnapshot = nil
	}
	g.headerSent = true
}

func weakenETag(header http.Header) {
	if tag := header.Get("ETag"); strings.HasPrefix(tag, `"`) {
		header.Set("ETag", "W/"+tag)
	}
}

func (g *gzipResponseWriter) beginCompressed() {
	g.decided = true
	g.buffering = false
	g.compress = true
	header := g.responseHeader()
	header.Del("Content-Length")
	header.Set("Content-Encoding", "gzip")
	// Compression changes representation bytes. Keep the handler's opaque
	// validator for weak revalidation, but do not promise byte identity for
	// strong comparisons such as If-Range. Existing weak validators stay weak.
	weakenETag(header)
	g.writeHeaderOnce()
	g.ensureWriter()
}

func (g *gzipResponseWriter) commitCompressed() error {
	g.beginCompressed()
	if len(g.pending) > 0 {
		_, err := g.gz.Write(g.pending)
		g.pending = nil
		return err
	}
	return nil
}

func (g *gzipResponseWriter) hasTrailers() bool {
	for _, header := range []http.Header{g.responseHeader(), g.Header()} {
		if hasHeaderValue(header, "Trailer") {
			return true
		}
		for key := range header {
			if strings.HasPrefix(key, http.TrailerPrefix) {
				return true
			}
		}
	}
	return false
}

func (g *gzipResponseWriter) flushPending() error {
	g.decided = true
	g.buffering = false
	header := g.responseHeader()
	// A fixed length would suppress HTTP/1 chunked trailers. Let net/http own
	// framing when the handler uses either supported trailer mechanism.
	if header.Get("Content-Length") == "" && !g.hasTrailers() {
		header.Set("Content-Length", strconv.Itoa(len(g.pending)))
	}
	g.writeHeaderOnce()
	pending := g.pending
	g.pending = nil
	if len(pending) == 0 {
		return nil
	}
	n, err := g.ResponseWriter.Write(pending)
	if err == nil && n != len(pending) {
		return io.ErrShortWrite
	}
	return err
}

func (g *gzipResponseWriter) finish() {
	if g.hijacked {
		return
	}
	if g.bypass {
		addVaryAcceptEncoding(g.Header())
		return
	}
	switch {
	case g.buffering:
		if err := g.flushPending(); err != nil {
			slog.Error("gzip buffered write failed", "err", err)
		}
	case !g.headerSent:
		// No body means no encoding, including an empty ReaderFrom. Advertising
		// gzip without creating a complete stream breaks strict decoders.
		g.decided = true
		g.writeHeaderOnce()
	}
}

func (g *gzipResponseWriter) ensureWriter() {
	if g.gz == nil {
		g.gz = gzPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
}

func (g *gzipResponseWriter) Write(data []byte) (int, error) {
	if g.hijacked {
		return 0, http.ErrHijacked
	}
	if g.bypass {
		addVaryAcceptEncoding(g.Header())
		return g.ResponseWriter.Write(data)
	}
	if !g.wroteHeader {
		g.wroteHeader = true
		g.statusCode = http.StatusOK
	}
	if g.decided {
		if !g.compress {
			return g.ResponseWriter.Write(data)
		}
		g.ensureWriter()
		return g.gz.Write(data)
	}
	if len(data) == 0 {
		g.snapshotHeader()
		return 0, nil
	}
	header := g.responseHeader()
	if _, exists := header["Content-Type"]; !exists && !hasHeaderValue(header, "Content-Encoding") {
		header.Set("Content-Type", http.DetectContentType(data))
	}
	if !g.eligible() {
		g.decided = true
		g.writeHeaderOnce()
		return g.ResponseWriter.Write(data)
	}
	// Preserve the large-first-Write fast path and avoid copying a large later
	// write. Only the small prefix needs buffering for the size-floor decision.
	if len(data) >= minGzipSize-len(g.pending) {
		if err := g.commitCompressed(); err != nil {
			return 0, err
		}
		return g.gz.Write(data)
	}
	g.snapshotHeader()
	g.buffering = true
	g.pending = append(g.pending, data...)
	return len(data), nil
}

func (g *gzipResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if g.hijacked {
		return 0, http.ErrHijacked
	}
	if g.bypass {
		addVaryAcceptEncoding(g.Header())
	} else if !g.decided {
		// The reader interface says nothing about the eventual size. Route
		// through Write until it makes the same decision as ordinary writes.
		// In particular, EOF must not force gzip or commit an implicit 200.
		return g.writeFrom(reader)
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

// writeFrom deliberately avoids io.CopyBuffer(g, reader): its ReaderFrom
// dispatch would recurse. Preserve read errors after processing any returned
// bytes, and detect a short nil-error write just as io.Copy does.
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
			if written != n {
				return total, io.ErrShortWrite
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

func (g *gzipResponseWriter) Flush() { _ = g.FlushError() }

func (g *gzipResponseWriter) FlushError() error {
	if g.hijacked {
		return http.ErrHijacked
	}
	if g.bypass {
		addVaryAcceptEncoding(g.Header())
	} else if !g.decided {
		if len(g.pending) > 0 {
			if err := g.commitCompressed(); err != nil {
				return err
			}
		} else {
			// A header-only flush commits identity. Do not advertise gzip with
			// zero bytes and no writer to produce its framing on return.
			g.decided = true
			g.writeHeaderOnce()
		}
	}
	if g.gz != nil {
		if err := g.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(g.ResponseWriter).Flush()
}

func (g *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if g.hijacked {
		return nil, nil, http.ErrHijacked
	}
	// Check support before flushing any delayed response. Unwrap-aware callers
	// must not lose hijacking just because another wrapper sits below us.
	writer := g.ResponseWriter
	var hijacker http.Hijacker
	for {
		if h, ok := writer.(http.Hijacker); ok {
			hijacker = h
			break
		}
		if w, ok := writer.(interface{ Unwrap() http.ResponseWriter }); ok {
			writer = w.Unwrap()
			continue
		}
		return nil, nil, http.ErrNotSupported
	}
	if !g.bypass && g.wroteHeader {
		// Hand off all bytes already accepted by Write, including a complete
		// gzip member. Do not synthesize a response when hijacking before Write.
		if !g.decided {
			if len(g.pending) > 0 {
				if err := g.commitCompressed(); err != nil {
					return nil, nil, err
				}
			} else {
				g.decided = true
				g.writeHeaderOnce()
			}
		}
		if g.gz != nil {
			if err := g.gz.Close(); err != nil {
				g.gz = nil
				return nil, nil, err
			}
			g.recycle()
		}
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		g.hijacked = true
	}
	return conn, rw, err
}

func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := g.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

func (g *gzipResponseWriter) recycle() {
	if g.gz != nil {
		// Detach the response/request graph before putting the writer back in
		// the pool. Reset also lets panic cleanup discard an unfinished stream
		// without appending a misleading successful footer to a failed reply.
		g.gz.Reset(io.Discard)
		gzPool.Put(g.gz)
		g.gz = nil
	}
}

func (g *gzipResponseWriter) close() {
	if g.gz == nil {
		return
	}
	if err := g.gz.Close(); err != nil {
		slog.Error("gzip close failed", "err", err)
		g.gz = nil
		return
	}
	g.recycle()
}

func Gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addVaryAcceptEncoding(w.Header())
		grw := &gzipResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			bypass:         shouldBypassGzipRequest(r) || !acceptsGzip(strings.Join(r.Header.Values("Accept-Encoding"), ",")),
		}
		returned := false
		defer func() {
			if returned {
				grw.close()
			} else {
				grw.recycle()
			}
		}()
		next.ServeHTTP(grw, r)
		grw.finish()
		returned = true
	})
}
