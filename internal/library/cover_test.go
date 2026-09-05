package library

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

func TestResizeToFit(t *testing.T) {
	t.Parallel()

	small := image.NewRGBA(image.Rect(0, 0, 100, 150))
	if got := resizeToFit(small, maxCoverWidth, maxCoverHeight); got != small {
		t.Fatal("small image should be returned unchanged")
	}

	large := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	got := resizeToFit(large, maxCoverWidth, maxCoverHeight)
	b := got.Bounds()
	if b.Dx() > maxCoverWidth || b.Dy() > maxCoverHeight {
		t.Fatalf("resized %dx%d exceeds %dx%d", b.Dx(), b.Dy(), maxCoverWidth, maxCoverHeight)
	}
	if b.Dx() < 1 || b.Dy() < 1 {
		t.Fatalf("degenerate size %dx%d", b.Dx(), b.Dy())
	}
	// Aspect roughly preserved (2:3).
	ratio := float64(b.Dx()) / float64(b.Dy())
	if ratio < 0.6 || ratio > 0.75 {
		t.Fatalf("aspect ratio %v unexpected for 2:3 source", ratio)
	}

	if got := resizeToFit(large, 200, 300).Bounds(); got.Dx() != 200 || got.Dy() != 300 {
		t.Fatalf("custom bounds = %v, want 200x300", got)
	}
}

func TestEncodeCoverJPEG(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	pngData := encodePNG(t, 80, 120)
	out, err := EncodeCoverJPEG(ctx, "book1", pngData)
	if err != nil {
		t.Fatalf("EncodeCoverJPEG png: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty jpeg output")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if cfg.Width > maxCoverWidth || cfg.Height > maxCoverHeight {
		t.Fatalf("output dims %dx%d", cfg.Width, cfg.Height)
	}

	// Already-JPEG small source also works.
	if _, err := EncodeCoverJPEG(ctx, "book1", encodeJPEGBytes(t, 50, 50)); err != nil {
		t.Fatalf("EncodeCoverJPEG jpeg: %v", err)
	}

	// Byte-size cap.
	oversize := make([]byte, maxCoverBytes+1)
	if _, err := EncodeCoverJPEG(ctx, "book1", oversize); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("byte oversize err = %v", err)
	}
}

func TestWriteCoverImageJPEG(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	jpegData := encodeJPEGBytes(t, 40, 60)

	rel, err := WriteCoverImageJPEG(lib, "id-abc", jpegData)
	if err != nil {
		t.Fatalf("WriteCoverImageJPEG: %v", err)
	}
	// Slash-form, not filepath.Join: this value is persisted in books.cover_path
	// and the library folder is portable, so an OS-native separator written on
	// Windows would be a single literal filename once the folder is opened on
	// macOS or Linux.
	wantRel := ".sayumi/covers/id-abc.jpg"
	if rel != wantRel {
		t.Fatalf("rel = %q, want %q", rel, wantRel)
	}
	abs := filepath.Join(lib, filepath.FromSlash(rel))
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Equal(got, jpegData) {
		t.Fatalf("cover bytes mismatch (len %d vs %d)", len(got), len(jpegData))
	}

	// Overwrite.
	next := encodeJPEGBytes(t, 20, 20)
	if _, err := WriteCoverImageJPEG(lib, "id-abc", next); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got2, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read after overwrite: %v", err)
	}
	if !bytes.Equal(got2, next) {
		t.Fatal("overwrite did not replace bytes")
	}
}

func TestReadCoverData(t *testing.T) {
	t.Parallel()

	pngData := encodePNG(t, 10, 10)
	zr := coverZIP(t, "OEBPS/cover.png", pngData)
	for _, path := range []string{"OEBPS/cover.png", "oebps/COVER.PNG", "/OEBPS/cover.png"} {
		got, err := readCoverData(zr, path)
		if err != nil || !bytes.Equal(got, pngData) {
			t.Fatalf("readCoverData(%q) = %d bytes, %v", path, len(got), err)
		}
	}
	if _, err := readCoverData(zr, "missing.png"); err == nil {
		t.Fatal("missing entry: want error")
	}

	// Reject the declared size before opening the entry, without allocating
	// a multi-megabyte payload just to exercise this header check.
	zr.File[0].UncompressedSize64 = uint64(maxCoverBytes) + 1
	if got, err := readCoverData(zr, "OEBPS/cover.png"); got != nil || err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large cover = %d bytes, %v", len(got), err)
	}
}

func TestExtractCoverSkipsExisting(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	lib := t.TempDir()
	covers := filepath.Join(lib, ".sayumi", "covers")
	if err := os.MkdirAll(covers, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte("EXISTING-JPEG-BYTES")
	if err := os.WriteFile(filepath.Join(covers, "bk1.jpg"), existing, 0o644); err != nil {
		t.Fatal(err)
	}

	// Zip with a real cover that would overwrite if extract ran.
	zr := coverZIP(t, "cover.png", encodePNG(t, 20, 20))

	if err := extractCover(ctx, lib, "bk1", zr, "cover.png"); err != nil {
		t.Fatalf("extractCover existing: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(covers, "bk1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatal("extractCover overwrote existing cover")
	}
}

func TestEncodeAndWriteCoverImage(t *testing.T) {
	t.Parallel()
	lib := t.TempDir()

	data, err := EncodeCoverJPEG(t.Context(), "s1", encodePNG(t, 30, 40))
	if err != nil {
		t.Fatalf("encode cover: %v", err)
	}
	rel, err := WriteCoverImageJPEG(lib, "s1", data)
	if err != nil {
		t.Fatalf("write cover: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(lib, filepath.FromSlash(rel)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved cover differs: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 30 || cfg.Height != 40 {
		t.Fatalf("saved JPEG = %+v, %v", cfg, err)
	}
}
