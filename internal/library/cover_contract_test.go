package library

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	xdraw "golang.org/x/image/draw"
)

func coverZIP(t *testing.T, name string, data []byte) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writer, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func pngWithDimensions(t *testing.T, width, height int) []byte {
	t.Helper()
	data := encodePNG(t, 1, 1)
	// A CRC-correct IHDR tests the pre-decode limits without allocating the
	// advertised pixels. The tiny IDAT deliberately cannot decode at this size.
	binary.BigEndian.PutUint32(data[16:20], uint32(width))
	binary.BigEndian.PutUint32(data[20:24], uint32(height))
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != width || cfg.Height != height {
		t.Fatalf("invalid header fixture: %+v, %v", cfg, err)
	}
	return data
}

func TestCoverDimensionLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{"width", maxCoverDimension + 1, 1},
		{"height", 1, maxCoverDimension + 1},
		{"pixels", 5000, 5000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := pngWithDimensions(t, tc.width, tc.height)
			if got, err := EncodeCoverJPEG(t.Context(), "oversized", data); got != nil || !errors.Is(err, ErrCoverSkipped) {
				t.Fatalf("oversized cover = %d bytes, %v", len(got), err)
			}
		})
	}
}

// Context permits Done to close asynchronously after cancellation. Keeping its
// notification pending makes the pre-canceled check deterministic: only Err
// can stop the operation before a free semaphore slot is acquired.
type delayedDoneContext struct {
	context.Context
	done <-chan struct{}
}

func (c delayedDoneContext) Done() <-chan struct{} { return c.done }

func TestCoverAlreadyCanceled(t *testing.T) {
	// Serial: no other test may occupy coverDecodeSem during the slot checks.
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	defer close(done)
	ctx := delayedDoneContext{Context: parent, done: done}
	data := encodePNG(t, 12, 18)
	zr := coverZIP(t, "cover.png", data)
	if got, err := EncodeCoverJPEG(ctx, "canceled", data); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encoding = %d bytes, %v", len(got), err)
	}
	if got, err := decodeAndResizeCover(ctx, "canceled", zr, "cover.png"); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decoding = %v, %v", got, err)
	}
	lib := t.TempDir()
	if err := extractCover(ctx, lib, "canceled", zr, "cover.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extraction = %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".sayumi")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled extraction touched the filesystem: %v", err)
	}
	if len(coverDecodeSem) != 0 {
		t.Fatal("canceled operation leaked a decode slot")
	}
}

func TestCoverCancellationDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
	zr.RegisterDecompressor(zip.Store, func(reader io.Reader) io.ReadCloser {
		cancel()
		return io.NopCloser(reader)
	})
	if got, err := decodeAndResizeCover(ctx, "canceled", zr, "cover.png"); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v, %v", got, err)
	}
	if len(coverDecodeSem) != 0 {
		t.Fatal("canceled read leaked a decode slot")
	}
}

func TestCoverDecodeFailureReleasesSlot(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("not an image"),
		pngWithDimensions(t, 2, 2), // Valid config, truncated image pixels.
		pngWithDimensions(t, maxCoverDimension+1, 1),
	} {
		for range maxConcurrentCoverDecodes + 1 {
			if got, err := EncodeCoverJPEG(t.Context(), "invalid", data); got != nil || err == nil {
				t.Fatalf("invalid image = %v, %v", got, err)
			}
			if len(coverDecodeSem) != 0 {
				t.Fatal("failed decoding leaked a slot")
			}
		}
	}
}

// These slot tests are serial: other tests must not occupy the global semaphore.
func TestCoverCancellationAtStageBoundaries(t *testing.T) {
	data := encodePNG(t, 12, 18)
	for _, tc := range []struct {
		name  string
		check int32
	}{
		{"before_slot", 2},
		{"after_slot", 3},
		{"before_config", 4},
		{"after_config", 5},
		{"after_decode", 6},
		{"after_resize", 7},
		{"after_encode", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			// The Err hook cancels at a stage boundary, not after a wall-clock
			// delay that may miss the operation on a fast machine.
			ctx := &checkHookContext{Context: parent, at: tc.check, action: cancel}
			got, err := EncodeCoverJPEG(ctx, "canceled", data)
			if got != nil || !errors.Is(err, context.Canceled) || ctx.checks.Load() < tc.check {
				t.Fatalf("stage cancellation = %d bytes, %v (checks %d)", len(got), err, ctx.checks.Load())
			}
			if len(coverDecodeSem) != 0 {
				t.Fatal("stage cancellation leaked a decode slot")
			}
		})
	}
}

func TestCoverCancellationWhileWaitingForSlot(t *testing.T) {
	for range maxConcurrentCoverDecodes {
		coverDecodeSem <- struct{}{}
	}
	defer func() {
		for len(coverDecodeSem) > 0 {
			<-coverDecodeSem
		}
	}()
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanWaitContext{Context: parent, ready: make(chan struct{})}
	data := encodePNG(t, 12, 18)
	resultCh := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	wg.Go(func() {
		_, err := EncodeCoverJPEG(ctx, "waiting", data)
		resultCh <- err
	})
	select {
	case <-ctx.ready:
	case err := <-resultCh:
		t.Fatalf("encoding did not wait for a slot: %v", err)
	}
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting encoder = %v, want cancellation", err)
	}
	if len(coverDecodeSem) != maxConcurrentCoverDecodes {
		t.Fatal("waiting encoder consumed another caller's slot")
	}
}

func TestExtractCoverCancellationBeforePublication(t *testing.T) {
	lib := t.TempDir()
	zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	// extractCover's eighth check follows the temp JPEG's close and precedes
	// final publication. Cancellation must discard that temp, not publish it.
	ctx := &checkHookContext{Context: parent, at: 8, action: cancel}
	if err := extractCover(ctx, lib, "canceled", zr, "cover.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(lib, ".sayumi", "covers"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled extraction left files: %v, %v", entries, err)
	}
	if len(coverDecodeSem) != 0 {
		t.Fatal("canceled extraction leaked a decode slot")
	}
}

func TestExtractCoverRejectsNonRegularTarget(t *testing.T) {
	t.Parallel()
	for _, timing := range []string{"before_decode", "during_decode"} {
		t.Run(timing, func(t *testing.T) {
			lib := t.TempDir()
			target := filepath.Join(lib, ".sayumi", "covers", "book.jpg")
			zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
			var createErr error
			if timing == "before_decode" {
				createErr = os.MkdirAll(target, 0o755)
			} else {
				zr.RegisterDecompressor(zip.Store, func(reader io.Reader) io.ReadCloser {
					createErr = os.Mkdir(target, 0o755)
					return io.NopCloser(reader)
				})
			}
			err := extractCover(t.Context(), lib, "book", zr, "cover.png")
			if createErr != nil {
				t.Fatal(createErr)
			}
			if _, ok := errors.AsType[*os.PathError](err); !ok {
				t.Fatalf("non-regular target = %v, want retryable PathError", err)
			}
			assertOnlyCoverFile(t, lib, "book.jpg")
		})
	}
}

func TestReadCoverDataClosesEntryOnError(t *testing.T) {
	t.Parallel()
	want := errors.New("entry close failed")
	zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
	var closed bool
	zr.RegisterDecompressor(zip.Store, func(reader io.Reader) io.ReadCloser {
		return &coverCloseReader{Reader: reader, close: func() error {
			closed = true
			return want
		}}
	})
	if data, err := readCoverData(zr, "cover.png"); data != nil || !errors.Is(err, want) || !closed {
		t.Fatalf("entry close failure = %d bytes, %v, closed=%v", len(data), err, closed)
	}
	closed = false
	zr.File[0].CRC32 ^= 1
	if data, err := readCoverData(zr, "cover.png"); data != nil || !errors.Is(err, zip.ErrChecksum) || !closed {
		t.Fatalf("read failure = %d bytes, %v, closed=%v", len(data), err, closed)
	}
}

type coverCloseReader struct {
	io.Reader
	close func() error
}

func (r *coverCloseReader) Close() error { return r.close() }

func TestReadCoverDataChecksArchiveIntegrity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*zip.File)
		want   error
	}{
		{"checksum", func(f *zip.File) { f.CRC32 ^= 1 }, zip.ErrChecksum},
		{"truncated", func(f *zip.File) { f.UncompressedSize64++ }, io.ErrUnexpectedEOF},
		{"excess", func(f *zip.File) { f.UncompressedSize64-- }, zip.ErrFormat},
		{"unsupported", func(f *zip.File) { f.Method = 99 }, zip.ErrAlgorithm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
			tc.mutate(zr.File[0])
			if got, err := readCoverData(zr, "cover.png"); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("corrupt cover = %d bytes, %v; want %v", len(got), err, tc.want)
			}
		})
	}
}

func TestExtractCoverPreservesConcurrentUpload(t *testing.T) {
	t.Parallel()
	lib := t.TempDir()
	zr := coverZIP(t, "cover.png", encodePNG(t, 12, 18))
	entered := make(chan struct{})
	resume := make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	zr.RegisterDecompressor(zip.Store, func(reader io.Reader) io.ReadCloser {
		close(entered)
		<-resume
		return io.NopCloser(reader)
	})
	resultCh := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() {
		release()
		wg.Wait()
	}()
	wg.Go(func() { resultCh <- extractCover(t.Context(), lib, "book", zr, "cover.png") })
	select {
	case <-entered:
	case err := <-resultCh:
		t.Fatalf("extraction ended before decoding: %v", err)
	}
	// The import has passed its initial existence check. A user's replacement
	// arriving during decoding must not be overwritten by that stale import.
	uploaded := encodeJPEGBytes(t, 40, 60)
	if _, err := WriteCoverImageJPEG(lib, "book", uploaded); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-resultCh; err != nil {
		t.Fatalf("extract cover: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(lib, filepath.FromSlash(CoverRelPath("book"))))
	if err != nil || !bytes.Equal(got, uploaded) {
		t.Fatalf("concurrent upload was overwritten: %v", err)
	}
	assertOnlyCoverFile(t, lib, "book.jpg")
}

func TestCoverWriteFailureCleansTemporaryFile(t *testing.T) {
	t.Parallel()
	lib := t.TempDir()
	coversDir := filepath.Join(lib, ".sayumi", "covers")
	if err := os.MkdirAll(filepath.Join(coversDir, "book.jpg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteCoverImageJPEG(lib, "book", encodeJPEGBytes(t, 12, 18)); err == nil {
		t.Fatal("replacing a directory unexpectedly succeeded")
	}
	assertOnlyCoverFile(t, lib, "book.jpg")
}

func assertOnlyCoverFile(t *testing.T, lib, name string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(lib, ".sayumi", "covers"))
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("cover directory contains temporary files: %v, %v", entries, err)
	}
}

func TestResizeMatchesOverOnEmptyDestination(t *testing.T) {
	t.Parallel()
	bounds := image.Rect(7, 11, 127, 191)
	for _, tc := range []struct {
		name string
		img  draw.Image
	}{
		{"rgba", image.NewRGBA(bounds)},
		{"nrgba", image.NewNRGBA(bounds)},
		{"rgba64", image.NewRGBA64(bounds)},
		{"nrgba64", image.NewNRGBA64(bounds)},
		{"gray", image.NewGray(bounds)},
		{"palette", image.NewPaletted(bounds, color.Palette{color.Transparent, color.Black, color.White})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					tc.img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: uint8(x * y)})
				}
			}
			got := resizeToFit(tc.img, 40, 60)
			want := image.NewRGBA(image.Rect(0, 0, 40, 60))
			xdraw.ApproxBiLinear.Scale(want, want.Bounds(), tc.img, tc.img.Bounds(), draw.Over, nil)
			if got.Bounds() != want.Bounds() {
				t.Fatalf("bounds = %v, want %v", got.Bounds(), want.Bounds())
			}
			for y := range want.Bounds().Dy() {
				for x := range want.Bounds().Dx() {
					gr, gg, gb, ga := got.At(x, y).RGBA()
					wr, wg, wb, wa := want.At(x, y).RGBA()
					if gr != wr || gg != wg || gb != wb || ga != wa {
						t.Fatalf("pixel (%d,%d) differs from Over", x, y)
					}
				}
			}
		})
	}
}
