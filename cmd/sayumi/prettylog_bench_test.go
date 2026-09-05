package main

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// The base attributes represent a logger derived once per profile; WithGroup
// then adds request context without changing those already-qualified keys.
func BenchmarkPrettyHandlerWithGroup(b *testing.B) {
	for _, count := range []int{0, 8} {
		name := "empty"
		if count > 0 {
			name = "with_attrs"
		}
		b.Run(name, func(b *testing.B) {
			attrs := make([]slog.Attr, count)
			for i := range attrs {
				attrs[i] = slog.Int(string(rune('a'+i)), i)
			}
			handler := newPrettyHandler(io.Discard, slog.LevelDebug).WithAttrs(attrs)
			b.ReportAllocs()
			for b.Loop() {
				_ = handler.WithGroup("request")
			}
		})
	}
}

func BenchmarkPrettyHandlerGeneric(b *testing.B) {
	handler := newPrettyHandler(io.Discard, slog.LevelDebug).WithAttrs([]slog.Attr{
		slog.String("profile", "Raven"),
	})
	record := slog.NewRecord(time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC), slog.LevelInfo, "scanning library", 0)
	record.AddAttrs(slog.Int("books", 42), slog.String("path", "/library/Raven"))
	b.ReportAllocs()
	for b.Loop() {
		if err := handler.Handle(b.Context(), record); err != nil {
			b.Fatal(err)
		}
	}
}
