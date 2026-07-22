package library

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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

	large := image.NewRGBA(image.Rect(0, 0, 2000, 3000))
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
}

func TestEncodeCoverJPEG(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

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

	// Dimension skip: width past maxCoverDimension.
	// Build a minimal PNG with DecodeConfig reporting huge dimensions without
	// allocating a huge pixel buffer — use a crafted IHDR via image encode of
	// a normal image is fine for "too many pixels" path with a large-but-legal
	// image under maxCoverDimension but over maxCoverPixels.
	// maxCoverPixels = 24e6; e.g. 5000x5000 = 25e6 > cap, and 5000 < 10000 dim cap.
	hugePNG := encodePNG(t, 5000, 5000)
	if int64(len(hugePNG)) > maxCoverBytes {
		t.Skip("png encoding of 5000x5000 exceeded byte cap; skip pixel-count case")
	}
	_, err = EncodeCoverJPEG(ctx, "book1", hugePNG)
	if !errors.Is(err, ErrCoverSkipped) {
		t.Fatalf("pixel oversize: err = %v, want ErrCoverSkipped", err)
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
	wantRel := filepath.Join(".sayumi", "covers", "id-abc.jpg")
	if rel != wantRel {
		t.Fatalf("rel = %q, want %q", rel, wantRel)
	}
	abs := filepath.Join(lib, rel)
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

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("OEBPS/cover.png")
	if err != nil {
		t.Fatal(err)
	}
	pngData := encodePNG(t, 10, 10)
	if _, err := w.Write(pngData); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}

	got, err := readCoverData(zr, "OEBPS/cover.png")
	if err != nil || !bytes.Equal(got, pngData) {
		t.Fatalf("readCoverData = %v err=%v", len(got), err)
	}
	// Case-insensitive path match.
	if _, err := readCoverData(zr, "oebps/COVER.PNG"); err != nil {
		t.Fatalf("case fold: %v", err)
	}
	if _, err := readCoverData(zr, "missing.png"); err == nil {
		t.Fatal("missing entry: want error")
	}

	// Declared oversize header rejects before full read.
	var bigBuf bytes.Buffer
	zw2 := zip.NewWriter(&bigBuf)
	hdr := &zip.FileHeader{Name: "big.jpg", Method: zip.Store}
	hdr.UncompressedSize64 = uint64(maxCoverBytes) + 1
	w2, err := zw2.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	// Write a few bytes only; UncompressedSize64 may not match — archive/zip may
	// still open. Prefer actual LimitReader path: write maxCoverBytes+2 of data.
	payload := bytes.Repeat([]byte{0xff}, int(maxCoverBytes)+2)
	if _, err := w2.Write(payload); err != nil {
		t.Fatal(err)
	}
	// Fix header size to match written payload for a valid zip.
	// CreateHeader already set size from write on close for Store... rebuild simply:
	_ = zw2.Close()

	// Rebuild valid zip with large payload exceeding maxCoverBytes.
	var big2 bytes.Buffer
	zw3 := zip.NewWriter(&big2)
	w3, err := zw3.Create("big.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w3.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw3.Close(); err != nil {
		t.Fatal(err)
	}
	zr3, err := zip.NewReader(bytes.NewReader(big2.Bytes()), int64(big2.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readCoverData(zr3, "big.jpg"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large cover err = %v", err)
	}
}

func TestExtractCoverSkipsExisting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

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
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("cover.png")
	_, _ = w.Write(encodePNG(t, 20, 20))
	_ = zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(zbuf.Bytes()), int64(zbuf.Len()))
	if err != nil {
		t.Fatal(err)
	}

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

func TestSaveCoverImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lib := t.TempDir()

	rel, err := SaveCoverImage(ctx, lib, "s1", encodePNG(t, 30, 40))
	if err != nil {
		t.Fatalf("SaveCoverImage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, rel)); err != nil {
		t.Fatalf("saved file: %v", err)
	}
}
