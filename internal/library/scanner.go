package library

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

type Scanner struct {
	libraryPath string
	db          *storage.DB

	mu      sync.Mutex
	current *scanCall
}

// scanCall represents a single in-flight library scan. Concurrent ScanNow
// callers share one scanCall instead of each launching a redundant walk.
type scanCall struct {
	done chan struct{}
	ids  []string
	err  error
}

func NewScanner(libraryPath string, db *storage.DB) *Scanner {
	return &Scanner{libraryPath: libraryPath, db: db}
}

// ScanNow walks the library directory and imports any new EPUBs into the DB,
// returning the IDs imported during the scan.
//
// Scans are single-flighted per Scanner: if a scan is already running when
// ScanNow is called, the caller waits for the in-flight scan and shares its
// result instead of starting a second walk. This coalesces overlapping rescans
// (e.g. a burst of POST /api/library/rescan, or a manual rescan racing the
// automatic scan on profile open) so the library is never walked and re-hashed
// by two goroutines at once.
//
// A waiting caller still observes its own ctx cancellation. Note that if the
// leader's scan is canceled mid-walk, waiters receive that error too; scans are
// safe to retry.
func (s *Scanner) ScanNow(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	if call := s.current; call != nil {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.ids, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &scanCall{done: make(chan struct{})}
	s.current = call
	s.mu.Unlock()

	call.ids, call.err = s.scan(ctx)

	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()
	close(call.done)

	return call.ids, call.err
}

// scan performs the actual library walk and import. It is only ever invoked by
// ScanNow, which serializes and coalesces concurrent callers. It returns the
// IDs of books imported during this scan so callers can update their in-memory
// caches (this function does not touch the in-memory cache). It respects ctx
// cancellation so slow scans don't block profile opens indefinitely.
func (s *Scanner) scan(ctx context.Context) ([]string, error) {
	slog.Info("scanning library", "path", s.libraryPath)

	paths, err := s.collectEPUBPaths(ctx)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		slog.Info("scan complete", "imported", 0)
		return nil, nil
	}

	// Load the existing path->id map and the ignored-path set once so each
	// importFile resolves the common "already imported at this path" / "ignored"
	// cases from memory instead of issuing two indexed point reads per file. The
	// snapshot is point-in-time at scan start, which is safe: WalkDir visits each
	// path once (no two workers race the same path) and content-level dedup via
	// GetBookIDByHashContext is still a live query inside importFile.
	snap, err := s.loadDedupSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	// Hashing, EPUB parsing, and cover decode/resize/encode dominate a
	// first-time import and are independent per file, so fan them out across a
	// bounded worker pool sized to the CPU count. The walk itself stays
	// single-threaded (WalkDir is not safe to call concurrently); only the heavy
	// per-file import work runs in parallel. storage.DB already serializes
	// writes and pools reads, so concurrent importFile calls need no extra
	// locking beyond the importedIDs append below.
	workers := min(runtime.GOMAXPROCS(0), len(paths))

	var (
		mu          sync.Mutex
		importedIDs = make([]string, 0, len(paths))
		wg          sync.WaitGroup
	)
	pathCh := make(chan string)

	for range workers {
		wg.Go(func() {
			for filePath := range pathCh {
				id, imported, importErr := s.importFile(ctx, filePath, "", snap)
				if importErr != nil {
					// When the scan's ctx is canceled, in-flight imports fail with
					// context errors as expected teardown — not per-file failures —
					// so suppress the warning in that case.
					if ctx.Err() == nil {
						slog.Warn("scan import failed", "path", filePath, "err", importErr)
					}
					continue
				}
				if imported {
					mu.Lock()
					importedIDs = append(importedIDs, id)
					mu.Unlock()
				}
			}
		})
	}

	// This goroutine is the sole sender on pathCh, so it owns closing it. The
	// select lets it stop feeding promptly on cancellation even when every
	// worker is mid-decode (a bare send would block until a worker frees up);
	// in-flight workers then unwind quickly because importFile observes ctx
	// through its DB and hashing calls.
	for _, filePath := range paths {
		select {
		case pathCh <- filePath:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(pathCh)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		// Return the IDs already imported (and committed) alongside the error so a
		// canceled scan still lets the caller warm its cache for them. The next
		// scan's dedup snapshot would otherwise treat these rows as known and never
		// re-report them, leaving the books absent from the cache until a reopen.
		slog.Info("scan canceled", "imported", len(importedIDs), "err", err)
		return importedIDs, err
	}

	slog.Info("scan complete", "imported", len(importedIDs))
	return importedIDs, nil
}

// collectEPUBPaths walks the library directory and returns the paths of all
// candidate .epub files, skipping dotfiles and dot-directories. It is the
// single-threaded discovery phase that feeds scan's worker pool and honors ctx
// cancellation between entries.
func (s *Scanner) collectEPUBPaths(ctx context.Context) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(s.libraryPath, func(filePath string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Warn("scan access failed", "path", filePath, "err", walkErr)
			return nil
		}
		if dirEntry == nil {
			return nil
		}

		// Cancellation is checked once per file entry, not per directory.
		// A directory with many EPUBs may process several files before the
		// check fires; this is acceptable given the low per-file overhead.
		if err := ctx.Err(); err != nil {
			return err
		}

		if dirEntry.IsDir() {
			if strings.HasPrefix(dirEntry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// filepath.Ext returns a substring (no allocation) and EqualFold compares
		// without lowercasing, so the suffix test stays alloc-free per entry —
		// unlike strings.ToLower, which copies any name containing an uppercase rune.
		if strings.HasPrefix(dirEntry.Name(), ".") || !strings.EqualFold(filepath.Ext(dirEntry.Name()), ".epub") {
			return nil
		}

		paths = append(paths, filePath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk library %q: %w", s.libraryPath, err)
	}
	return paths, nil
}

// dedupSnapshot is a point-in-time view of which library paths are already
// imported or ignored. The scan loads it once so importFile can resolve the
// common "already known" cases from memory instead of per-file DB reads. Its
// maps are read-only after construction and so are safe for concurrent reads
// across the scan worker pool.
type dedupSnapshot struct {
	existingByPath map[string]string   // absolute file path -> book ID
	ignored        map[string]struct{} // ignored absolute file paths
}

// loadDedupSnapshot builds a dedupSnapshot from the current DB state with two
// bulk queries, replacing the previous two point reads per scanned file.
func (s *Scanner) loadDedupSnapshot(ctx context.Context) (*dedupSnapshot, error) {
	bookPaths, err := s.db.ListBookPathsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("load existing book paths: %w", err)
	}
	ignoredPaths, err := s.db.ListIgnoredPathsContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("load ignored paths: %w", err)
	}

	snap := &dedupSnapshot{
		existingByPath: make(map[string]string, len(bookPaths)),
		ignored:        make(map[string]struct{}, len(ignoredPaths)),
	}
	for _, bp := range bookPaths {
		snap.existingByPath[bp.FilePath] = bp.ID
	}
	for _, path := range ignoredPaths {
		snap.ignored[path] = struct{}{}
	}
	return snap, nil
}

// importFile imports a single EPUB. When snap is non-nil (the scan path) the
// ignored/known-path pre-checks are served from the snapshot; when nil (a
// one-off ImportFile) they hit the DB directly.
func (s *Scanner) importFile(ctx context.Context, filePath string, knownHash string, snap *dedupSnapshot) (id string, imported bool, err error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", false, fmt.Errorf("abs path: %w", err)
	}

	if snap != nil {
		if _, ok := snap.ignored[absPath]; ok {
			return "", false, nil
		}
		if existingID, ok := snap.existingByPath[absPath]; ok {
			return existingID, false, nil
		}
	} else {
		ignored, err := s.db.IsFileIgnoredContext(ctx, absPath)
		if err != nil {
			return "", false, fmt.Errorf("check ignored: %w", err)
		}
		if ignored {
			return "", false, nil
		}
		if existingID, found, err := s.db.BookExistsByPathContext(ctx, absPath); err != nil {
			return "", false, fmt.Errorf("check existing by path: %w", err)
		} else if found {
			return existingID, false, nil
		}
	}

	var hash string
	var fileSize int64

	if knownHash != "" {
		hash = knownHash

		fileInfo, err := os.Stat(absPath)
		if err != nil {
			return "", false, fmt.Errorf("stat file: %w", err)
		}
		fileSize = fileInfo.Size()
	} else {
		hash, fileSize, err = contentHash(ctx, absPath)
		if err != nil {
			return "", false, fmt.Errorf("hash file: %w", err)
		}
	}

	existingID, existingPath, found, err := s.db.GetBookIDByHashContext(ctx, hash)
	if err != nil {
		return "", false, fmt.Errorf("check existing by hash: %w", err)
	}
	if found {
		// Reconcile the stored path: if the file has been moved, renamed, or
		// copied into a cloned profile, update the DB so future reads use the
		// correct location. Failures are non-fatal — the book is still usable
		// at its old path until the next successful reconciliation.
		if existingPath != absPath {
			if updateErr := s.db.UpdateBookFilePathContext(ctx, existingID, absPath); updateErr != nil {
				slog.Warn("reconcile book path after hash match failed",
					"id", existingID, "old", existingPath, "new", absPath, "err", updateErr)
			}
		}
		return existingID, false, nil
	}

	zr, err := zip.OpenReader(absPath)
	if err != nil {
		return "", false, fmt.Errorf("open zip: %w", err)
	}
	defer func() {
		if closeErr := zr.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close epub: %w", closeErr)
		}
	}()

	meta, err := epub.Parse(&zr.Reader)
	if err != nil {
		return "", false, fmt.Errorf("parse epub: %w", err)
	}

	id = generateID(absPath, hash)

	spineJSON, err := json.Marshal(meta.Spine)
	if err != nil {
		return "", false, fmt.Errorf("marshal spine: %w", err)
	}

	tocJSON, err := json.Marshal(meta.TOC)
	if err != nil {
		return "", false, fmt.Errorf("marshal toc: %w", err)
	}

	record := storage.BookRecord{
		BookSummary: storage.BookSummary{
			ID:           id,
			Title:        meta.Title,
			Author:       meta.Author,
			Language:     meta.Language,
			Publisher:    meta.Publisher,
			Description:  meta.Description,
			PubDate:      meta.PubDate,
			ISBN:         meta.ISBN,
			FilePath:     absPath,
			FileHash:     hash,
			FileSize:     fileSize,
			Direction:    meta.Direction,
			ChapterCount: len(meta.Spine),
		},
		SpineJSON: string(spineJSON),
		TocJSON:   string(tocJSON),
	}

	canonicalID, err := s.db.InsertBookContext(ctx, record)
	if err != nil {
		return "", false, fmt.Errorf("insert book: %w", err)
	}
	// If a concurrent import of identical content won the race, InsertBookContext
	// returns that row's ID instead of our proposed one (generateID is
	// path-dependent, so the winner's ID never matches ours). That import owns the
	// row and its cover, so don't re-report it as newly imported or redo the cover
	// decode.
	if canonicalID != id {
		return canonicalID, false, nil
	}
	id = canonicalID

	if meta.CoverPath != "" {
		if coverErr := extractCover(ctx, s.libraryPath, id, &zr.Reader, meta.CoverPath); coverErr != nil {
			slog.Warn("cover extraction failed", "title", meta.Title, "err", coverErr)
		} else {
			coverRelPath := filepath.Join(".sayumi", "covers", id+".jpg")
			if updateErr := s.db.UpdateBookCoverContext(ctx, id, coverRelPath); updateErr != nil {
				slog.Warn("update cover path failed", "book", id, "err", updateErr)
			}
		}
	}

	slog.Info("imported book", "title", meta.Title, "author", meta.Author, "chapters", len(meta.Spine))
	return id, true, nil
}

// CheckDuplicate reports whether filePath is already in the library by content hash.
func (s *Scanner) CheckDuplicate(ctx context.Context, filePath string) (existingID string, hash string, isDuplicate bool) {
	h, _, err := contentHash(ctx, filePath)
	if err != nil {
		return "", "", false
	}

	existingID, _, found, err := s.db.GetBookIDByHashContext(ctx, h)
	if err != nil {
		slog.Warn("duplicate check failed", "path", filePath, "err", err)
		return "", h, false
	}
	if !found {
		return "", h, false
	}

	return existingID, h, true
}

// ImportFile imports a single EPUB file into the library, returning its book ID.
func (s *Scanner) ImportFile(ctx context.Context, filePath string, knownHash string) (string, error) {
	id, _, err := s.importFile(ctx, filePath, knownHash, nil)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	return "", errors.New("book was not imported and could not be found")
}

// hashBufPool reuses 1 MiB read buffers across the scan worker pool so each
// contentHash call avoids allocating (and later GC-ing) its own large buffer.
var hashBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 1<<20) // 1 MiB
		return &buf
	},
}

func contentHash(ctx context.Context, filePath string) (hash string, size int64, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open file for hashing: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close file: %w", closeErr)
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat file: %w", err)
	}

	hasher := sha256.New()
	bufPtr := hashBufPool.Get().(*[]byte)
	defer hashBufPool.Put(bufPtr)
	buf := *bufPtr
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, writeErr := hasher.Write(buf[:n]); writeErr != nil {
				return "", 0, fmt.Errorf("hash file content: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, fmt.Errorf("hash file content: %w", readErr)
		}
	}

	return hex.EncodeToString(hasher.Sum(nil)), info.Size(), nil
}

func generateID(filePath, contentHash string) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(filePath))
	_, _ = hasher.Write([]byte(contentHash))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
