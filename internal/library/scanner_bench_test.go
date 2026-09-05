package library

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkContentHash(b *testing.B) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"4KiB", 4 << 10},
		{"1MiB", 1 << 20},
		{"16MiB", 16 << 20},
	} {
		b.Run(tc.name, func(b *testing.B) {
			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte(i*17 + i/257)
			}
			path := filepath.Join(b.TempDir(), "book.epub")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				b.Fatal(err)
			}
			digest := sha256.Sum256(data)
			want := hex.EncodeToString(digest[:])
			ctx := b.Context()
			// Measure steady-state hashing, not a cold 1 MiB pooled-buffer allocation.
			if got, size, err := contentHash(ctx, path); err != nil || got != want || size != int64(tc.size) {
				b.Fatalf("warm hash = %q, %d, %v", got, size, err)
			}
			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			for b.Loop() {
				got, size, err := contentHash(ctx, path)
				if err != nil || got != want || size != int64(tc.size) {
					b.Fatalf("hash = %q, %d, %v; want %q, %d", got, size, err, want, tc.size)
				}
			}
		})
	}
}

func BenchmarkGenerateID(b *testing.B) {
	const hash = "70d74cc90da268818b45a3d929e4a00c2d7a4c11dd715fb930d8dd778e557dcb"
	for _, tc := range []struct {
		name string
		path string
	}{
		{"short", "/lib/book.epub"},
		{"library_path", `C:\Users\Reader\Documents\Library\日本語\A Book With A Long Title.epub`},
		{"long_path", "/library/" + strings.Repeat("nested/", 40) + "book.epub"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			digest := sha256.Sum256([]byte(tc.path + hash))
			want := hex.EncodeToString(digest[:8])
			b.ReportAllocs()
			for b.Loop() {
				if got := generateID(tc.path, hash); got != want {
					b.Fatalf("ID = %q, want %q", got, want)
				}
			}
		})
	}
}
