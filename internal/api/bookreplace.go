package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

var errReplacementUnreadable = errors.New("replacement is not a readable EPUB")

// parseReplacementEPUB reads the package metadata of a staged replacement. A
// book with no spine cannot hold a reading position, so it is refused like any
// other unreadable upload.
func parseReplacementEPUB(path string) (epub.BookMeta, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return epub.BookMeta{}, fmt.Errorf("%w: %w", errReplacementUnreadable, err)
	}
	defer func() {
		if err := zr.Close(); err != nil {
			slog.Error("close replacement epub failed", "err", err)
		}
	}()
	meta, err := epub.Parse(&zr.Reader)
	if err != nil {
		return epub.BookMeta{}, fmt.Errorf("%w: %w", errReplacementUnreadable, err)
	}
	if len(meta.Spine) == 0 {
		return epub.BookMeta{}, fmt.Errorf("%w: empty spine", errReplacementUnreadable)
	}
	return meta, nil
}

// buildChapterRemap matches the previous generation's spine documents to the
// replacement's by href. Serial exports (a novel re-downloaded with more
// chapters) keep their per-chapter file names, so this follows a chapter that
// moved because front matter was added or an arc was re-split, not just the
// append-only case where every index already lines up.
//
// When no href matches at all the generator renamed every document; the old
// index is then the best available guess, and Apply drops the anchors because
// they no longer address the same document.
func buildChapterRemap(oldSpine, newSpine []epub.SpineEntry) storage.ChapterRemap {
	remap := storage.ChapterRemap{
		Moves:    make([]storage.ChapterMove, len(oldSpine)),
		NewCount: len(newSpine),
	}
	newIndex := make(map[string]int, len(newSpine))
	for i, entry := range newSpine {
		if _, dup := newIndex[entry.Href]; !dup {
			newIndex[entry.Href] = i
		}
	}
	matched := false
	for i, entry := range oldSpine {
		if j, ok := newIndex[entry.Href]; ok {
			remap.Moves[i] = storage.ChapterMove{Index: j, KeepAnchor: true}
			matched = true
		} else {
			remap.Moves[i] = storage.ChapterMove{Index: -1}
		}
	}
	if !matched {
		for i := range remap.Moves {
			remap.Moves[i] = storage.ChapterMove{Index: -1}
		}
	}
	return remap
}

// carryBookIdentity writes the library's title/author into the replacement
// when the new file disagrees, so the row and the bytes keep describing the
// same book (the invariant metadata edits maintain). An empty library value is
// left to the file. Returns "" when nothing needed rewriting.
func carryBookIdentity(path string, book storage.BookRecord, meta epub.BookMeta) (string, error) {
	var edit epub.MetadataEdit
	if book.Title != "" && book.Title != meta.Title {
		edit.Title = &book.Title
	}
	if book.Author != "" && book.Author != meta.Author {
		edit.Author = &book.Author
	}
	if edit.Title == nil && edit.Author == nil {
		return "", nil
	}
	return epub.RewriteBook(path, edit)
}

type preparedReplacement struct {
	remap    storage.ChapterRemap
	original bookEditBackup
	root     *os.Root
}

func (p *preparedReplacement) cleanup() {
	p.original.cleanup()
	if p.root != nil {
		if err := p.root.Close(); err != nil {
			slog.Error("close book replace root failed", "err", err)
		}
	}
}

// prepareReplacement reads the current spine and backs up the current file
// under the read gate, like prepareBookFile: deletion is excluded while the
// copy is open, chapter readers are not.
func prepareReplacement(ctx context.Context, pd *profileDeps, book storage.BookRecord, newSpine []epub.SpineEntry) (preparedReplacement, error) {
	pd.bookReplaceMu.RLock()
	defer pd.bookReplaceMu.RUnlock()
	if err := checkBookEditSnapshot(pd, book); err != nil {
		return preparedReplacement{}, err
	}
	oldSpine, found, err := pd.Books.GetSpine(ctx, book.ID)
	if err != nil {
		return preparedReplacement{}, fmt.Errorf("load current spine: %w", err)
	}
	if !found {
		return preparedReplacement{}, storage.ErrNotFound
	}
	prepared := preparedReplacement{remap: buildChapterRemap(oldSpine, newSpine)}
	prepared.root, err = os.OpenRoot(filepath.Dir(book.FilePath))
	if err == nil {
		prepared.original, err = backupBookEditFile(ctx, prepared.root, filepath.Base(book.FilePath))
		if err == nil && prepared.original.tempPath == "" {
			err = errors.New("source disappeared during replace preparation")
		}
	}
	if err != nil {
		prepared.cleanup()
		return preparedReplacement{}, fmt.Errorf("back up epub: %w", err)
	}
	return prepared, nil
}

// replaceBookHandler handles POST /api/books/{id}/replace: installs a newer
// EPUB (multipart field "epub") under the existing book ID. The ID is what
// progress, bookmarks and the flair hang off, so keeping it keeps them; the
// stored positions are remapped into the new chapter numbering in the same
// transaction that publishes the new spine. Title, author and the displayed
// cover are the user's and survive; the remaining metadata follows the file.
//
// POST rather than PUT: the client retries idempotent verbs, and a retry would
// resend up to 100 MB after a server that may already have committed.
func replaceBookHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		if _, ok := pd.Books.Get(id); !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		// Same reasoning as uploadBookHandler: the body can outlast the
		// header-armed write deadline. MaxBytesReader bounds it instead.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear replace write deadline unsupported", "err", err)
		}

		tmpPath, _, ok := stageMultipartEPUB(w, r, pd.LibPath, maxUploadSize)
		if !ok {
			return
		}
		// After a successful swap the staged name no longer exists.
		defer removeIfExists(tmpPath)

		if err := validateEPUB(tmpPath); err != nil {
			// Fixed message: the error text carries the absolute staging path.
			slog.Warn("replacement file rejected", "book", id, "err", err)
			writeError(w, http.StatusBadRequest, "invalid", "file is not a valid EPUB")
			return
		}

		// Serialize with metadata/cover edits and keep rescans out, then re-read
		// the book: its title/author decide whether the upload is rewritten.
		pd.libraryScanMu.RLock()
		defer pd.libraryScanMu.RUnlock()
		pd.bookEditMu.Lock()
		defer pd.bookEditMu.Unlock()
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}
		if book.FilePath == "" {
			writeError(w, http.StatusUnprocessableEntity, "no_file", "book has no source file to replace")
			return
		}

		meta, err := parseReplacementEPUB(tmpPath)
		if err != nil {
			slog.Warn("replacement file rejected", "book", id, "err", err)
			writeError(w, http.StatusBadRequest, "invalid", "file is not a valid EPUB")
			return
		}

		installPath := tmpPath
		rewritten, err := carryBookIdentity(tmpPath, book, meta)
		switch {
		case err != nil:
			// Cosmetic only: the row keeps the library title either way. An OPF
			// the splicer cannot handle must not block a content update.
			slog.Warn("carry title/author into replacement failed; installing as uploaded", "book", id, "err", err)
		case rewritten != "":
			installPath = rewritten
			defer removeIfExists(rewritten)
		}

		hash, size, err := library.HashFile(r.Context(), installPath)
		if requestContextDone(r, err) {
			return
		}
		if err != nil {
			slog.Error("hash replacement failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to read the uploaded file")
			return
		}
		if hash == book.FileHash {
			// Byte-identical to what is installed: nothing to move.
			writeJSON(w, http.StatusOK, bookResponseFromRecord(r, pd, book))
			return
		}

		spineJSON, err := json.Marshal(meta.Spine)
		if err != nil {
			slog.Error("marshal replacement spine failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to read the uploaded file")
			return
		}
		tocJSON, err := json.Marshal(meta.TOC)
		if err != nil {
			slog.Error("marshal replacement toc failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to read the uploaded file")
			return
		}

		prepared, err := prepareReplacement(r.Context(), pd, book, meta.Spine)
		if err != nil {
			if writeBookEditConflict(w, err) || requestContextDone(r, err) {
				return
			}
			slog.Error("prepare book replacement failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to replace the book file")
			return
		}
		defer prepared.cleanup()

		pd.bookReplaceMu.Lock()
		defer pd.bookReplaceMu.Unlock()
		if err := checkBookEditSnapshot(pd, book); err != nil {
			writeBookEditConflict(w, err)
			return
		}
		if err := replaceBookFile(pd, book.FilePath, installPath); err != nil {
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("install replacement epub failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "edit_failed", "failed to replace the book file")
			return
		}

		// The new bytes are live: the row (and the positions keyed to its spine)
		// must follow even if the client hung up. See bookEditCommitTimeout.
		commitCtx, cancelCommit := context.WithTimeout(context.WithoutCancel(r.Context()), bookEditCommitTimeout)
		defer cancelCommit()

		err = pd.DB.ReplaceBookFileContext(commitCtx, id, storage.BookFileReplacement{
			Language:    meta.Language,
			Publisher:   meta.Publisher,
			Description: meta.Description,
			PubDate:     meta.PubDate,
			ISBN:        meta.ISBN,
			Direction:   meta.Direction,
			FileHash:    hash,
			FileSize:    size,
			SpineJSON:   string(spineJSON),
			TocJSON:     string(tocJSON),
			Remap:       prepared.remap,
		})
		if err != nil {
			if !rollbackBookEdit(pd, id, &prepared.original) {
				writeError(w, http.StatusInternalServerError, "io_error", "failed to restore the original book; see server log")
				return
			}
			if writeBookEditConflict(w, err) {
				return
			}
			slog.Error("update replaced book failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to replace the book file")
			return
		}

		// Staged progress is keyed by the old numbering too; left alone, the
		// next flush would overwrite the remapped row with a stale chapter.
		pd.Progress.remapBook(id, prepared.remap)

		updated, err := refreshBookCache(commitCtx, pd, id)
		if err != nil {
			slog.Error("reload book after replacement failed", "book", id, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "book replaced but failed to reload")
			return
		}
		slog.Info("replaced book file", "book", id,
			"chapters_before", book.ChapterCount, "chapters_after", updated.ChapterCount)
		writeJSON(w, http.StatusOK, bookResponseFromRecord(r, pd, updated))
	}
}

func removeIfExists(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove temp file failed", "path", path, "err", err)
	}
}
