package library

import (
	"archive/zip"
	"bytes"
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
	maxCoverBytes     = 20 << 20
)

func extractCover(libraryPath, bookID string, zr *zip.Reader, coverPathInZip string) (err error) {
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

	coverData, err := readCoverData(zr, coverPathInZip)
	if err != nil {
		return err
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(coverData))
	if err != nil {
		return fmt.Errorf("decode cover config: %w", err)
	}
	if config.Width > maxCoverDimension || config.Height > maxCoverDimension {
		slog.Warn("skipping oversized cover", "book", bookID, "width", config.Width, "height", config.Height)
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(coverData))
	if err != nil {
		return fmt.Errorf("decode cover image: %w", err)
	}
	img = resizeToFit(img, maxCoverWidth, maxCoverHeight)

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
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}
