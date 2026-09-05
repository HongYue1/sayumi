package storage

import (
	"fmt"
	"testing"
)

func BenchmarkListPresets(b *testing.B) {
	for _, n := range []int{0, 1, 32, 1000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			db := newTestDB(b)
			ctx := b.Context()
			const settingsJSON = `{"fontSize":18,"fontFamily":"Literata","theme":"rose-pine","lineHeight":1.6,"preserveStyles":false,"fontRoles":{}}`
			for i := range n {
				rec := samplePreset(fmt.Sprintf("preset_%04d", i), "reader", "Evening reading", settingsJSON)
				rec.CreatedAt = "2026-01-02 03:04:05"
				rec.UpdatedAt = rec.CreatedAt
				if err := db.InsertPresetContext(ctx, rec); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.ListPresetsContext(ctx, "reader")
				if err != nil || len(got) != n {
					b.Fatalf("list: count=%d, err=%v", len(got), err)
				}
			}
		})
	}
}
