package storage

import (
	"database/sql"
	"fmt"
	"testing"
)

// Include empty and single-row results so destination reuse is not optimized
// only for populated libraries. All database setup stays outside b.Loop.
func BenchmarkReadingStateScans(b *testing.B) {
	for _, operation := range []string{"progress", "bookmarks"} {
		for _, size := range []int{0, 1, 32, 1000} {
			b.Run(fmt.Sprintf("%s/%d", operation, size), func(b *testing.B) {
				db := newTestDB(b)
				ctx := b.Context()
				if operation == "progress" {
					seedBooks(b, db, size)
				} else {
					mustInsertBook(b, db, sampleBook("book", "hash", "/lib/book.epub"))
				}
				for i := range size {
					cfi := sql.NullString{String: "epubcfi(/6/2)", Valid: i%2 == 0}
					if operation == "progress" {
						if err := db.SaveProgressContext(ctx, ProgressRecord{
							BookID: fmt.Sprintf("id%04d", i), UserID: "reader",
							Chapter: i % 30, Percent: float64(i%100) / 100, CFI: cfi,
						}); err != nil {
							b.Fatal(err)
						}
					} else {
						if err := db.InsertBookmarkContext(ctx, BookmarkRecord{
							ID: fmt.Sprintf("mark%04d", i), BookID: "book", UserID: "reader",
							Chapter: i % 30, Percent: float64(i%100) / 100, CFI: cfi,
							Label: "Chapter marker", Comment: "Reader note",
						}); err != nil {
							b.Fatal(err)
						}
					}
				}
				b.ReportAllocs()
				if operation == "progress" {
					for b.Loop() {
						if got, err := db.GetAllProgressContext(ctx, "reader"); err != nil || len(got) != size {
							b.Fatalf("progress: rows=%d err=%v", len(got), err)
						}
					}
				} else {
					for b.Loop() {
						if got, err := db.ListBookmarksContext(ctx, "book", "reader"); err != nil || len(got) != size {
							b.Fatalf("bookmarks: rows=%d err=%v", len(got), err)
						}
					}
				}
			})
		}
	}
}
