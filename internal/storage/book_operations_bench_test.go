package storage

import (
	"context"
	"fmt"
	"testing"

	"sayumi/internal/epub"
)

// Each mutation keeps the cache size fixed. Setup and fixture formatting stay
// outside b.Loop so comparisons measure cache work, not database population.
func BenchmarkBookCacheMutations(b *testing.B) {
	for _, size := range []int{32, 2000} {
		for _, operation := range []string{"retitle", "refresh", "remove_add", "remove_missing"} {
			b.Run(fmt.Sprintf("%s/%d", operation, size), func(b *testing.B) {
				books := make([]BookRecord, size)
				for i := range books {
					id := fmt.Sprintf("id%04d", i)
					books[i] = sampleBook(id, id, "/lib/"+id+".epub")
				}
				cache := newMemoryBookCache(books...)
				book := books[size/2]
				b.ReportAllocs()
				switch operation {
				case "retitle":
					first := false
					for b.Loop() {
						if first {
							book.Title = "Aaa Retitled"
						} else {
							book.Title = "Zzz Retitled"
						}
						cache.Add(book)
						first = !first
					}
				case "refresh":
					for b.Loop() {
						book.FileSize++
						cache.Add(book)
					}
				case "remove_add":
					for b.Loop() {
						cache.Remove(book.ID)
						cache.Add(book)
					}
				case "remove_missing":
					for b.Loop() {
						cache.Remove("missing")
					}
				}
				if cache.Len() != size {
					b.Fatalf("cache length = %d, want %d", cache.Len(), size)
				}
			})
		}
	}
}

func BenchmarkBookCacheSpineMemo(b *testing.B) {
	for _, operation := range []string{"hot_get", "late_load"} {
		b.Run(operation, func(b *testing.B) {
			book := sampleBook("book", "hash", "/lib/book.epub")
			book.SpineJSON = ""
			cache := newMemoryBookCache(book)
			cache.spines[book.ID] = []epub.SpineEntry{{Href: "cached.xhtml"}}
			cache.loadBookContent = func(context.Context, string) (string, string, error) {
				return `[{"href":"cached.xhtml"}]`, "[]", nil
			}
			ctx := b.Context()
			generation := cache.generations[book.ID]
			b.ReportAllocs()
			if operation == "hot_get" {
				for b.Loop() {
					if _, found, err := cache.GetSpine(ctx, book.ID); err != nil || !found {
						b.Fatalf("GetSpine: found=%v err=%v", found, err)
					}
				}
			} else {
				// Isolate a delayed leader that enters after a previous flight
				// has populated the memo. This is not the ordinary hot path.
				for b.Loop() {
					if got, err := cache.loadSpine(ctx, book.ID, book, generation); err != nil || !got.found {
						b.Fatalf("loadSpine: found=%v err=%v", got.found, err)
					}
				}
			}
		})
	}
}

func BenchmarkBookPathScans(b *testing.B) {
	for _, size := range []int{0, 1, 1000} {
		for _, operation := range []string{"paths", "missing_covers", "ignored"} {
			b.Run(fmt.Sprintf("%s/%d", operation, size), func(b *testing.B) {
				db := newTestDB(b)
				seedBooks(b, db, size)
				ctx := b.Context()
				if operation == "ignored" {
					for i := range size {
						if err := db.DeleteBookContext(ctx, fmt.Sprintf("id%04d", i)); err != nil {
							b.Fatal(err)
						}
					}
				}
				b.ReportAllocs()
				switch operation {
				case "paths":
					for b.Loop() {
						if got, err := db.ListBookPathsContext(ctx); err != nil || len(got) != size {
							b.Fatalf("book paths: rows=%d err=%v", len(got), err)
						}
					}
				case "missing_covers":
					for b.Loop() {
						if got, err := db.ListBooksMissingCoversContext(ctx); err != nil || len(got) != size {
							b.Fatalf("missing covers: rows=%d err=%v", len(got), err)
						}
					}
				case "ignored":
					for b.Loop() {
						if got, err := db.ListIgnoredPathsContext(ctx); err != nil || len(got) != size {
							b.Fatalf("ignored paths: rows=%d err=%v", len(got), err)
						}
					}
				}
			})
		}
	}
}
