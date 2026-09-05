package storage

import (
	"errors"
	"fmt"
	"testing"
)

func BenchmarkListCustomThemes(b *testing.B) {
	for _, n := range []int{0, 1, 32, 1000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			db := newTestDB(b)
			ctx := b.Context()
			for i := range n {
				rec := sampleTheme(fmt.Sprintf("theme_%04d", i), "reader", "Reading palette")
				rec.CreatedAt = "2026-01-02 03:04:05"
				rec.UpdatedAt = rec.CreatedAt
				if err := db.InsertCustomThemeContext(ctx, rec); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.ListCustomThemesContext(ctx, "reader")
				if err != nil || len(got) != n {
					b.Fatalf("list: count=%d, err=%v", len(got), err)
				}
			}
		})
	}
}

func BenchmarkUpdateCustomTheme(b *testing.B) {
	for _, missing := range []bool{false, true} {
		name := "existing"
		if missing {
			name = "missing"
		}
		b.Run(name, func(b *testing.B) {
			db := newTestDB(b)
			ctx := b.Context()
			rec := sampleTheme("theme_one", "reader", "Day")
			rec.CreatedAt = "2026-01-02 03:04:05"
			if err := db.InsertCustomThemeContext(ctx, rec); err != nil {
				b.Fatal(err)
			}
			if missing {
				rec.ID = "theme_missing"
			}
			b.ReportAllocs()
			for b.Loop() {
				if rec.Name == "Day" {
					rec.Name = "Night"
				} else {
					rec.Name = "Day"
				}
				got, err := db.UpdateCustomThemeContext(ctx, rec)
				if missing {
					if !errors.Is(err, ErrNotFound) {
						b.Fatalf("missing update: %v", err)
					}
				} else if err != nil || got.Name != rec.Name || got.CreatedAt != rec.CreatedAt {
					b.Fatalf("update: record=%+v, err=%v", got, err)
				}
			}
		})
	}
}
