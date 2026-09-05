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
	"sync"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Covers are normalized to JPEG within maxCoverWidth x maxCoverHeight. The
// standard-library JPEG encoder keeps this path pure Go without additional
// codec dependencies; WebP is imported above for decoding only.
const (
	maxCoverWidth     = 400
	maxCoverHeight    = 600
	maxCoverDimension = 10_000
	maxCoverPixels    = 24_000_000
	maxCoverBytes     = 20 << 20
)

// maxConcurrentCoverDecodes caps simultaneous decoding and resizing. A decode
// holds a full-size source image whose storage depends on the format and bit
// depth (RGBA64, for example, uses twice the pixel storage of RGBA). Together
// with the dimension/pixel limits, four slots bound decoder working sets
// independently of core count without throttling hashing and EPUB parsing.
const maxConcurrentCoverDecodes = 4

// coverDecodeSem is shared by scans and cover uploads. Scan workers acquire it
// before reading ZIP data so they cannot each buffer a large compressed image
// while waiting to decode. Upload bodies are already buffered by the API.
var coverDecodeSem = make(chan struct{}, maxConcurrentCoverDecodes)

// coverPublishMu pairs extraction's final existence check with publication by
// either writer. Uploads replace covers; extraction must preserve an upload
// that arrived while it was decoding. Keep decoding, encoding and temp writes
// outside the lock. External filesystem writers do not participate in it.
var coverPublishMu sync.Mutex

// errCoverSkipped is returned when declared dimensions exceed the render limits.
// Callers treat it as a non-fatal skip, without attempting a full image decode.
var errCoverSkipped = errors.New("cover skipped")

// ErrCoverSkipped exposes errCoverSkipped to callers outside this package (the
// cover-upload handler), which map it to a 400 "image dimensions too large"
// instead of a 500. It is the same sentinel, so errors.Is matches either name.
var ErrCoverSkipped = errCoverSkipped

func acquireCoverDecode(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case coverDecodeSem <- struct{}{}:
		// Both cases can be ready when a slot opens during cancellation.
		if err := ctx.Err(); err != nil {
			<-coverDecodeSem
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// decodeAndResizeCover holds a decode slot across the ZIP read and image work,
// but releases it before JPEG encoding. The bounded read and image operations
// do not accept a context; cancellation is observed between those stages.
func decodeAndResizeCover(ctx context.Context, bookID string, zr *zip.Reader, coverPathInZip string) (image.Image, error) {
	if err := acquireCoverDecode(ctx); err != nil {
		return nil, err
	}
	defer func() { <-coverDecodeSem }()

	coverData, err := readCoverData(zr, coverPathInZip)
	if err != nil {
		return nil, err
	}
	return decodeAndResizeCoverData(ctx, bookID, coverData)
}

// decodeAndResizeCoverData validates dimensions before allocating image pixels.
// The caller must hold coverDecodeSem until this function returns; only the
// thumbnail, not the full-size decoded source, is needed for JPEG encoding.
func decodeAndResizeCoverData(ctx context.Context, bookID string, coverData []byte) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(coverData))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("decode cover config: %w", err)
	}
	if config.Width > maxCoverDimension || config.Height > maxCoverDimension {
		slog.Warn("skipping oversized cover", "book", bookID, "width", config.Width, "height", config.Height)
		return nil, errCoverSkipped
	}
	// Bound total pixels as well as each side: a square can satisfy the
	// dimension limit and still require an excessive decoder working set.
	if int64(config.Width)*int64(config.Height) > maxCoverPixels {
		slog.Warn("skipping high-pixel-count cover", "book", bookID, "width", config.Width, "height", config.Height, "pixels", int64(config.Width)*int64(config.Height))
		return nil, errCoverSkipped
	}

	img, _, err := image.Decode(bytes.NewReader(coverData))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("decode cover image: %w", err)
	}
	img = resizeToFit(img, maxCoverWidth, maxCoverHeight)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return img, nil
}

// EncodeCoverJPEG validates and resizes an uploaded cover image and returns the
// normalized JPEG bytes (the same resized JPEG the importer produces, so the
// served cover stays uniform regardless of the source format/size). Oversized
// or too-many-pixel images return ErrCoverSkipped, which the API maps to a 400.
// Cancellation is checked between stages, and the decode slot is released
// before encoding the thumbnail so another full-size decode can proceed.
func EncodeCoverJPEG(ctx context.Context, bookID string, data []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(data)) > maxCoverBytes {
		return nil, errors.New("cover image too large")
	}

	img, decErr := func() (image.Image, error) {
		if err := acquireCoverDecode(ctx); err != nil {
			return nil, err
		}
		defer func() { <-coverDecodeSem }()
		return decodeAndResizeCoverData(ctx, bookID, data)
	}()
	if decErr != nil {
		return nil, decErr
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteCoverImageJPEG writes pre-encoded cover JPEG bytes to the sidecar cover
// store (".sayumi/covers/<id>.jpg"), replacing an existing regular cover with a
// completed temp file rather than truncating it in place. It returns the path
// relative to libraryPath for storage in cover_path. bookID comes from a stored
// book, not from an EPUB entry name.
func WriteCoverImageJPEG(libraryPath, bookID string, jpegData []byte) (relPath string, err error) {
	coversDir := filepath.Join(libraryPath, ".sayumi", "covers")
	if mkErr := os.MkdirAll(coversDir, 0o755); mkErr != nil {
		return "", fmt.Errorf("create covers dir: %w", mkErr)
	}
	coverFilename := bookID + ".jpg"

	coversRoot, rootErr := os.OpenRoot(coversDir)
	if rootErr != nil {
		return "", fmt.Errorf("open covers root: %w", rootErr)
	}
	defer func() {
		if closeErr := coversRoot.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close covers root: %w", closeErr)
		}
	}()

	tempFile, err := os.CreateTemp(coversDir, bookID+".*.jpg")
	if err != nil {
		return "", fmt.Errorf("create temp cover file: %w", err)
	}

	tempPath := tempFile.Name()
	tempName := filepath.Base(tempPath)
	closed, published := false, false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close cover file: %w", closeErr)
			}
		}
		if !published {
			if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove temp cover file failed", "path", tempPath, "err", removeErr)
			}
		}
	}()

	if _, writeErr := tempFile.Write(jpegData); writeErr != nil {
		return "", fmt.Errorf("write cover file: %w", writeErr)
	}

	closeErr := tempFile.Close()
	closed = true
	if closeErr != nil {
		return "", fmt.Errorf("close cover file: %w", closeErr)
	}

	coverPublishMu.Lock()
	defer coverPublishMu.Unlock()
	if _, err := regularCoverExists(coversRoot, coverFilename); err != nil {
		return "", err
	}
	if renameErr := coversRoot.Rename(tempName, coverFilename); renameErr != nil {
		return "", fmt.Errorf("rename cover file: %w", renameErr)
	}
	published = true

	return CoverRelPath(bookID), nil
}

// CoverRelPath returns a book's cover sidecar path relative to the library
// root, as it is persisted in books.cover_path.
//
// It is deliberately built with forward slashes instead of filepath.Join. The
// value goes into the database, and a library folder is meant to be portable:
// a Windows-written ".sayumi\covers\<id>.jpg" is a single literal filename on
// macOS and Linux, so every cover would 404 after the folder moved, with no
// self-heal (has_cover=1 plus cover_checked=1 keeps the backfill away). os.Root
// accepts forward slashes on every platform, so this form works everywhere.
func CoverRelPath(bookID string) string {
	return ".sayumi/covers/" + bookID + ".jpg"
}

// NormalizeCoverPath maps a stored cover_path to the slash form used for
// lookups, so rows written by an older Windows build still resolve after the
// library moves to a case- and separator-sensitive filesystem.
func NormalizeCoverPath(coverPath string) string {
	return strings.ReplaceAll(coverPath, `\`, "/")
}

// regularCoverExists rejects directories, symlinks and special files rather
// than treating them as rendered covers. A PathError keeps the scanner's
// backfill retryable after the filesystem obstruction is removed.
func regularCoverExists(root *os.Root, name string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat cover file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, &os.PathError{Op: "stat", Path: name, Err: errors.New("cover is not a regular file")}
	}
	return true, nil
}

func extractCover(ctx context.Context, libraryPath, bookID string, zr *zip.Reader, coverPathInZip string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	coversDir := filepath.Join(libraryPath, ".sayumi", "covers")
	if err := os.MkdirAll(coversDir, 0o755); err != nil {
		return fmt.Errorf("create covers dir: %w", err)
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

	coverFilename := bookID + ".jpg"
	if exists, err := regularCoverExists(coversRoot, coverFilename); err != nil || exists {
		return err
	}

	img, err := decodeAndResizeCover(ctx, bookID, zr, coverPathInZip)
	if err != nil {
		// A skip is not a successful extraction: recording a cover_path here
		// would advertise a JPEG that was never created.
		return err
	}

	tempFile, err := os.CreateTemp(coversDir, bookID+".*.jpg")
	if err != nil {
		return fmt.Errorf("create temp cover file: %w", err)
	}

	tempPath := tempFile.Name()
	tempName := filepath.Base(tempPath)
	closed, published := false, false
	defer func() {
		if !closed {
			if closeErr := tempFile.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close cover file: %w", closeErr)
			}
		}
		// Preserving a cover published during decoding is also a success, but
		// leaves our temp behind. A completed rename needs no extra unlink.
		if !published {
			if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove temp cover file failed", "path", tempPath, "err", removeErr)
			}
		}
	}()

	if encodeErr := jpeg.Encode(tempFile, img, &jpeg.Options{Quality: 85}); encodeErr != nil {
		return fmt.Errorf("encode jpeg: %w", encodeErr)
	}

	closeErr := tempFile.Close()
	closed = true
	if closeErr != nil {
		return fmt.Errorf("close cover file: %w", closeErr)
	}

	coverPublishMu.Lock()
	defer coverPublishMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if exists, err := regularCoverExists(coversRoot, coverFilename); err != nil || exists {
		return err
	}
	if renameErr := coversRoot.Rename(tempName, coverFilename); renameErr != nil {
		return fmt.Errorf("rename cover file: %w", renameErr)
	}
	published = true

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
	// Keep the filter and compositing mode stable: changing either can alter
	// persisted cover pixels even when the output dimensions stay the same.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}
