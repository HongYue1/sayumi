package library

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxCoverWidth     = 400
	maxCoverHeight    = 600
	maxCoverDimension = 10_000
	maxCoverPixels    = 24_000_000
	maxCoverBytes     = 20 << 20
)

// maxConcurrentCoverDecodes caps how many cover images may be decoded and
// resized at the same time. A single decode transiently holds a full-size
// RGBA buffer (up to maxCoverPixels*4 bytes, ~96 MB at the cap), so without a
// limit the library scan's worker pool — sized to GOMAXPROCS — could hold one
// such buffer per core at once. Bounding concurrent decodes keeps peak memory
// independent of core count without throttling the cheaper hashing/parsing.
const maxConcurrentCoverDecodes = 4

// coverDecodeSem enforces maxConcurrentCoverDecodes across every goroutine that
// calls extractCover (notably the scan worker pool).
var coverDecodeSem = make(chan struct{}, maxConcurrentCoverDecodes)

// errCoverSkipped is returned by decodeAndResizeCover when a cover is valid but
// intentionally not rendered (oversized or too many pixels). Callers treat it
// as a non-fatal skip rather than a failure.
var errCoverSkipped = errors.New("cover skipped")

// decodeAndResizeCover reads the cover bytes from the zip, validates its
// dimensions, and returns a resized image ready for JPEG encoding. It returns
// errCoverSkipped when the cover should be skipped (oversized or too many pixels).
//
// The entire read + decode + resize runs while holding coverDecodeSem, so both
// the encoded source buffer (up to maxCoverBytes) and the decoded RGBA buffer
// (up to maxCoverPixels*4) are bounded by maxConcurrentCoverDecodes rather than
// by the scan worker count — otherwise every worker could hold a multi-MB
// encoded buffer while merely waiting for a decode slot. The acquire honors ctx
// so a canceled scan unblocks here instead of waiting for an in-flight decode.
func decodeAndResizeCover(ctx context.Context, bookID string, zr *zip.Reader, coverPathInZip string) (image.Image, error) {
	select {
	case coverDecodeSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-coverDecodeSem }()

	coverData, err := readCoverData(zr, coverPathInZip)
	if err != nil {
		return nil, err
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(coverData))
	if err != nil {
		return nil, fmt.Errorf("decode cover config: %w", err)
	}
	if config.Width > maxCoverDimension || config.Height > maxCoverDimension {
		slog.Warn("skipping oversized cover", "book", bookID, "width", config.Width, "height", config.Height)
		return nil, errCoverSkipped
	}
	// Reject high pixel counts even when each side is within bounds: decoding
	// expands to a 4-byte-per-pixel RGBA buffer, so capping total pixels keeps a
	// crafted cover from exhausting memory.
	if int64(config.Width)*int64(config.Height) > maxCoverPixels {
		slog.Warn("skipping high-pixel-count cover", "book", bookID, "width", config.Width, "height", config.Height, "pixels", int64(config.Width)*int64(config.Height))
		return nil, errCoverSkipped
	}

	img, _, err := image.Decode(bytes.NewReader(coverData))
	if err != nil {
		return nil, fmt.Errorf("decode cover image: %w", err)
	}
	return resizeToFit(img, maxCoverWidth, maxCoverHeight), nil
}

func extractCover(ctx context.Context, libraryPath, bookID string, zr *zip.Reader, coverPathInZip string) (err error) {
	coversDir := filepath.Join(libraryPath, ".sayumi", "covers")
	if err := os.MkdirAll(coversDir, 0o755); err != nil {
		return fmt.Errorf("create covers dir: %w", err)
	}

	coverFilename := bookID + ".jpg"
	outPath := filepath.Join(coversDir, coverFilename)
	if _, err := os.Stat(outPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat cover file: %w", err)
	}

	img, err := decodeAndResizeCover(ctx, bookID, zr, coverPathInZip)
	if err != nil {
		if errors.Is(err, errCoverSkipped) {
			// Cover was valid but intentionally not rendered; not a failure.
			return nil
		}
		return err
	}

	coversRoot, rootErr := os.OpenRoot(coversDir)
	if rootErr != nil {
		return fmt.Errorf("open covers root: %w", rootErr)
	}
	defer func() {
		if closeErr := coversRoot.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close covers root: %w", closeErr)
		}
	}()

	tempFile, err := os.CreateTemp(coversDir, bookID+".*.jpg")
	if err != nil {
		return fmt.Errorf("create temp cover file: %w", err)
	}

	tempPath := tempFile.Name()
	tempName := filepath.Base(tempPath)
	closed := false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close cover file: %w", closeErr)
			}
		}
		if err != nil {
			if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove temp cover file failed", "path", tempPath, "err", removeErr)
			}
		}
	}()

	if encodeErr := jpeg.Encode(tempFile, img, &jpeg.Options{Quality: 85}); encodeErr != nil {
		return fmt.Errorf("encode jpeg: %w", encodeErr)
	}

	if closeErr := tempFile.Close(); closeErr != nil {
		return fmt.Errorf("close cover file: %w", closeErr)
	}
	closed = true

	if renameErr := coversRoot.Rename(tempName, coverFilename); renameErr != nil {
		return fmt.Errorf("rename cover file: %w", renameErr)
	}

	return nil
}

func readCoverData(zr *zip.Reader, coverPathInZip string) ([]byte, error) {
	coverPathInZip = strings.TrimPrefix(coverPathInZip, "/")

	for _, file := range zr.File {
		fileName := strings.TrimPrefix(file.Name, "/")
		if !strings.EqualFold(fileName, coverPathInZip) {
			continue
		}
		if file.UncompressedSize64 > uint64(maxCoverBytes) {
			return nil, errors.New("cover image too large")
		}

		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open cover in zip: %w", err)
		}

		data, readErr := io.ReadAll(io.LimitReader(reader, maxCoverBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read cover: %w", readErr)
		}
		if int64(len(data)) > maxCoverBytes {
			return nil, errors.New("cover image too large")
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close cover entry: %w", closeErr)
		}
		return data, nil
	}

	return nil, fmt.Errorf("cover not found in zip: %s", coverPathInZip)
}

func resizeToFit(img image.Image, maxW, maxH int) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= maxW && height <= maxH {
		return img
	}

	scaleW := float64(maxW) / float64(width)
	scaleH := float64(maxH) / float64(height)
	scale := min(scaleW, scaleH)

	newWidth := max(int(float64(width)*scale), 1)
	newHeight := max(int(float64(height)*scale), 1)

	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	// ApproxBiLinear is markedly faster than BiLinear and the quality
	// difference is imperceptible when downscaling to cover-thumbnail sizes
	// (<=400x600), so it is the better tradeoff on the import path.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}
