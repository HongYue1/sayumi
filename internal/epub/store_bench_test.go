package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkStoreOpenIndexed(b *testing.B) {
	for _, tt := range []struct {
		name    string
		entries int
		cold    bool
	}{
		{name: "Hit128", entries: 128},
		{name: "Cold128", entries: 128, cold: true},
		{name: "Cold2048", entries: 2048, cold: true},
	} {
		b.Run(tt.name, func(b *testing.B) {
			filePath := storeBenchmarkArchive(b, tt.entries, zip.Store, 64)
			store := NewStore(2)
			b.Cleanup(store.Close)
			reader, index, err := store.OpenIndexed(filePath)
			if err != nil || reader == nil || index["OPS/item0000.bin"] == nil {
				b.Fatalf("warm archive: reader=%v, err=%v", reader != nil, err)
			}
			store.Release(filePath)
			if tt.cold {
				store.CloseBook(filePath)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := store.OpenIndexed(filePath); err != nil {
					b.Fatal(err)
				}
				store.Release(filePath)
				if tt.cold {
					store.CloseBook(filePath)
				}
			}
		})
	}
}

func BenchmarkStoreOpenIndexedParallel(b *testing.B) {
	filePath := storeBenchmarkArchive(b, 128, zip.Store, 64)
	store := NewStore(2)
	b.Cleanup(store.Close)
	if _, _, err := store.OpenIndexed(filePath); err != nil {
		b.Fatal(err)
	}
	store.Release(filePath)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := store.OpenIndexed(filePath); err != nil {
				b.Error(err)
				return
			}
			store.Release(filePath)
		}
	})
}

func BenchmarkStoreOpenResource(b *testing.B) {
	for _, tt := range []struct {
		name   string
		method uint16
		size   int
	}{
		{name: "Store4KiB", method: zip.Store, size: 4 << 10},
		{name: "Deflate64KiB", method: zip.Deflate, size: 64 << 10},
	} {
		b.Run(tt.name, func(b *testing.B) {
			filePath := storeBenchmarkArchive(b, 1, tt.method, tt.size)
			store := NewStore(2)
			b.Cleanup(store.Close)
			consume := func() {
				reader, err := store.OpenResource(filePath, "OPS/item0000.bin")
				if err != nil {
					b.Fatal(err)
				}
				n, readErr := io.Copy(io.Discard, reader)
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil || n != int64(tt.size) {
					b.Fatalf("stream: bytes=%d, read=%v, close=%v", n, readErr, closeErr)
				}
			}
			consume() // Warm the store, decompressor, and copy buffer outside timing.
			b.ReportAllocs()
			b.SetBytes(int64(tt.size))
			for b.Loop() {
				consume()
			}
		})
	}
}

func storeBenchmarkArchive(b *testing.B, entries int, method uint16, size int) string {
	b.Helper()
	filePath := filepath.Join(b.TempDir(), "book.epub")
	file, err := os.Create(filePath)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			b.Errorf("close fixture file: %v", err)
		}
	}()
	writer := zip.NewWriter(file)
	body := strings.Repeat("resource bytes\n", size/15+1)[:size]
	for i := range entries {
		entry, err := writer.CreateHeader(&zip.FileHeader{
			Name:   fmt.Sprintf("OPS/item%04d.bin", i),
			Method: method,
		})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.WriteString(entry, body); err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	return filePath
}
