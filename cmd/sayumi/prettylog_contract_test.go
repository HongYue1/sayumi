package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type logValueFunc func() slog.Value

func (f logValueFunc) LogValue() slog.Value { return f() }

func plainLogText(text string) string {
	return strings.NewReplacer(
		ansiReset, "", ansiBold, "", ansiDim, "", ansiRed, "",
		ansiGreen, "", ansiYellow, "", ansiCyan, "",
	).Replace(text)
}

func TestPrettyHandlerAttributeContract(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	calls := 0
	value := logValueFunc(func() slog.Value {
		calls++
		return slog.GroupValue(slog.String("name", "Raven\nreader"))
	})
	handler := newPrettyHandler(&output, slog.LevelDebug).
		WithAttrs([]slog.Attr{slog.Any("profile", value), {}, slog.Group("empty")}).
		WithGroup("request")
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "request", 0)
	record.AddAttrs(slog.Group("", slog.Int("id", 7)), slog.Group("nested", slog.String("state", "ready")))
	for range 2 {
		if err := handler.Handle(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	got := plainLogText(output.String())
	for _, want := range []string{`profile.name=Raven\nreader`, "request.id=7", "request.nested.state=ready"} {
		if strings.Count(got, want) != 2 {
			t.Errorf("output %q should contain %q twice", got, want)
		}
	}
	for _, unwanted := range []string{"00:00:00", "empty=", "<nil>", "request.profile"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output contains %q: %q", unwanted, got)
		}
	}
	if calls != 1 {
		t.Errorf("WithAttrs LogValue called %d times, want once", calls)
	}
	if strings.Count(got, "\n") != 2 {
		t.Errorf("expected two physical lines, got %q", got)
	}
}

func TestPrettyHandlerResolvesRecordValues(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	handler := newPrettyHandler(&output, slog.LevelDebug)
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "event", 0)
	record.AddAttrs(slog.Any("value", logValueFunc(func() slog.Value {
		return slog.StringValue("safe\n\x1b[2J")
	})))
	if err := handler.Handle(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if got := plainLogText(output.String()); !strings.Contains(got, `value=safe\n\x1b[2J`) {
		t.Fatalf("lazy value was not resolved and escaped: %q", got)
	}
}

func TestPrettyHandlerRequestContextAndControls(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"method", "path", "status", "duration", "size"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			handler := newPrettyHandler(&output, slog.LevelDebug).
				WithAttrs([]slog.Attr{slog.String("profile", "Raven")})
			record := benchmarkRequestRecord()
			record.AddAttrs(slog.String(field, "value\n\x1b[2J"))
			if err := handler.Handle(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			got := plainLogText(output.String())
			if strings.Count(got, "\n") != 1 || strings.Contains(got, "\x1b[2J") {
				t.Errorf("request field injected controls: %q", got)
			}
			if !strings.Contains(got, `value\n\x1b[2J`) || !strings.Contains(got, "profile=Raven") {
				t.Errorf("request field or context was lost: %q", got)
			}
		})
	}
}

func TestPrettyHandlerDerivedHandlersAreIndependent(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	base := newPrettyHandler(&output, slog.LevelDebug).WithAttrs([]slog.Attr{slog.String("profile", "Raven")})
	left := base.WithGroup("left").WithAttrs([]slog.Attr{slog.Int("id", 1)})
	right := base.WithGroup("right").WithAttrs([]slog.Attr{slog.Int("id", 2)})
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "event", 0)
	var wg sync.WaitGroup
	for _, handler := range []slog.Handler{base, left, right} {
		wg.Go(func() {
			for range 20 {
				if err := handler.Handle(t.Context(), record); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(plainLogText(output.String())), "\n")
	if len(lines) != 60 {
		t.Fatalf("got %d lines, want 60", len(lines))
	}
	var baseLines, leftLines, rightLines int
	for _, line := range lines {
		if !strings.Contains(line, "profile=Raven") || strings.Contains(line, "left.profile") || strings.Contains(line, "right.profile") {
			t.Errorf("base attribute changed: %q", line)
		}
		hasLeft, hasRight := strings.Contains(line, "left.id=1"), strings.Contains(line, "right.id=2")
		switch {
		case hasLeft && hasRight:
			t.Errorf("sibling attributes leaked: %q", line)
		case hasLeft:
			leftLines++
		case hasRight:
			rightLines++
		default:
			baseLines++
		}
	}
	if baseLines != 20 || leftLines != 20 || rightLines != 20 {
		t.Errorf("line counts = %d/%d/%d, want 20/20/20", baseLines, leftLines, rightLines)
	}
}

type failingLogWriter struct{ err error }

func (w failingLogWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPrettyHandlerReturnsWriteError(t *testing.T) {
	t.Parallel()
	want := errors.New("write failed")
	handler := newPrettyHandler(failingLogWriter{want}, slog.LevelInfo)
	got := handler.Handle(t.Context(), slog.NewRecord(time.Time{}, slog.LevelInfo, "event", 0))
	if !errors.Is(got, want) {
		t.Fatalf("Handle error = %v, want %v", got, want)
	}
}
