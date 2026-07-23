package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sayumi/internal/storage"
)

const (
	maxUploadSize         = 100 << 20 // 100 MB
	maxMultipartOverhead  = 1 << 20   // bounded room for multipart headers/fields
	maxFilenameCollisions = 10_000
)

func uploadBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		// Receiving up to 100 MB can outlast the server's global WriteTimeout,
		// which Go arms when the request headers are read — before the body is
		// consumed. Clear the write deadline for just this handler so a large
		// upload over a slow link is not aborted mid-response; the body is already
		// bounded by MaxBytesReader below. Best-effort: if the writer does not
		// support deadlines we proceed unchanged.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear upload write deadline unsupported", "err", err)
		}

		tmpPath, filename, ok := stageMultipartEPUB(w, r, pd.LibPath, maxUploadSize)
		if !ok {
			return
		}
		defer func() {
			if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Error("remove temp file failed", "path", tmpPath, "err", err)
			}
		}()

		if err := validateEPUB(tmpPath); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}

		existingID, contentHash, isDuplicate := pd.Scanner.CheckDuplicate(r.Context(), tmpPath)
		if isDuplicate {
			book, found, err := loadAndWarmUploadedBook(r, pd, existingID, true)
			if err != nil {
				slog.Error("load duplicate book failed", "filename", filename, "existing_id", existingID, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load duplicate book")
				return
			}
			if !found {
				slog.Error("duplicate book missing after dedup match", "filename", filename, "existing_id", existingID)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load duplicate book")
				return
			}
			writeJSON(w, http.StatusOK, bookResponseFromRecord(book))
			return
		}

		destName := sanitizeFilename(filename)
		baseName := strings.TrimSuffix(destName, ".epub")

		// Atomically reserve a destination filename so two concurrent uploads
		// cannot both claim the same path: linkOrCopyExclusive hard-links the
		// staged temp into place (falling back to an O_EXCL copy where links are
		// unsupported) and reports os.ErrExist when the name is taken. If the
		// first candidate is taken we append a numeric suffix and retry, up to
		// maxFilenameCollisions times.
		var destPath string
		var copyErr error
		for index := 0; index <= maxFilenameCollisions; index++ {
			if index == 0 {
				destPath = filepath.Join(pd.LibPath, destName)
			} else {
				destPath = filepath.Join(pd.LibPath, fmt.Sprintf("%s (%d).epub", baseName, index))
			}
			copyErr = linkOrCopyExclusive(tmpPath, destPath)
			if copyErr == nil {
				break
			}
			if !errors.Is(copyErr, os.ErrExist) {
				slog.Error("copy to library failed", "src", tmpPath, "dst", destPath, "err", copyErr)
				writeError(w, http.StatusInternalServerError, "server_error", "failed to save file to library")
				return
			}
		}
		if errors.Is(copyErr, os.ErrExist) {
			writeError(w, http.StatusConflict, "name_exhausted", "too many files with the same name")
			return
		}

		if err := pd.DB.RemoveIgnoredFileContext(r.Context(), destPath); err != nil {
			// The EPUB landed on disk but its ignored_files entry could not be
			// cleared, so ImportFile would treat it as ignored and return empty
			// results. Remove the orphaned file and surface the error instead.
			if removeErr := os.Remove(destPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove orphaned epub after ignored-entry clear failure", "path", destPath, "err", removeErr)
			}
			slog.Error("remove ignored file entry failed", "path", destPath, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to prepare book for import")
			return
		}

		bookID, imported, err := pd.Scanner.ImportUploadedFile(r.Context(), destPath, contentHash)
		if err != nil {
			if removeErr := os.Remove(destPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove failed import file", "path", destPath, "err", removeErr)
			}
			slog.Error("import book failed", "filename", filename, "path", destPath, "err", err)
			writeError(w, http.StatusInternalServerError, "import_error", "failed to import book")
			return
		}

		// Use an authoritative DB read after ImportUploadedFile: when this upload
		// lost a content-hash race, the canonical path determines whether our
		// destination is redundant or was concurrently imported by a scan.
		book, found, err := loadAndWarmUploadedBook(r, pd, bookID, false)
		if err != nil {
			slog.Error("retrieve imported book failed", "filename", filename, "book_id", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "book imported but failed to retrieve")
			return
		}
		if !found {
			slog.Error("imported book missing after import", "filename", filename, "book_id", bookID)
			writeError(w, http.StatusInternalServerError, "db_error", "book imported but failed to retrieve")
			return
		}

		status := http.StatusCreated
		if !imported {
			status = http.StatusOK
			canonicalPath, pathErr := filepath.Abs(book.FilePath)
			if pathErr != nil {
				slog.Error("resolve canonical duplicate path failed", "book_id", bookID, "path", book.FilePath, "err", pathErr)
				writeError(w, http.StatusInternalServerError, "server_error", "failed to resolve imported book")
				return
			}
			stagedPath, pathErr := filepath.Abs(destPath)
			if pathErr != nil {
				slog.Error("resolve staged duplicate path failed", "book_id", bookID, "path", destPath, "err", pathErr)
				writeError(w, http.StatusInternalServerError, "server_error", "failed to resolve imported book")
				return
			}
			if canonicalPath != stagedPath {
				if removeErr := os.Remove(destPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					slog.Error("remove duplicate upload path failed", "path", destPath, "err", removeErr)
					writeError(w, http.StatusInternalServerError, "server_error", "failed to clean duplicate upload")
					return
				}
			}
		}

		writeJSON(w, status, bookResponseFromRecord(book))
	}
}

func stageMultipartEPUB(
	w http.ResponseWriter,
	r *http.Request,
	libraryPath string,
	maxFileSize int64,
) (tmpPath, filename string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFileSize+maxMultipartOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid multipart form")
		return "", "", false
	}

	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid", "missing epub file field")
			return "", "", false
		}
		if nextErr != nil {
			if _, tooLarge := errors.AsType[*http.MaxBytesError](nextErr); tooLarge {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 100MB)")
				return "", "", false
			}
			writeError(w, http.StatusBadRequest, "invalid", "invalid multipart form")
			return "", "", false
		}

		filename = part.FileName()
		if part.FormName() != "epub" || filename == "" {
			if closeErr := part.Close(); closeErr != nil {
				if _, tooLarge := errors.AsType[*http.MaxBytesError](closeErr); tooLarge {
					writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 100MB)")
					return "", "", false
				}
				writeError(w, http.StatusBadRequest, "invalid", "invalid multipart form")
				return "", "", false
			}
			continue
		}
		if !strings.HasSuffix(strings.ToLower(filename), ".epub") {
			writeError(w, http.StatusBadRequest, "invalid", "file must be an .epub")
			return "", "", false
		}

		tmpFile, createErr := os.CreateTemp(libraryPath, ".sayumi-upload-*.epub")
		if createErr != nil {
			slog.Error("create temp file failed", "filename", filename, "err", createErr)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to create temp file")
			return "", "", false
		}
		tmpPath = tmpFile.Name()
		if chmodErr := os.Chmod(tmpPath, 0o600); chmodErr != nil {
			slog.Warn("failed to restrict temp file permissions", "path", tmpPath, "err", chmodErr)
		}

		written, copyErr := io.Copy(tmpFile, io.LimitReader(part, maxFileSize+1))
		if written > maxFileSize {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 100MB)")
			return "", "", false
		}
		partCloseErr := part.Close()
		fileCloseErr := tmpFile.Close()
		if copyErr != nil || partCloseErr != nil || fileCloseErr != nil {
			_ = os.Remove(tmpPath)
			saveErr := errors.Join(copyErr, partCloseErr, fileCloseErr)
			if requestContextDone(r, saveErr) {
				return "", "", false
			}
			if _, tooLarge := errors.AsType[*http.MaxBytesError](saveErr); tooLarge {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 100MB)")
				return "", "", false
			}
			slog.Error("write temp file failed", "filename", filename, "err", saveErr)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to save upload")
			return "", "", false
		}
		return tmpPath, filename, true
	}
}

func loadAndWarmUploadedBook(
	r *http.Request,
	pd *profileDeps,
	id string,
	allowCache bool,
) (storage.BookRecord, bool, error) {
	pd.bookReplaceMu.RLock()
	defer pd.bookReplaceMu.RUnlock()

	if allowCache {
		if book, ok := pd.Books.Get(id); ok {
			return book, true, nil
		}
	}
	summary, found, err := pd.DB.GetBookSummaryContext(r.Context(), id)
	if err != nil || !found {
		return storage.BookRecord{}, found, err
	}
	book := storage.BookRecord{BookSummary: summary}
	pd.Books.Add(book)
	return book, true, nil
}

func bookResponseFromRecord(book storage.BookRecord) BookResponse {
	return bookResponseFromSummary(book.BookSummary)
}

func validateEPUB(filePath string) error {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return fmt.Errorf("file is not a valid ZIP archive: %w", err)
	}
	defer func() {
		if err := zr.Close(); err != nil {
			slog.Error("close epub zip failed", "path", filePath, "err", err)
		}
	}()

	for _, file := range zr.File {
		if strings.EqualFold(file.Name, "META-INF/container.xml") {
			return nil
		}
	}
	return errors.New("file is not a valid EPUB (missing container.xml)")
}

// filenameSanitizer maps characters that are illegal or awkward in filenames
// across common filesystems to underscores. It is built once at package scope:
// strings.Replacer is immutable and safe for concurrent use, so rebuilding it
// on every upload (a cold path, but trivially avoidable) is pure waste.
var filenameSanitizer = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_",
	"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
)

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSpace(name)

	name = filenameSanitizer.Replace(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)

	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if name == "" || name == "." {
		name = "book"
	}

	return name + ".epub"
}

// linkOrCopyExclusive places src at dst without overwriting an existing file,
// preferring a hard link so the staged upload is not copied a second time.
//
// os.Link is atomic and fails with os.ErrExist when dst already exists, giving
// the same no-clobber guarantee as an O_EXCL create, so callers retry with a
// new name on os.ErrExist. The link shares src's inode (mode 0o600 from the
// staging temp), so the mode is widened to 0o644 to match a normal library
// file. On filesystems without hard-link support (e.g. FAT, some network
// mounts) Link fails with a non-ErrExist error and we fall back to a full
// O_EXCL copy.
func linkOrCopyExclusive(src, dst string) error {
	switch err := os.Link(src, dst); {
	case err == nil:
		if chmodErr := os.Chmod(dst, 0o644); chmodErr != nil {
			slog.Warn("set library file permissions failed", "path", dst, "err", chmodErr)
		}
		return nil
	case errors.Is(err, os.ErrExist):
		return err // name taken: caller tries the next candidate
	default:
		// Link unsupported on this filesystem (or otherwise failed); fall back to
		// copying the bytes under the same O_EXCL no-clobber contract.
		return copyFileExclusive(src, dst)
	}
}

// copyFileExclusive atomically reserves dst with O_EXCL and streams src into
// it. Returns os.ErrExist (wrapped) if dst already exists, allowing callers to
// retry with a different name. On any write failure the partial file is removed.
// It is the fallback for linkOrCopyExclusive on filesystems lacking hard-link
// support.
//
// src is a server-controlled staging temp and dst is derived from
// sanitizeFilename, so os.Root is not needed for path safety here.
func copyFileExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %q: %w", dst, err)
	}

	// O_EXCL makes creation atomic: if two goroutines race on the same path,
	// exactly one gets the file and the other receives os.ErrExist.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err // os.ErrExist signals "try next candidate name"
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy to %q: %w", dst, err)
	}

	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close destination %q: %w", dst, err)
	}

	return nil
}
