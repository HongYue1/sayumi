package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEscapeLogText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ordinary unicode", in: "Raven — 日本語", want: "Raven — 日本語"},
		{name: "line controls", in: "a\r\nb\tc", want: `a\r\nb\tc`},
		{name: "terminal escape", in: "a\x1b[2Jb", want: `a\x1b[2Jb`},
		{name: "C1 controls", in: "a\u0085\u009bb", want: `a\x85\x9bb`},
		{name: "unicode separators", in: "a\u2028b\u2029c", want: `a\u2028b\u2029c`},
		{name: "invalid UTF-8", in: string([]byte{'a', 0xff, 'b'}), want: `a\xffb`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := escapeLogText(tt.in); got != tt.want {
				t.Fatalf("escapeLogText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrettyHandlerEscapesGenericControls(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	handler := newPrettyHandler(&output, slog.LevelDebug)
	record := slog.NewRecord(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC), slog.LevelInfo, "imported\nbook", 0)
	unsafeKey := "bad\nkey"
	record.AddAttrs(
		slog.String("title", "Raven\x1b[2J\r\nSecond line"),
		slog.String("author", "日本語\u2028Author"),
		slog.String(unsafeKey, "visible"),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := output.String()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("physical newline count = %d, want 1; output %q", strings.Count(got, "\n"), got)
	}
	for _, want := range []string{`imported\nbook`, `Raven\x1b[2J\r\nSecond line`, `日本語\u2028Author`, `bad\nkey`} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("output contains injected terminal clear sequence: %q", got)
	}
	if !strings.Contains(got, ansiCyan) || !strings.Contains(got, ansiDim) {
		t.Fatalf("handler-generated colors missing from output %q", got)
	}
}

func TestPrettyHandlerEscapesRequestControls(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	handler := newPrettyHandler(&output, slog.LevelDebug)
	record := slog.NewRecord(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC), slog.LevelDebug, "request", 0)
	record.AddAttrs(
		slog.String("method", "GET"),
		slog.String("path", "/books\r\nforged\x1b[2J"),
		slog.Int("status", 200),
		slog.String("size", "12B"),
		slog.Duration("duration", 1500*time.Microsecond),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := output.String()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("physical newline count = %d, want 1; output %q", strings.Count(got, "\n"), got)
	}
	for _, want := range []string{"GET", `/books\r\nforged\x1b[2J`, "1.5ms", "12B"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("output contains injected terminal clear sequence: %q", got)
	}
}

func BenchmarkEscapeLogText(b *testing.B) {
	for _, benchmark := range []struct {
		name string
		text string
	}{
		{name: "safe", text: "/api/books/Raven-日本語"},
		{name: "controls", text: "/api/books\r\nforged\x1b[2J"},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = escapeLogText(benchmark.text)
			}
		})
	}
}

func BenchmarkPrettyHandlerDebugRequest(b *testing.B) {
	record := benchmarkRequestRecord()
	ctx := context.Background()

	b.Run("legacy", func(b *testing.B) {
		handler := newLegacyPrettyHandler(io.Discard, slog.LevelDebug)
		b.ReportAllocs()
		for b.Loop() {
			if err := handler.Handle(ctx, record); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("escaped", func(b *testing.B) {
		handler := newPrettyHandler(io.Discard, slog.LevelDebug)
		b.ReportAllocs()
		for b.Loop() {
			if err := handler.Handle(ctx, record); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPrettyHandlerDebugRequestInterleaved(b *testing.B) {
	const batchSize = 32

	record := benchmarkRequestRecord()
	ctx := context.Background()
	legacy := newLegacyPrettyHandler(io.Discard, slog.LevelDebug)
	escaped := newPrettyHandler(io.Discard, slog.LevelDebug)
	var legacyElapsed, escapedElapsed time.Duration

	runBatch := func(handler slog.Handler) time.Duration {
		start := time.Now()
		for range batchSize {
			if err := handler.Handle(ctx, record); err != nil {
				b.Fatal(err)
			}
		}
		return time.Since(start)
	}

	iteration := 0
	for b.Loop() {
		if iteration%2 == 0 {
			legacyElapsed += runBatch(legacy)
			escapedElapsed += runBatch(escaped)
		} else {
			escapedElapsed += runBatch(escaped)
			legacyElapsed += runBatch(legacy)
		}
		iteration++
	}

	operations := float64(b.N * batchSize)
	b.ReportMetric(float64(legacyElapsed.Nanoseconds())/operations, "legacy-ns/op")
	b.ReportMetric(float64(escapedElapsed.Nanoseconds())/operations, "escaped-ns/op")
}

func benchmarkRequestRecord() slog.Record {
	record := slog.NewRecord(time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC), slog.LevelDebug, "request", 0)
	record.AddAttrs(
		slog.String("method", "GET"),
		slog.String("path", "/api/books/123"),
		slog.Int("status", 200),
		slog.String("size", "1.5KB"),
		slog.Duration("duration", 1500*time.Microsecond),
	)
	return record
}

// legacyPrettyHandler preserves the complete unchanged handler path for the
// benchmark only. It intentionally omits escapeLogText while retaining the
// same interface dispatch, pre-attribute map, group prefixing, formatting, and
// writer lock as the replacement.
type legacyPrettyHandler struct {
	mu          *sync.Mutex
	w           io.Writer
	level       slog.Level
	preAttrs    map[string]string
	groupPrefix string
}

func newLegacyPrettyHandler(w io.Writer, level slog.Level) slog.Handler {
	return &legacyPrettyHandler{mu: new(sync.Mutex), w: w, level: level, preAttrs: make(map[string]string)}
}

func (h *legacyPrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *legacyPrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make(map[string]string, len(h.preAttrs)+len(attrs))
	maps.Copy(merged, h.preAttrs)
	for _, attr := range attrs {
		merged[h.groupPrefix+attr.Key] = attr.Value.String()
	}
	return &legacyPrettyHandler{mu: h.mu, w: h.w, level: h.level, preAttrs: merged, groupPrefix: h.groupPrefix}
}

func (h *legacyPrettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copied := make(map[string]string, len(h.preAttrs))
	maps.Copy(copied, h.preAttrs)
	return &legacyPrettyHandler{
		mu:          h.mu,
		w:           h.w,
		level:       h.level,
		preAttrs:    copied,
		groupPrefix: h.groupPrefix + name + ".",
	}
}

func (h *legacyPrettyHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]string, len(h.preAttrs)+record.NumAttrs())
	maps.Copy(attrs, h.preAttrs)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[h.groupPrefix+attr.Key] = attr.Value.String()
		return true
	})

	var output strings.Builder
	output.WriteString(ansiDim)
	output.WriteString(record.Time.Format(time.TimeOnly))
	output.WriteString(ansiReset)
	output.WriteString("  ")
	output.WriteString(levelTag(record.Level))
	output.WriteString("  ")
	(&prettyHandler{}).writeRequest(&output, attrs)
	output.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprint(h.w, output.String())
	return err
}
