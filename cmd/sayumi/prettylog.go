package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// prettyHandler is a slog.Handler that writes compact, color-coded lines to w.
//
// Request lines (msg="request") are formatted as:
//
//	HH:MM:SS  DBG  GET   200  /path/…   1.5ms
//
// All other lines:
//
//	HH:MM:SS  INF  scanning library   profile=Raven books=4
type prettyHandler struct {
	// mu is shared by pointer across every derived handler (WithAttrs/WithGroup)
	// so all of them serialize writes to the same w, as the slog.Handler
	// contract requires of a shared writer.
	mu    *sync.Mutex
	w     io.Writer
	level slog.Level
	// preAttrs holds attributes added via WithAttrs, each pre-keyed with the
	// group prefix that was active at the time of the WithAttrs call. This
	// satisfies the slog.Handler contract: attributes added before a WithGroup
	// call must not be rendered with that group's prefix.
	preAttrs    map[string]string
	groupPrefix string
}

// priorityKeys controls the order of well-known attributes in generic log lines.
var priorityKeys = []string{
	"path", "profile", "books", "imported", "book",
	"width", "height", "kind", "err", "panic",
}

// priorityKeySet enables O(1) membership tests for the secondary (non-priority) loop.
var priorityKeySet = func() map[string]bool {
	m := make(map[string]bool, len(priorityKeys))
	for _, k := range priorityKeys {
		m[k] = true
	}
	return m
}()

func newPrettyHandler(w io.Writer, level slog.Level) slog.Handler {
	return &prettyHandler{mu: new(sync.Mutex), w: w, level: level, preAttrs: make(map[string]string)}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

// WithAttrs records each attribute under its fully-qualified key (current
// groupPrefix + attr.Key) so that subsequent WithGroup calls cannot
// retroactively change the prefix of these attributes.
func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make(map[string]string, len(h.preAttrs)+len(attrs))
	maps.Copy(merged, h.preAttrs)
	for _, a := range attrs {
		merged[h.groupPrefix+a.Key] = logValueString(a.Value)
	}
	return &prettyHandler{mu: h.mu, w: h.w, level: h.level, preAttrs: merged, groupPrefix: h.groupPrefix}
}

// WithGroup returns a handler whose subsequent attribute keys are prefixed with
// name followed by a dot, satisfying the slog.Handler contract.
func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	prefix := h.groupPrefix + name + "."
	// Copy preAttrs — they were keyed at their own prefix and must not change.
	copied := make(map[string]string, len(h.preAttrs))
	maps.Copy(copied, h.preAttrs)
	return &prettyHandler{mu: h.mu, w: h.w, level: h.level, preAttrs: copied, groupPrefix: prefix}
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	isRequest := r.Message == "request"
	all := make(map[string]string, len(h.preAttrs)+r.NumAttrs())
	// Pre-attrs are already fully-qualified; copy them directly.
	maps.Copy(all, h.preAttrs)
	// Record attrs are keyed relative to the current group prefix.
	if isRequest {
		r.Attrs(func(a slog.Attr) bool {
			all[h.groupPrefix+a.Key] = a.Value.String()
			return true
		})
		if path, ok := all["path"]; ok {
			all["path"] = escapeLogText(path)
		}
	} else {
		r.Attrs(func(a slog.Attr) bool {
			all[h.groupPrefix+a.Key] = logValueString(a.Value)
			return true
		})
	}

	var sb strings.Builder

	sb.WriteString(ansiDim)
	sb.WriteString(r.Time.Format(time.TimeOnly))
	sb.WriteString(ansiReset)
	sb.WriteString("  ")

	sb.WriteString(levelTag(r.Level))
	sb.WriteString("  ")

	if isRequest {
		h.writeRequest(&sb, all)
	} else {
		h.writeGeneric(&sb, escapeLogText(r.Message), all)
	}

	sb.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprint(h.w, sb.String())
	return err
}

func (h *prettyHandler) writeRequest(sb *strings.Builder, attrs map[string]string) {
	method := attrs["method"]
	path := attrs["path"]
	status := attrs["status"]
	duration := attrs["duration"]

	sb.WriteString(methodColor(method))
	fmt.Fprintf(sb, "%-6s", method)
	sb.WriteString(ansiReset)
	sb.WriteString("  ")

	sb.WriteString(statusColor(status))
	sb.WriteString(status)
	sb.WriteString(ansiReset)
	sb.WriteString("  ")

	sb.WriteString(path)

	if duration != "" && duration != "0s" {
		d := fmtDuration(duration)
		pad := max(0, 60-len(path))
		sb.WriteString(strings.Repeat(" ", pad))
		sb.WriteString(ansiDim)
		sb.WriteString(d)
		sb.WriteString(ansiReset)
	}

	if size := attrs["size"]; size != "" {
		sb.WriteString(ansiDim)
		sb.WriteString("  ")
		sb.WriteString(size)
		sb.WriteString(ansiReset)
	}
}

func (h *prettyHandler) writeGeneric(sb *strings.Builder, msg string, attrs map[string]string) {
	sb.WriteString(msg)

	for _, k := range priorityKeys {
		if v, ok := attrs[k]; ok {
			sb.WriteString("  ")
			sb.WriteString(ansiDim)
			sb.WriteString(k)
			sb.WriteString("=")
			sb.WriteString(ansiReset)
			sb.WriteString(v)
		}
	}

	// Collect and sort non-priority keys for deterministic output order.
	extraKeys := make([]string, 0, len(attrs))
	for k := range attrs {
		if !priorityKeySet[k] {
			extraKeys = append(extraKeys, k)
		}
	}
	slices.Sort(extraKeys)
	for _, k := range extraKeys {
		sb.WriteString("  ")
		sb.WriteString(ansiDim)
		sb.WriteString(escapeLogText(k))
		sb.WriteString("=")
		sb.WriteString(ansiReset)
		sb.WriteString(attrs[k])
	}
}

func levelTag(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return ansiRed + "ERR" + ansiReset
	case l >= slog.LevelWarn:
		return ansiYellow + "WRN" + ansiReset
	case l >= slog.LevelInfo:
		return ansiCyan + "INF" + ansiReset
	default:
		return ansiDim + "DBG" + ansiReset
	}
}

func methodColor(m string) string {
	switch m {
	case "POST", "PUT", "PATCH":
		return ansiCyan
	case "DELETE":
		return ansiRed
	default:
		return ansiDim
	}
}

func statusColor(s string) string {
	n, _ := strconv.Atoi(s)
	switch {
	case n >= 500:
		return ansiRed
	case n >= 400:
		return ansiYellow
	case n >= 300:
		return ansiCyan
	default:
		return ansiGreen
	}
}

// fmtDuration trims Go's default duration string to 1 decimal place.
// e.g. "32.4339ms" → "32.4ms", "1.0016ms" → "1.0ms"
func fmtDuration(s string) string {
	for _, unit := range []string{"ms", "µs", "us", "s"} {
		if before, ok := strings.CutSuffix(s, unit); ok {
			num := before
			if i := strings.Index(num, "."); i != -1 && i+2 < len(num) {
				num = num[:i+2]
			}
			return num + unit
		}
	}
	return s
}

// logValueString escapes only value kinds that can carry caller-controlled
// text. Numeric, boolean, time, and duration kinds use slog's fixed encodings
// and can bypass the scan.
func logValueString(value slog.Value) string {
	text := value.String()
	if value.Kind() == slog.KindString || value.Kind() == slog.KindAny {
		return escapeLogText(text)
	}
	return text
}

// escapeLogText preserves ordinary Unicode while making terminal controls and
// physical line separators visible. The handler's own ANSI sequences are added
// after this boundary, so callers cannot inject terminal commands or forge
// additional log lines through messages, keys, or values.
func escapeLogText(s string) string {
	if !needsLogEscape(s) {
		return s
	}

	const hex = "0123456789abcdef"

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch c {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			default:
				if c < ' ' || c == 0x7f {
					b.WriteString(`\x`)
					b.WriteByte(hex[c>>4])
					b.WriteByte(hex[c&0x0f])
				} else {
					b.WriteByte(c)
				}
			}
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
			i++
			continue
		}
		switch {
		case r >= 0x80 && r <= 0x9f:
			b.WriteString(`\x`)
			b.WriteByte(hex[byte(r)>>4])
			b.WriteByte(hex[byte(r)&0x0f])
		case r == '\u2028':
			b.WriteString(`\u2028`)
		case r == '\u2029':
			b.WriteString(`\u2029`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

func needsLogEscape(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c < ' ' || c == 0x7f {
				return true
			}
			i++
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || r >= 0x80 && r <= 0x9f || r == '\u2028' || r == '\u2029' {
			return true
		}
		i += size
	}
	return false
}
