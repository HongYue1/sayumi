package storage

import (
	"fmt"
	"testing"
	"time"
)

// Measure complete SQLite reads with realistic small sets and larger libraries.
// Database creation and fixture writes are outside the b.Loop measurement.
func BenchmarkStorageScans(b *testing.B) {
	for _, size := range []int{8, 128} {
		b.Run(fmt.Sprintf("profiles/%d", size), func(b *testing.B) {
			pdb := newTestProfilesDB(b)
			ctx := b.Context()
			for i := range size {
				if err := pdb.CreateProfileContext(ctx, fmt.Sprintf("reader-%03d", i), "opaque-pin-hash"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := pdb.ListProfilesContext(ctx)
				if err != nil || len(got) != size {
					b.Fatalf("list profiles: count=%d, err=%v", len(got), err)
				}
			}
		})
		b.Run(fmt.Sprintf("sessions/%d", size), func(b *testing.B) {
			pdb := newTestProfilesDB(b)
			ctx := b.Context()
			if err := pdb.CreateProfileContext(ctx, "reader", ""); err != nil {
				b.Fatal(err)
			}
			expiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range size {
				if err := pdb.SaveSession(ctx, fmt.Sprintf("token-%03d", i), "reader", expiry.Add(time.Duration(i)*time.Second)); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := pdb.LoadSessions(ctx)
				if err != nil || len(got) != size {
					b.Fatalf("load sessions: count=%d, err=%v", len(got), err)
				}
			}
		})
	}

	for _, size := range []int{32, 1000} {
		b.Run(fmt.Sprintf("path_keys/%d", size), func(b *testing.B) {
			db := newTestDB(b)
			for i := range size {
				id := fmt.Sprintf("book-%04d", i)
				mustInsertBook(b, db, sampleBook(id, id, "/library/Books/"+id+".epub"))
			}
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.pendingRekeys("SELECT file_path, file_path_key FROM books")
				if err != nil || len(got) != 0 {
					b.Fatalf("read converged keys: count=%d, err=%v", len(got), err)
				}
			}
		})
	}

	for _, table := range []string{"books", "settings"} {
		b.Run("columns/"+table, func(b *testing.B) {
			db := newTestDB(b)
			b.ReportAllocs()
			for b.Loop() {
				got, err := db.tableColumns(table)
				if err != nil || len(got) == 0 {
					b.Fatalf("read columns: count=%d, err=%v", len(got), err)
				}
			}
		})
	}
}
