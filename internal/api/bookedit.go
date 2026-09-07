package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

const (
	maxBookTitleLen  = 512
	maxBookAuthorLen = 512
	// maxCoverUploadBytes bounds the cover upload body. It matches
	// library.maxCoverBytes (the decode-time guard) so the two limits agree.
	maxCoverUploadBytes = 20 << 20 // 20 MB
	// bookEditCommitTimeout bounds the DB/cache commit that follows a completed
	// file swap. It runs on a context detached from the request because the new
	// bytes are already on disk: abandoning the row update because the client
	// hung up would leave the file and its row describing different generations.
	bookEditCommitTimeout = 30 * time.Second
)

// updateBookRequest is the PATCH /api/books/{id} body. Fields are pointers so a
// missing key is distinguishable from an empty string: only provided fields are
// changed (patch semantics), an omitted field keeps its current value.
type updateBookRequest struct {
	Title  *string `json:"title"`
	Author *string `json:"author"`
}

// errBookInUse signals that a book's file could not be replaced because a
// reader currently holds it open. Handlers map it to a 409 so the client can
// prompt the user to close the book and retry.
var errBookInUse = errors.New("book is open in the reader")

var errBookChanged = errors.New("book changed during edit preparation")

// The edit mutex excludes other edits, not deletion. Mutation handlers also
// hold libraryScanMu against rescans. Call under either side of bookReplaceMu,
// and again under its write side before publishing: the read-to-write handoff
// is not an atomic lock upgrade.
func checkBookEditSnapshot(pd *profileDeps, book storage.BookRecord) error {
	current, ok := pd.Books.Get(book.ID)
	if !ok {
		return storage.ErrNotFound
	}
	if current.BookSummary != book.BookSummary {
		return errBookChanged
	}
	return nil
}

func writeBookEditConflict(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "book not found")
	case errors.Is(err, errBookChanged):
		writeError(w, http.StatusConflict, "book_changed", "book changed; reload and try again")
	case errors.Is(err, errBookInUse):
		writeError(w, http.StatusConflict, "book_open", "close the book in the reader and try again")
	case errors.Is(err, storage.ErrFileHashConflict):
		writeError(w, http.StatusConflict, "duplicate", "another copy of this book already exists")
	default:
		return false
	}
	return true
}

// applyBookMetaPatch merges a PATCH body onto the current title/author.
// Omitted pointer fields keep the current value. Returns a user-facing error
// message when validation fails (empty title, overlong fields).
func applyBookMetaPatch(curTitle, curAuthor string, req updateBookRequest) (title, author string, errMsg string) {
	title = curTitle
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	author = curAuthor
	if req.Author != nil {
		author = strings.TrimSpace(*req.Author)
	}
	if title == "" {
		return "", "", "title must not be empty"
	}
	if len(title) > maxBookTitleLen {
		return "", "", "title too long"
	}
	if len(author) > maxBookAuthorLen {
		return "", "", "author too long"
	}
	return title, author, ""
}

// refreshBookCache reloads a book's summary from the DB and updates the
// in-memory book cache so list/detail responses (and their ETags) reflect a
// just-applied change. The caller holds bookReplaceMu's write side. If the row
// cannot be reloaded after commit, fail closed instead of serving new bytes with
// an old hash/token. Reopening the profile can rebuild the cache from the DB.
func refreshBookCache(ctx context.Context, pd *profileDeps, id string) (storage.BookRecord, error) {
	summary, found, err := pd.DB.GetBookSummaryContext(ctx, id)
	if err != nil || !found {
		pd.Books.Remove(id)
		if err == nil {
			err = storage.ErrNotFound
		}
		return storage.BookRecord{}, fmt.Errorf("reload book %s: %w", id, err)
	}
	book := storage.BookRecord{BookSummary: summary}
	pd.Books.Add(book)
	return book, nil
}

// bookEditBackup keeps the old bytes until the DB accepts the edit. Keeping a
// sibling copy (rather than moving the source aside) preserves atomic rename
// publication and never exposes a missing source to a concurrent scanner. The
// root is borrowed; a failed rollback retains its backup for manual recovery.
// This reconciles live-operation failures, not a crash-atomic filesystem/DB txn.
type bookEditBackup struct {
	root     *os.Root
	path     string
	tempPath string
	keep     bool
}

func backupBookEditFile(ctx context.Context, root *os.Root, path string) (bookEditBackup, error) {
	if root == nil {
		return bookEditBackup{}, errors.New("book edit root unavailable")
	}
	if err := ctx.Err(); err != nil {
		return bookEditBackup{}, err
	}
	backup := bookEditBackup{root: root, path: path}
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return backup, nil // Rollback removes a newly created sidecar.
	}
	if err != nil {
		return bookEditBackup{}, fmt.Errorf("stat original: %w", err)
	}
	if !info.Mode().IsRegular() {
		return bookEditBackup{}, errors.New("original is not a regular file")
	}
	source, err := root.Open(path)
	if err != nil {
		return bookEditBackup{}, fmt.Errorf("open original: %w", err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			slog.Error("close book edit source failed", "path", path, "err", err)
		}
	}()

	backup.tempPath = filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+rand.Text()+".bak")
	tmp, err := root.OpenFile(backup.tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return bookEditBackup{}, fmt.Errorf("create edit backup: %w", err)
	}
	_, copyErr := io.Copy(tmp, source)
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		backup.cleanup()
		return bookEditBackup{}, fmt.Errorf("copy edit backup: %w", err)
	}
	return backup, nil
}

func (b *bookEditBackup) cleanup() {
	if b.tempPath != "" && !b.keep {
		if err := b.root.Remove(b.tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("remove edit backup failed", "path", b.tempPath, "err", err)
		}
	}
}

func (b *bookEditBackup) restore() error {
	if b.root == nil {
		return nil
	}
	if b.tempPath == "" {
		if err := b.root.Remove(b.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove uncommitted cover: %w", err)
		}
		return nil
	}
	if err := b.root.Rename(b.tempPath, b.path); err != nil {
		b.keep = true
		return fmt.Errorf("restore original (backup %s): %w", filepath.Join(b.root.Name(), b.tempPath), err)
	}
	b.tempPath = ""
	return nil
}

// Roll back before releasing the replacement gate or writing the error response.
// A rollback failure must not leave stale resource tokens usable for new bytes.
func rollbackBookEdit(pd *profileDeps, id string, backups ...*bookEditBackup) bool {
	ok := true
	for _, backup := range backups {
		if err := backup.restore(); err != nil {
			slog.Error("restore failed book edit", "book", id, "err", err)
			ok = false
		}
	}
	if !ok {
		pd.Books.Remove(id)
	}
	return ok
}

type preparedBookFile struct {
	tmpPath    string
	hash       string
	size       int64
	original   bookEditBackup
	sourceRoot *os.Root
}

func (p *preparedBookFile) cleanup() {
	if p.tmpPath != "" {
		if err := os.Remove(p.tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Error("remove temp epub failed", "path", p.tmpPath, "err", err)
		}
	}
	p.original.cleanup()
	if p.sourceRoot != nil {
		if err := p.sourceRoot.Close(); err != nil {
			slog.Error("close book edit root failed", "err", err)
		}
	}
}

// prepareBookFile builds, hashes and backs up beside the source EPUB. The read
// gate excludes deletion while the independent ZIP reader/copy is open, without
// blocking chapter readers. The caller holds bookEditMu to serialize edits and
// rechecks the snapshot after taking the write gate for publication.
func prepareBookFile(ctx context.Context, pd *profileDeps, book storage.BookRecord, edit epub.MetadataEdit) (preparedBookFile, error) {
	pd.bookReplaceMu.RLock()
	defer pd.bookReplaceMu.RUnlock()
	if err := checkBookEditSnapshot(pd, book); err != nil {
		return preparedBookFile{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedBookFile{}, err
	}
	tmpPath, err := epub.RewriteBook(book.FilePath, edit)
	if err != nil {
		return preparedBookFile{}, fmt.Errorf("rewrite epub: %w", err)
	}
	prepared := preparedBookFile{tmpPath: tmpPath}
	prepared.hash, prepared.size, err = library.HashFile(ctx, tmpPath)
	if err != nil {
		prepared.cleanup()
		return preparedBookFile{}, fmt.Errorf("rehash epub: %w", err)
	}
	prepared.sourceRoot, err = os.OpenRoot(filepath.Dir(book.FilePath))
	if err == nil {
		prepared.original, err = backupBookEditFile(ctx, prepared.sourceRoot, filepath.Base(book.FilePath))
		if err == nil && prepared.original.tempPath == "" {
			err = errors.New("source disappeared during edit preparation")
		}
	}
	if err != nil {
		prepared.cleanup()
		return preparedBookFile{}, fmt.Errorf("back up epub: %w", err)
	}
	return prepared, nil
}

// replaceBookFile atomically installs a prepared sibling file. The caller must
// hold bookReplaceMu's write side through the following DB/cache refresh so no
// chapter can pair the new bytes with the previous file hash/resource token.
func replaceBookFile(pd *profileDeps, filePath, tmpPath string) error {
	// The cached reader must be fully released before the file is replaced. If a
	// request still holds it open, refuse rather than risk a torn/failed swap.
	if !pd.Store.TryCloseForReplace(filePath) {
		return errBookInUse
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		// Missing files, permissions and other I/O failures are not evidence
		// that an app reader is open. Preserve the cause for the 500 path.
		return fmt.Errorf("replace epub: %w", err)
	}
	return nil
}

// updateBookHandler handles PATCH /api/books/{id}: edits the user-facing title
// and author. The change is written back into the EPUB's package document (so
// the file itself reflects it) and the recomputed file hash/size are persisted.
// Cover edits go through uploadCoverHandler.
func updateBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		// Existence only: the record itself is re-read under bookEditMu below,
		// since decoding the body gives a preceding edit time to land.
		if _, ok := pd.Books.Get(id); !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Shared decoder: this was the only JSON endpoint decoding inline, so it
		// alone mapped an oversize body to 400 instead of 413 and silently
		// accepted trailing data after the first value.
		var req updateBookRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}

		// Waiting for another edit can itself outlast the header-armed deadline.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear book edit write deadline unsupported", "err", err)
		}

		// Serialize edit preparation without blocking chapter readers. Re-read the
		// book after taking the lock so a preceding cover/metadata edit cannot be
		// overwritten from the stale pre-decode snapshot above.
		pd.libraryScanMu.RLock()
		defer pd.libraryScanMu.RUnlock()
		pd.bookEditMu.Lock()
		defer pd.bookEditMu.Unlock()
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		title, author, errMsg := applyBookMetaPatch(book.Title, book.Author, req)
		if errMsg != "" {
			writeError(w, http.StatusBadRequest, "invalid", errMsg)
			return
		}

		// No effective change: skip rewriting the (potentially large) file.
		if title == book.Title && author == book.Author {
			writeJSON(w, http.StatusOK, bookResponseFromRecord(r, pd, book))
			return
		}

		if book.FilePath == "" {
			writeError(w, http.StatusUnprocessableEntity, "no_file", "book has no source file to edit")
			return
		}

		prepared, err := prepareBookFile(r.Context(), pd, book, epub.MetadataEdit{Title: &title, Author: &author})
		if err != nil {
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("write book metadata into epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
			return
		}
		defer prepared.cleanup()

		pd.bookReplaceMu.Lock()
		defer pd.bookReplaceMu.Unlock()
		if err := checkBookEditSnapshot(pd, book); err != nil {
			writeBookEditConflict(w, err)
			return
		}
		if err := replaceBookFile(pd, book.FilePath, prepared.tmpPath); err != nil {
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("write book metadata into epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
			return
		}

		// The file on disk is already the new generation, so the row describing
		// it must be updated even if the client has hung up by now: on
		// r.Context() a disconnect mid-rewrite (a large EPUB takes seconds)
		// aborts the write and strands the DB holding the previous title and
		// file_hash for bytes that no longer match, which nothing reconciles —
		// the scanner short-circuits on a known path and never re-hashes it.
		commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(r.Context()), bookEditCommitTimeout)
		defer cancelCommit()

		if err := pd.DB.UpdateBookMetadataAndFileContext(commitCtx, id, title, author, prepared.hash, prepared.size); err != nil {
			if !rollbackBookEdit(pd, id, &prepared.original) {
				writeError(w, http.StatusInternalServerError, "io_error", "failed to restore the original book; see server log")
				return
			}
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("update book metadata failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update book")
			return
		}

		updated, err := refreshBookCache(commitCtx, pd, id)
		if err != nil {
			slog.Error("reload book after metadata update failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "book updated but failed to reload")
			return
		}
		writeJSON(w, http.StatusOK, bookResponseFromRecord(r, pd, updated))
	}
}

// uploadCoverHandler handles PUT /api/books/{id}/cover: replaces a book's cover
// with an uploaded image (multipart field "cover"). The image is normalized to
// the same resized JPEG the importer produces, so the served cover stays
// uniform regardless of the source format/size. The normalized JPEG is embedded
// into the EPUB file itself and also written to the sidecar cover store that
// serves the displayed cover; books without a source file fall back to a
// sidecar-only update.
func uploadCoverHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		// Existence only: the record itself is re-read under bookEditMu below,
		// since decoding and re-encoding the image gives a preceding edit time
		// to land.
		if _, ok := pd.Books.Get(id); !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Re-encoding a large image and rewriting the EPUB can outlast the server
		// WriteTimeout (armed at header-read time). Clear the write deadline; the
		// body is bounded below.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear cover upload write deadline unsupported", "err", err)
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCoverUploadBytes+1024)
		if err := r.ParseMultipartForm(maxCoverUploadBytes + 1024); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "image too large (max 20MB)")
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

		file, _, err := r.FormFile("cover")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "missing cover file field")
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				slog.Error("close uploaded cover failed", "err", err)
			}
		}()

		data, err := io.ReadAll(io.LimitReader(file, maxCoverUploadBytes+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "failed to read cover upload")
			return
		}
		if int64(len(data)) > maxCoverUploadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "image too large (max 20MB)")
			return
		}

		// Normalize the upload to the same resized JPEG the importer produces. The
		// bytes are both embedded into the EPUB and written to the sidecar store.
		jpegData, err := library.EncodeCoverJPEG(r.Context(), id, data)
		if err != nil {
			if errors.Is(err, library.ErrCoverSkipped) {
				writeError(w, http.StatusBadRequest, "invalid", "image dimensions too large")
				return
			}
			// A decode failure means the upload was not a supported image; that is
			// user error, so surface it as a 400 rather than a 500.
			slog.Warn("process uploaded cover failed", "book", id, "err", err)
			writeError(w, http.StatusBadRequest, "invalid", "could not process image (use JPEG, PNG, or WebP)")
			return
		}

		// Cover decoding is deliberately outside this lock: it can be expensive
		// and does not inspect or mutate the EPUB. Serialize only the edit/file
		// generation work, then refresh the book snapshot before preparing it.
		pd.libraryScanMu.RLock()
		defer pd.libraryScanMu.RUnlock()
		pd.bookEditMu.Lock()
		defer pd.bookEditMu.Unlock()
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		var prepared preparedBookFile
		embedInFile := book.FilePath != ""
		if embedInFile {
			prepared, err = prepareBookFile(r.Context(), pd, book, epub.MetadataEdit{CoverJPEG: jpegData})
			if err != nil {
				if writeBookEditConflict(w, err) {
					return
				}
				slog.Error("embed cover into epub failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
				return
			}
		}
		defer prepared.cleanup()

		// Sidecar-only edits need this gate too: cover readers pair bytes with
		// the cached validator, and deletion must not interleave publication.
		pd.bookReplaceMu.Lock()
		defer pd.bookReplaceMu.Unlock()
		if err := checkBookEditSnapshot(pd, book); err != nil {
			writeBookEditConflict(w, err)
			return
		}
		// Back up the canonical destination the library writer will replace,
		// not a potentially different legacy cover_path stored in the row.
		coverBackup, err := backupBookEditFile(r.Context(), pd.coverRoot, library.CoverRelPath(id))
		if err != nil {
			slog.Error("back up cover failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "io_error", "failed to save cover")
			return
		}
		defer coverBackup.cleanup()

		// A pinned reader conflict leaves both published files untouched.
		if embedInFile {
			if err := replaceBookFile(pd, book.FilePath, prepared.tmpPath); err != nil {
				if writeBookEditConflict(w, err) {
					return
				}
				slog.Error("embed cover into epub failed", "book", id, "err", err)
				writeError(w, http.StatusInternalServerError, "edit_failed", "failed to update the book file")
				return
			}
		}

		coverPath, err := library.WriteCoverImageJPEG(pd.LibPath, id, jpegData)
		if err != nil {
			slog.Error("save uploaded cover failed", "book", id, "err", err)
			if !rollbackBookEdit(pd, id, &coverBackup, &prepared.original) {
				writeError(w, http.StatusInternalServerError, "io_error", "failed to restore the original book; see server log")
				return
			}
			writeError(w, http.StatusInternalServerError, "io_error", "failed to save cover")
			return
		}

		// Both the sidecar cover and (when embedding) the EPUB itself are already
		// written, so the row must follow even if the client is gone. Abandoning
		// the update here leaves updated_at unbumped, which keeps the cover ETag
		// identical to the one browsers already hold — so every client keeps
		// 304-ing its way to the previous image indefinitely.
		commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(r.Context()), bookEditCommitTimeout)
		defer cancelCommit()

		if embedInFile {
			err = pd.DB.UpdateBookCoverAndFileContext(commitCtx, id, coverPath, prepared.hash, prepared.size)
		} else {
			err = pd.DB.UpdateBookCoverContext(commitCtx, id, coverPath)
		}
		if err != nil {
			if !rollbackBookEdit(pd, id, &coverBackup, &prepared.original) {
				writeError(w, http.StatusInternalServerError, "io_error", "failed to restore the original book; see server log")
				return
			}
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("update book cover failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update cover")
			return
		}

		updated, err := refreshBookCache(commitCtx, pd, id)
		if err != nil {
			slog.Error("reload book after cover update failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "cover updated but failed to reload")
			return
		}
		writeJSON(w, http.StatusOK, bookResponseFromRecord(r, pd, updated))
	}
}
