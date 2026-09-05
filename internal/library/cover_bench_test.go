package library

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkEncodeCoverJPEG(b *testing.B) {
	for _, tc := range []struct {
		name   string
		format string
		width  int
		height int
	}{
		{"small_png", "png", 80, 120},
		{"large_jpeg", "jpeg", 1200, 1800},
	} {
		b.Run(tc.name, func(b *testing.B) {
			data := benchmarkCoverData(b, tc.format, tc.width, tc.height)
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				got, err := EncodeCoverJPEG(ctx, "benchmark", data)
				if err != nil || len(got) == 0 {
					b.Fatalf("encode cover: %v (bytes %d)", err, len(got))
				}
			}
		})
	}
}

func BenchmarkWriteCoverImageJPEG(b *testing.B) {
	data := benchmarkCoverData(b, "jpeg", 400, 600)
	lib := b.TempDir()
	b.ReportAllocs()
	for b.Loop() {
		rel, err := WriteCoverImageJPEG(lib, "benchmark", data)
		if err != nil || rel != ".sayumi/covers/benchmark.jpg" {
			b.Fatalf("write cover = %q, %v", rel, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(lib, filepath.FromSlash(CoverRelPath("benchmark"))))
	if err != nil || !bytes.Equal(got, data) {
		b.Fatalf("stored cover differs: %v", err)
	}
}

func BenchmarkResizeToFit(b *testing.B) {
	for _, kind := range []string{"small", "rgba", "nrgba", "ycbcr", "gray"} {
		b.Run(kind, func(b *testing.B) {
			width, height := 1200, 1800
			wantWidth, wantHeight := maxCoverWidth, maxCoverHeight
			if kind == "small" {
				width, height = 80, 120
				wantWidth, wantHeight = width, height
			}
			img := benchmarkCoverImage(kind, width, height)
			b.ReportAllocs()
			for b.Loop() {
				got := resizeToFit(img, maxCoverWidth, maxCoverHeight)
				if bounds := got.Bounds(); bounds.Dx() != wantWidth || bounds.Dy() != wantHeight {
					b.Fatalf("bounds = %v, want %dx%d", bounds, wantWidth, wantHeight)
				}
			}
		})
	}
}

func benchmarkCoverData(b *testing.B, format string, width, height int) []byte {
	b.Helper()
	img := benchmarkCoverImage("rgba", width, height)
	var buf bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		b.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

func benchmarkCoverImage(kind string, width, height int) image.Image {
	bounds := image.Rect(0, 0, width, height)
	switch kind {
	case "nrgba":
		img := image.NewNRGBA(bounds)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(i/4), 90, 170, byte(i/16)
		}
		return img
	case "ycbcr":
		img := image.NewYCbCr(bounds, image.YCbCrSubsampleRatio420)
		for i := range img.Y {
			img.Y[i] = byte(i)
		}
		for i := range img.Cb {
			img.Cb[i], img.Cr[i] = 100, 150
		}
		return img
	case "gray":
		img := image.NewGray(bounds)
		for i := range img.Pix {
			img.Pix[i] = byte(i)
		}
		return img
	default:
		img := image.NewRGBA(bounds)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(i/4), 90, 170, 255
		}
		return img
	}
}
