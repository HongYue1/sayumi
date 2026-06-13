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

		// +1024: generous headroom for multipart framing (boundary lines,
		// Content-Disposition headers) so that a exactly-100 MB EPUB is not
		// rejected due to framing overhead before the body limit fires.
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024)

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 100MB)")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid", "invalid multipart form")
			return
		}
		if r.MultipartForm != nil {
			defer func() {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					slog.Error("clean multipart temp files failed", "err", err)
				}
			}()
		}

		file, header, err := r.FormFile("epub")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "missing epub file field")
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				slog.Error("close uploaded file failed", "err", err)
			}
		}()

		if !strings.HasSuffix(strings.ToLower(header.Filename), ".epub") {
			writeError(w, http.StatusBadRequest, "invalid", "file must be an .epub")
			return
		}

		tmpFile, err := os.CreateTemp("", "sayumi-upload-*.epub")
		if err != nil {
			slog.Error("create temp file failed", "filename", header.Filename, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to create temp file")
			return
		}

		tmpPath := tmpFile.Name()

		// Restrict the temp file to the owner only. os.CreateTemp creates with
		// 0o600 on most Unix systems, but the mode is subject to the process
		// umask. Calling Chmod explicitly removes any doubt.
		if err := os.Chmod(tmpPath, 0o600); err != nil {
			slog.Warn("failed to restrict temp file permissions", "path", tmpPath, "err", err)
		}

		defer func() {
			if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Error("remove temp file failed", "path", tmpPath, "err", err)
			}
		}()

		if _, err := tmpFile.ReadFrom(file); err != nil {
			if closeErr := tmpFile.Close(); closeErr != nil {
				slog.Error("close temp file after write error failed", "err", closeErr)
			}
			slog.Error("write temp file failed", "filename", header.Filename, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to save upload")
			return
		}
		if err := tmpFile.Close(); err != nil {
			slog.Error("close temp file failed", "filename", header.Filename, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to save upload")
			return
		}

		if err := validateEPUB(tmpPath); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}

		existingID, contentHash, isDuplicate := pd.Scanner.CheckDuplicate(r.Context(), tmpPath)
		if isDuplicate {
			if book, ok := pd.Books.Get(existingID); ok {
				writeJSON(w, http.StatusOK, bookResponseFromRecord(book))
				return
			}

			book, err := pd.DB.GetBookContext(r.Context(), existingID)
			if err != nil {
				slog.Error("load duplicate book failed", "filename", header.Filename, "existing_id", existingID, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load duplicate book")
				return
			}

			pd.Books.Add(book)
			writeJSON(w, http.StatusOK, bookResponseFromRecord(book))
			return
		}

		destName := sanitizeFilename(header.Filename)
		baseName := strings.TrimSuffix(destName, ".epub")

		// Atomically reserve a destination filename with O_EXCL so that two
		// concurrent uploads of the same file cannot both claim the same path.
		// If the first candidate is taken we append a numeric suffix and retry,
		// up to maxFilenameCollisions times.
		var destPath string
		var copyErr error
		for index := 0; index <= maxFilenameCollisions; index++ {
			if index == 0 {
				destPath = filepath.Join(pd.LibPath, destName)
			} else {
				destPath = filepath.Join(pd.LibPath, fmt.Sprintf("%s (%d).epub", baseName, index))
			}
			copyErr = copyFileExclusive(tmpPath, destPath)
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

		bookID, err := pd.Scanner.ImportFile(r.Context(), destPath, contentHash)
		if err != nil {
			if removeErr := os.Remove(destPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				slog.Error("remove failed import file", "path", destPath, "err", removeErr)
			}
			slog.Error("import book failed", "filename", header.Filename, "path", destPath, "err", err)
			writeError(w, http.StatusInternalServerError, "import_error", "failed to import book")
			return
		}

		book, err := pd.DB.GetBookContext(r.Context(), bookID)
		if err != nil {
			slog.Error("retrieve imported book failed", "filename", header.Filename, "book_id", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "book imported but failed to retrieve")
			return
		}
		pd.Books.Add(book)

		writeJSON(w, http.StatusCreated, bookResponseFromRecord(book))
	}
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

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSpace(name)

	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	name = replacer.Replace(name)
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

// copyFileExclusive atomically reserves dst with O_EXCL and streams src into
// it. Returns os.ErrExist (wrapped) if dst already exists, allowing callers to
// retry with a different name. On any write failure the partial file is removed.
//
// src is a temp file in os.TempDir, not inside the library, so os.Root is not
// applicable here; path safety is ensured by sanitizeFilename on the dst side.
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
