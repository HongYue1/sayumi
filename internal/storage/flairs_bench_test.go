package storage

import (
	"fmt"
	"testing"
)

func BenchmarkListFlairs(b *testing.B) {
	for _, n := range []int{0, 1, 32, 1000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			db := newTestDB(b)
			ctx := b.Context()
			for i := range n {
				if err := db.InsertFlairContext(ctx, FlairRecord{
					ID: fmt.Sprintf("flair_%04d", i), UserID: "reader",
					Label: "Favorite", Color: "#2563eb", CreatedAt: "2026-01-02 03:04:05",
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.ListFlairsContext(ctx, "reader")
				if err != nil || len(got) != n {
					b.Fatalf("list: count=%d, err=%v", len(got), err)
				}
			}
		})
	}
}

func BenchmarkGetAllBookFlairs(b *testing.B) {
	for _, n := range []int{0, 1, 32, 1000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			db := newTestDB(b)
			ctx := b.Context()
			seedBooks(b, db, n)
			for i := range n {
				flairID := "reading"
				if i%2 != 0 {
					flairID = "finished"
				}
				if err := db.SetBookFlairCheckedContext(ctx, fmt.Sprintf("id%04d", i), "reader", flairID, testBuiltinFlairs); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.GetAllBookFlairsContext(ctx, "reader")
				if err != nil || len(got) != n {
					b.Fatalf("list: count=%d, err=%v", len(got), err)
				}
			}
		})
	}
}
