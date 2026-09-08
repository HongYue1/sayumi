package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

type profileDeps struct {
	DB      *storage.DB
	Books   *storage.BookCache
	Store   *epub.EPUBStore
	Scanner *library.Scanner
	LibPath string

	// Progress coalesces frequent reading-position writes per (book, user) and
	// flushes them on a timer, so the WAL-fsync hot path is not hit on every
	// scroll event. Owned by this profile; drained in closeProfile before
	// DB.Close.
	Progress *progressCoalescer

	// coverRoot is a long-lived os.Root anchored at LibPath, opened once when the
	// profile opens and reused by every cover request. It replaces a per-request
	// os.OpenRoot (a directory open + fd) on the cover-serving path while keeping
	// the same sandboxed-traversal guarantee. os.Root.Open is safe for concurrent
	// use; the root is closed in closeProfile after refs drain to zero, so no
	// in-flight request can touch a closed root.
	coverRoot *os.Root

	// libraryScanMu keeps upload/edit/delete mutations out of a rescan's entire
	// walk-through-cache-publication cycle. Mutations share the read side;
	// rescans take the write side. This is separate from bookReplaceMu so a
	// large scan does not block chapter/download readers, and ordinary mutations
	// can still run concurrently. Acquire it before bookEditMu/bookReplaceMu.
	// The initial profile scan runs before these dependencies are shared.
	libraryScanMu sync.RWMutex

	// bookEditMu serializes in-place EPUB edits without blocking chapter reads
	// while the replacement file is being built and hashed. bookReplaceMu is
	// held for the much shorter commit window (rename through DB/cache refresh):
	// chapter/download/gofile handlers take its read side before snapshotting
	// BookCache, while deletion takes its write side through file removal. This
	// keeps active readers on one complete generation and prevents replacing or
	// removing an EPUB while Windows still has a reader handle open.
	bookEditMu    sync.Mutex
	bookReplaceMu sync.RWMutex

	lifetimeMu   sync.Mutex
	lifetimeCond *sync.Cond
	refs         int
	closing      bool
}

// epubStoreCacheSize is the maximum number of EPUB ZIP files held open
// simultaneously per profile. Each open ZIP occupies an OS file descriptor
// and its central-directory index in memory. 10 covers a typical reading
// session where a user switches between a small number of books.
const epubStoreCacheSize = 10

func newProfileDeps(
	db *storage.DB,
	books *storage.BookCache,
	scanner *library.Scanner,
	libPath string,
) *profileDeps {
	pd := &profileDeps{
		DB:      db,
		Books:   books,
		Store:   epub.NewStore(epubStoreCacheSize),
		Scanner: scanner,
		LibPath: libPath,
	}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	pd.Progress = newProgressCoalescer(db, progressFlushInterval, progressMaxPending)
	return pd
}

func (pd *profileDeps) acquire() bool {
	pd.lifetimeMu.Lock()
	defer pd.lifetimeMu.Unlock()

	if pd.closing {
		return false
	}
	pd.refs++
	return true
}

func (pd *profileDeps) release() {
	pd.lifetimeMu.Lock()
	defer pd.lifetimeMu.Unlock()

	if pd.refs <= 0 {
		slog.Error("profile release called with non-positive refs", "path", pd.LibPath, "refs", pd.refs)
		return
	}

	pd.refs--
	if pd.refs == 0 {
		pd.lifetimeCond.Broadcast()
	}
}

func (pd *profileDeps) markClosingAndWait() {
	pd.lifetimeMu.Lock()
	defer pd.lifetimeMu.Unlock()

	pd.closing = true
	for pd.refs > 0 {
		pd.lifetimeCond.Wait()
	}
}

type ProfileManager struct {
	libraryRoot string
	closeOnce   sync.Once
	mu          sync.Mutex
	cond        *sync.Cond
	open        map[string]*profileDeps
	blocked     map[string]bool
	opening     map[string]bool
	closed      bool

	// Fixed before the manager is shared. Tests can pause a cold open at the
	// publication boundary without relying on scan duration or global hooks.
	loadProfile func(context.Context, string) (*profileDeps, error)
}

func NewProfileManager(libraryRoot string) *ProfileManager {
	pm := &ProfileManager{
		libraryRoot: libraryRoot,
		open:        make(map[string]*profileDeps),
		blocked:     make(map[string]bool),
		opening:     make(map[string]bool),
	}
	pm.cond = sync.NewCond(&pm.mu)
	pm.loadProfile = pm.openProfile
	return pm
}

func (pm *ProfileManager) profileDir(name string) string {
	return filepath.Join(pm.libraryRoot, name)
}

func normalizeProfileNames(profileNames []string) []string {
	names := slices.DeleteFunc(slices.Clone(profileNames), func(s string) bool { return s == "" })
	slices.Sort(names)
	return slices.Compact(names)
}

func (pm *ProfileManager) closeProfile(profileName string, pd *profileDeps) {
	if pd == nil {
		return
	}

	pd.markClosingAndWait()
	// Refs have drained to zero, so no handler can be mid-stage; flush any
	// buffered progress writes before the DB is closed underneath them.
	pd.Progress.stop()
	pd.Store.Close()
	if pd.coverRoot != nil {
		if err := pd.coverRoot.Close(); err != nil {
			slog.Error("profile cover root close failed", "profile", profileName, "err", err)
		}
	}
	if err := pd.DB.Close(); err != nil {
		slog.Error("profile db close failed", "profile", profileName, "err", err)
	}
	slog.Info("profile closed", "profile", profileName)
}

func (pm *ProfileManager) openProfile(ctx context.Context, profileName string) (*profileDeps, error) {
	libPath := pm.profileDir(profileName)
	libInfo, err := os.Stat(libPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("profile dir %q: %w", profileName, err)
		}
		return nil, fmt.Errorf("stat profile dir %q: %w", profileName, err)
	}
	if !libInfo.IsDir() {
		return nil, fmt.Errorf("profile dir %q is not a directory", profileName)
	}

	// Diagnostics: time each stage of profile open so the ~60 ms first-request
	// latency can be attributed to DB open, library scan, or book-cache build.
	// These lines are Debug-level and only surface under --debug.
	openStart := time.Now()
	db, err := storage.Open(libPath)
	if err != nil {
		return nil, fmt.Errorf("open db for %q: %w", profileName, err)
	}
	dbOpenDur := time.Since(openStart)

	scanStart := time.Now()
	scanner := library.NewScanner(libPath, db)
	if _, err := scanner.ScanNow(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("scan library for %q: %w", profileName, err)
	}
	scanDur := time.Since(scanStart)

	cacheStart := time.Now()
	books, err := storage.NewBookCache(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("build book cache for %q: %w", profileName, err)
	}
	cacheDur := time.Since(cacheStart)

	slog.Debug(
		"profile open timing",
		"profile", profileName,
		"db_open", dbOpenDur,
		"scan", scanDur,
		"book_cache", cacheDur,
		"total", time.Since(openStart),
	)

	coverRoot, err := os.OpenRoot(libPath)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open cover root for %q: %w", profileName, err)
	}

	pd := newProfileDeps(db, books, scanner, libPath)
	pd.coverRoot = coverRoot
	return pd, nil
}

func (pm *ProfileManager) lockProfiles(ctx context.Context, profileNames ...string) (func(), error) {
	names := normalizeProfileNames(profileNames)
	toClose := make(map[string]*profileDeps, len(names))

	// Wake the cond if the caller's context is canceled so the wait loop
	// below can observe ctx.Err() rather than blocking indefinitely.
	stopWake := context.AfterFunc(ctx, func() {
		pm.mu.Lock()
		pm.cond.Broadcast()
		pm.mu.Unlock()
	})
	defer stopWake()

	pm.mu.Lock()
	// Every name must be observed free in ONE uninterrupted critical section
	// before any is marked. cond.Wait releases pm.mu, so checking the names in
	// sequence and never revisiting the earlier ones let a second caller claim a
	// name this one had already passed: both would then believe they held it
	// exclusively, and whichever finished first would delete the other's block.
	// Re-scanning from the start after every wait keeps the claim atomic.
	for {
		// Check even when every name is free, including just after a wake-up.
		// Cancellation must not evict a profile merely because it won a race
		// with the operation that was keeping the caller parked.
		if pm.closed {
			pm.mu.Unlock()
			return nil, os.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			pm.mu.Unlock()
			return nil, err
		}
		allFree := true
		for _, name := range names {
			if pm.blocked[name] || pm.opening[name] {
				allFree = false
				break
			}
		}
		if allFree {
			break
		}
		pm.cond.Wait()
	}

	for _, name := range names {
		pm.blocked[name] = true
		if pd, ok := pm.open[name]; ok {
			delete(pm.open, name)
			toClose[name] = pd
		}
	}
	pm.mu.Unlock()

	// Once detached, these dependencies must finish draining even if the
	// request is canceled. Releasing the names sooner would let another open
	// or filesystem mutation race the still-borrowed DB and file handles.
	for _, name := range names {
		pm.closeProfile(name, toClose[name])
	}

	unlock := func() {
		pm.mu.Lock()
		for _, name := range names {
			delete(pm.blocked, name)
		}
		pm.cond.Broadcast()
		pm.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// Get opens a profile (or returns the already-open one), incrementing its
// reference count. The caller must call pd.release() when done. ctx is
// forwarded to the initial library scan so a canceled request does not
// leave the server stuck waiting for a slow scan.
func (pm *ProfileManager) Get(ctx context.Context, profileName string) (*profileDeps, error) {
	// Wake the cond if the caller's context is canceled so blocked waiters
	// observe ctx.Err() instead of waiting for an unrelated Broadcast.
	stopWake := context.AfterFunc(ctx, func() {
		pm.mu.Lock()
		pm.cond.Broadcast()
		pm.mu.Unlock()
	})
	defer stopWake()

	pm.mu.Lock()
	for {
		if pm.closed {
			pm.mu.Unlock()
			return nil, os.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			pm.mu.Unlock()
			return nil, err
		}
		if !pm.blocked[profileName] && !pm.opening[profileName] {
			break
		}
		pm.cond.Wait()
	}

	if pd, ok := pm.open[profileName]; ok {
		if pd.acquire() {
			pm.mu.Unlock()
			return pd, nil
		}
		delete(pm.open, profileName)
	}

	pm.opening[profileName] = true
	pm.mu.Unlock()
	pd, err := pm.loadProfile(ctx, profileName)

	pm.mu.Lock()
	if err == nil {
		if pm.closed {
			err = os.ErrClosed
		} else {
			err = ctx.Err()
		}
	}
	if err != nil {
		// Keep ownership of opening through cleanup. Waking a clone/delete
		// or CloseAll before these handles close would expose a live DB.
		pm.mu.Unlock()
		pm.closeProfile(profileName, pd)
		pm.mu.Lock()
		delete(pm.opening, profileName)
		pm.cond.Broadcast()
		pm.mu.Unlock()
		return nil, err
	}

	// The opening marker excludes other openers and profile lockers. Publish
	// the fresh dependencies and their first reference in one critical section.
	pd.acquire()
	pm.open[profileName] = pd
	delete(pm.opening, profileName)
	pm.cond.Broadcast()
	pm.mu.Unlock()

	slog.Info("profile opened", "profile", profileName, "books", pd.Books.Len())
	return pd, nil
}

func (pm *ProfileManager) Evict(profileName string) {
	if unlockProfiles, err := pm.lockProfiles(context.Background(), profileName); err == nil {
		unlockProfiles()
	}
}

func (pm *ProfileManager) FindBook(bookID string) (*profileDeps, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.closed {
		return nil, false
	}
	for profileName, pd := range pm.open {
		if pm.blocked[profileName] {
			continue
		}
		if _, ok := pd.Books.Get(bookID); !ok {
			continue
		}
		if !pd.acquire() {
			continue
		}
		return pd, true
	}
	return nil, false
}

// CloseAll permanently closes the manager. A login warm-up can outlive its
// HTTP request, so a snapshot of open profiles alone is not a shutdown barrier.
// Seal admission first, wait for open/clone/delete/evict owners, then drain all
// references. Once also makes concurrent callers wait for the same teardown.
func (pm *ProfileManager) CloseAll() {
	pm.closeOnce.Do(func() {
		pm.mu.Lock()
		pm.closed = true
		pm.cond.Broadcast()
		for len(pm.opening) != 0 || len(pm.blocked) != 0 {
			pm.cond.Wait()
		}
		toClose := pm.open
		pm.open = nil
		pm.mu.Unlock()

		for name, pd := range toClose {
			pm.closeProfile(name, pd)
		}
	})
}

// CloneProfile copies srcProfile's directory into a new dstProfile directory.
// It uses an os.Root for all source reads so symlinks that point outside the
// source directory are rejected by the OS rather than silently followed.
// It owns rollback of only the destination it creates, while both names remain
// locked. Callers must not remove the destination on failure.
func (pm *ProfileManager) CloneProfile(ctx context.Context, srcProfile, dstProfile string) (err error) {
	unlockProfiles, err := pm.lockProfiles(ctx, srcProfile, dstProfile)
	if err != nil {
		return err
	}
	defer unlockProfiles()

	srcDir := pm.profileDir(srcProfile)
	dstDir := pm.profileDir(dstProfile)

	srcRoot, err := os.OpenRoot(srcDir)
	if err != nil {
		return fmt.Errorf("open source root: %w", err)
	}
	created := false
	defer func() {
		if closeErr := srcRoot.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close source root: %w", closeErr))
		}
		if err != nil && created {
			if removeErr := os.RemoveAll(dstDir); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("remove incomplete clone: %w", removeErr))
			}
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	// Mkdir atomically claims a new directory. Stat followed by MkdirAll can
	// adopt a path created in between; even a dangling symlink must be refused.
	// A conflict gives us no ownership and must never trigger directory removal.
	if err := os.Mkdir(dstDir, 0o755); err != nil {
		return fmt.Errorf("create destination dir %q: %w", dstDir, err)
	}
	created = true

	return cloneRootDir(ctx, srcRoot, dstDir, ".", 0)
}

const maxCloneDepth = 100

// cloneRootDir recursively copies the entries of srcRoot/relDir into
// filepath.Join(dstBase, relDir). Only regular files and directories are
// copied; symlinks and special files are skipped. Because all reads go
// through srcRoot (an os.Root), the OS refuses any path that would escape
// the source directory, preventing symlink-based directory-traversal attacks.
func cloneRootDir(ctx context.Context, srcRoot *os.Root, dstBase, relDir string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxCloneDepth {
		return fmt.Errorf("clone depth limit exceeded at %q", relDir)
	}

	// os.Root has no ReadDir method; open the directory through the root
	// (preserving the sandbox guarantee) then call ReadDir on the file handle.
	dirFile, err := srcRoot.Open(relDir)
	if err != nil {
		return fmt.Errorf("open dir %q: %w", relDir, err)
	}
	entries, readErr := dirFile.ReadDir(-1)
	if closeErr := dirFile.Close(); closeErr != nil {
		slog.Warn("close dir handle in clone failed", "dir", relDir, "err", closeErr)
	}
	if err := readErr; err != nil {
		return fmt.Errorf("read dir %q: %w", relDir, err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()

		// Build the path relative to srcRoot (os.Root uses forward slashes).
		var childRel string
		if relDir == "." {
			childRel = name
		} else {
			childRel = relDir + "/" + name
		}

		dstPath := filepath.Join(dstBase, filepath.FromSlash(childRel))

		if entry.IsDir() {
			// Skip hidden directories except .sayumi (which holds the database).
			if strings.HasPrefix(name, ".") && name != ".sayumi" {
				continue
			}
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return fmt.Errorf("create dir %q: %w", dstPath, err)
			}
			if err := cloneRootDir(ctx, srcRoot, dstBase, childRel, depth+1); err != nil {
				return err
			}
		} else if entry.Type().IsRegular() {
			// Skip symlinks and special files; copy only regular files.
			if err := copyFileFromRoot(ctx, srcRoot, childRel, dstPath); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// copyReaderToFile creates dst (and any missing parent directories), then
// streams src into it. On cancellation or a copy/close error the partial file
// is removed. Cancellation is checked between reads, not just between files.
func copyReaderToFile(ctx context.Context, src io.Reader, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %q: %w", dst, err)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination %q: %w", dst, err)
	}

	_, copyErr := io.Copy(out, cloneReader{ctx: ctx, src: src})
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	if copyErr != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy to %q: %w", dst, copyErr)
	}

	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close destination %q: %w", dst, err)
	}

	return nil
}

type cloneReader struct {
	ctx context.Context
	src io.Reader
}

func (r cloneReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}

func copyFileFromRoot(ctx context.Context, srcRoot *os.Root, relPath, dst string) error {
	in, err := srcRoot.Open(relPath)
	if err != nil {
		return fmt.Errorf("open source %q: %w", relPath, err)
	}
	defer func() { _ = in.Close() }()
	return copyReaderToFile(ctx, in, dst)
}
