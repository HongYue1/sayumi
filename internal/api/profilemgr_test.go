package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"sayumi/internal/storage"
)

// lockProfiles must observe every name free in one uninterrupted critical
// section before marking any of them. cond.Wait releases pm.mu, so a version
// that checked the names in sequence and never revisited the earlier ones let a
// second caller claim a name the first had already passed: both then believed
// they held it exclusively, and whichever finished first deleted the other's
// block. Clone is the multi-name caller (src + dst), so it is the one that can
// collide with a concurrent delete/evict of one of its names.
func TestLockProfilesWaitsForNamesClaimedWhileBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pm := NewProfileManager(t.TempDir())
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pm.opening["beta"] = true

		result := make(chan error, 1)
		go func() {
			unlock, err := pm.lockProfiles(ctx, "alpha", "beta")
			if err == nil {
				unlock()
			}
			result <- err
		}()
		// Unlike observing map contents or sleeping, Wait proves the caller
		// reached cond.Wait before another operation claims alpha.
		synctest.Wait()
		unlockAlpha, err := pm.lockProfiles(ctx, "alpha")
		if err != nil {
			t.Fatal(err)
		}
		releaseAlpha := sync.OnceFunc(unlockAlpha)
		defer releaseAlpha()

		pm.mu.Lock()
		delete(pm.opening, "beta")
		pm.cond.Broadcast()
		pm.mu.Unlock()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("multi-name lock bypassed alpha's owner: %v", err)
		default:
		}
		releaseAlpha()
		synctest.Wait()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

func newLifecycleManager(t *testing.T, names ...string) *ProfileManager {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pm := NewProfileManager(root)
	t.Cleanup(pm.CloseAll)
	for _, name := range names {
		if err := os.Mkdir(pm.profileDir(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return pm
}

func TestProfileManagerGetCanceledCachedProfile(t *testing.T) {
	pm := newLifecycleManager(t, "reader")
	pd, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	pd.release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := pm.Get(ctx, "reader")
	if got != nil {
		got.release()
	}
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("Get canceled context = (%p, %v), want (nil, canceled)", got, err)
	}
}

func TestLockProfilesCanceledDoesNotEvict(t *testing.T) {
	pm := newLifecycleManager(t, "reader")
	pd, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	pd.release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	unlock, err := pm.lockProfiles(ctx, "reader")
	if unlock != nil {
		unlock()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lock = %v, want canceled without eviction", err)
	}
	got, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer got.release()
	if got != pd {
		t.Fatal("canceled lock replaced the cached profile")
	}
}

func TestProfileManagerCloseAllRejectsGet(t *testing.T) {
	pm := newLifecycleManager(t, "reader")
	pm.CloseAll()
	pd, err := pm.Get(t.Context(), "reader")
	if pd != nil {
		pd.release()
	}
	if !errors.Is(err, os.ErrClosed) || pd != nil {
		t.Fatalf("Get after CloseAll = (%p, %v), want (nil, closed)", pd, err)
	}
}

func TestProfileManagerCloseAllWaitsForOpening(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pm := NewProfileManager(t.TempDir())
		// Model a cold open that owns its marker but has not published yet.
		pm.opening["reader"] = true
		finishOpen := sync.OnceFunc(func() {
			pm.mu.Lock()
			delete(pm.opening, "reader")
			pm.cond.Broadcast()
			pm.mu.Unlock()
		})
		defer finishOpen()
		done := make(chan struct{})
		go func() {
			pm.CloseAll()
			close(done)
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("CloseAll returned while a profile was still opening")
		default:
		}
		finishOpen()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("CloseAll did not finish after the open completed")
		}
	})
}

func TestProfileManagerCloseAllWaitsForProfileLocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pm := NewProfileManager(t.TempDir())
		unlock, err := pm.lockProfiles(t.Context(), "source", "clone")
		if err != nil {
			t.Fatal(err)
		}
		release := sync.OnceFunc(unlock)
		defer release()
		done := make(chan struct{})
		go func() {
			pm.CloseAll()
			close(done)
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("CloseAll returned while filesystem ownership was held")
		default:
		}
		release()
		synctest.Wait()
		<-done
	})
}

func TestCloneProfileCanceledDoesNotCreateDestination(t *testing.T) {
	pm := newLifecycleManager(t, "source")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := pm.CloneProfile(ctx, "source", "clone"); !errors.Is(err, context.Canceled) {
		t.Errorf("CloneProfile = %v, want canceled", err)
	}
	if _, err := os.Lstat(pm.profileDir("clone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("canceled clone left a destination: %v", err)
	}
}

func TestCloneProfileMissingSourceDoesNotLeaveDestination(t *testing.T) {
	pm := newLifecycleManager(t)
	if err := pm.CloneProfile(t.Context(), "missing", "clone"); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Lstat(filepath.Join(pm.libraryRoot, "clone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed clone left a destination: %v", err)
	}
}

func TestNormalizeProfileNames(t *testing.T) {
	input := []string{"beta", "", "alpha", "beta", "alpha"}
	before := slices.Clone(input)
	if got := normalizeProfileNames(input); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Errorf("names = %v", got)
	}
	if !slices.Equal(input, before) {
		t.Error("normalization mutated the caller's slice")
	}
	if got := normalizeProfileNames(nil); len(got) != 0 {
		t.Errorf("empty names = %v", got)
	}
}

type lifecycleGetResult struct {
	pd  *profileDeps
	err error
}

func TestProfileManagerColdOpenPublication(t *testing.T) {
	for _, outcome := range []string{"publish", "cancel", "shutdown"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pm := newLifecycleManager(t, "reader")
				defer pm.CloseAll()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				ready := make(chan *profileDeps, 1)
				gate := make(chan struct{})
				unblock := sync.OnceFunc(func() { close(gate) })
				defer unblock()
				var loads atomic.Int32
				var firstOpened *profileDeps
				pm.loadProfile = func(ctx context.Context, name string) (*profileDeps, error) {
					n := loads.Add(1)
					if n > 1 && firstOpened.DB.PingContext(ctx) == nil {
						return nil, errors.New("retry opened before abandoned dependencies closed")
					}
					pd, err := pm.openProfile(ctx, name)
					if err == nil && n == 1 {
						firstOpened = pd
						ready <- pd
						<-gate
					}
					return pd, err
				}
				first := make(chan lifecycleGetResult, 1)
				go func() {
					pd, err := pm.Get(ctx, "reader")
					first <- lifecycleGetResult{pd, err}
				}()
				opened := <-ready
				second := make(chan lifecycleGetResult, 1)
				go func() {
					pd, err := pm.Get(t.Context(), "reader")
					second <- lifecycleGetResult{pd, err}
				}()
				synctest.Wait()
				var closeDone chan struct{}
				switch outcome {
				case "cancel":
					cancel()
				case "shutdown":
					closeDone = make(chan struct{})
					go func() {
						pm.CloseAll()
						close(closeDone)
					}()
					synctest.Wait()
					select {
					case <-closeDone:
						t.Fatal("shutdown returned before cold-open cleanup")
					default:
					}
				}
				unblock()
				synctest.Wait()
				a, b := <-first, <-second
				if a.pd != nil {
					defer a.pd.release()
				}
				if b.pd != nil {
					defer b.pd.release()
				}
				switch outcome {
				case "publish":
					if a.err != nil || b.err != nil || a.pd != opened || b.pd != opened || loads.Load() != 1 {
						t.Fatalf("shared open: first=%+v second=%+v loads=%d", a, b, loads.Load())
					}
					opened.lifetimeMu.Lock()
					refs := opened.refs
					opened.lifetimeMu.Unlock()
					if refs != 2 {
						t.Errorf("shared open has %d refs, want 2", refs)
					}
				case "cancel":
					if !errors.Is(a.err, context.Canceled) || a.pd != nil || b.err != nil || b.pd == nil || b.pd == opened || loads.Load() != 2 {
						t.Fatalf("canceled open/retry: first=%+v second=%+v loads=%d", a, b, loads.Load())
					}
					assertLifecycleClosed(t, opened)
				case "shutdown":
					if !errors.Is(a.err, os.ErrClosed) || !errors.Is(b.err, os.ErrClosed) || a.pd != nil || b.pd != nil || loads.Load() != 1 {
						t.Fatalf("shutdown open: first=%+v second=%+v loads=%d", a, b, loads.Load())
					}
					<-closeDone
					assertLifecycleClosed(t, opened)
				}
			})
		})
	}
}

func TestProfileManagerOpenFailureCanRetry(t *testing.T) {
	pm := newLifecycleManager(t)
	if pd, err := pm.Get(t.Context(), "reader"); err == nil || pd != nil {
		if pd != nil {
			pd.release()
		}
		t.Fatalf("missing directory: (%p, %v)", pd, err)
	}
	if err := os.Mkdir(pm.profileDir("reader"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if pd, err := pm.Get(ctx, "reader"); !errors.Is(err, context.Canceled) || pd != nil {
		if pd != nil {
			pd.release()
		}
		t.Fatalf("canceled cold open: (%p, %v)", pd, err)
	}
	if _, err := os.Stat(filepath.Join(pm.profileDir("reader"), ".sayumi")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled cold open touched the database directory: %v", err)
	}
	pd, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	pd.release()
	pm.Evict("reader")
	assertLifecycleClosed(t, pd)
	reopened, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.release()
	if reopened == pd {
		t.Fatal("Evict reused closed dependencies")
	}
}

func TestProfileManagerWaitCancellation(t *testing.T) {
	for _, operation := range []string{"get", "lock"} {
		for _, marker := range []string{"blocked", "opening"} {
			for _, release := range []string{"held", "released"} {
				t.Run(operation+"/"+marker+"/"+release, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						pm := NewProfileManager(t.TempDir())
						marks := pm.blocked
						if marker == "opening" {
							marks = pm.opening
						}
						marks["reader"] = true
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						result := make(chan error, 1)
						go func() {
							if operation == "get" {
								pd, err := pm.Get(ctx, "reader")
								if pd != nil {
									pd.release()
								}
								result <- err
								return
							}
							unlock, err := pm.lockProfiles(ctx, "reader")
							if unlock != nil {
								unlock()
							}
							result <- err
						}()
						synctest.Wait()
						pm.mu.Lock()
						cancel()
						if release == "released" {
							delete(marks, "reader")
							pm.cond.Broadcast()
						}
						pm.mu.Unlock()
						synctest.Wait()
						if err := <-result; !errors.Is(err, context.Canceled) {
							t.Fatalf("waiter = %v, want canceled", err)
						}
					})
				})
			}
		}
	}
}

func writeLifecycleFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertLifecycleClosed(t *testing.T, pd *profileDeps) {
	t.Helper()
	if pd.acquire() {
		pd.release()
		t.Error("closed dependencies still admit references")
	}
	if err := pd.DB.PingContext(t.Context()); err == nil {
		t.Error("profile database still open")
	}
	if f, err := pd.coverRoot.Open("."); err == nil {
		_ = f.Close()
		t.Error("profile cover root still open")
	}
	select {
	case <-pd.Progress.done:
	default:
		t.Error("profile progress worker not stopped")
	}
}

func TestProfileManagerCloseAllDrainsReferencesAndProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pm := newLifecycleManager(t, "reader")
		defer pm.CloseAll()
		writeLifecycleFile(t, filepath.Join(pm.profileDir("reader"), "book.epub"), minimalEPUBBytes(t, "Lifecycle"))
		pd, err := pm.Get(t.Context(), "reader")
		if err != nil {
			t.Fatal(err)
		}
		release := sync.OnceFunc(pd.release)
		defer release()
		books := pd.Books.ListSummaries()
		if len(books) != 1 {
			t.Fatalf("books = %v", books)
		}
		borrowed, ok := pm.FindBook(books[0].ID)
		if !ok || borrowed != pd {
			t.Fatal("FindBook did not borrow the cached profile")
		}
		releaseBook := sync.OnceFunc(borrowed.release)
		defer releaseBook()
		done := make(chan struct{})
		go func() {
			pm.CloseAll()
			close(done)
		}()
		synctest.Wait()
		if err := pd.DB.PingContext(t.Context()); err != nil {
			t.Fatalf("database closed under active references: %v", err)
		}
		if found, ok := pm.FindBook(books[0].ID); ok {
			found.release()
			t.Fatal("FindBook admitted a reference during shutdown")
		}
		if unlock, err := pm.lockProfiles(t.Context(), "reader"); !errors.Is(err, os.ErrClosed) {
			if unlock != nil {
				unlock()
			}
			t.Fatalf("lock during shutdown = %v", err)
		}
		release()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("FindBook's reference was not retained")
		default:
		}
		// An in-flight request may stage one final position while teardown waits.
		want := storage.ProgressRecord{BookID: books[0].ID, UserID: "reader", Chapter: 3, Percent: 0.75}
		pd.Progress.stage(want)
		releaseBook()
		synctest.Wait()
		<-done
		assertLifecycleClosed(t, pd)
		db, err := storage.Open(pd.LibPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		got, err := db.GetProgressContext(t.Context(), want.BookID, want.UserID)
		if err != nil || got.Chapter != want.Chapter || got.Percent != want.Percent {
			t.Fatalf("drained progress = %+v, %v", got, err)
		}
	})
}

func TestProfileManagerConcurrentCloseAll(t *testing.T) {
	pm := newLifecycleManager(t, "reader")
	pd, err := pm.Get(t.Context(), "reader")
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(pd.release)
	defer release()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			pm.CloseAll()
			if err := pd.DB.PingContext(t.Context()); err == nil {
				t.Error("CloseAll returned before database teardown")
			}
		})
	}
	release()
	wg.Wait()
	assertLifecycleClosed(t, pd)
}

func TestCloneProfileCanceledWhileDraining(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pm := newLifecycleManager(t, "source")
		defer pm.CloseAll()
		pd, err := pm.Get(t.Context(), "source")
		if err != nil {
			t.Fatal(err)
		}
		release := sync.OnceFunc(pd.release)
		defer release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- pm.CloneProfile(ctx, "source", "clone") }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("clone abandoned live references: %v", err)
		default:
		}
		pm.mu.Lock()
		blocked := pm.blocked["source"] && pm.blocked["clone"]
		pm.mu.Unlock()
		if !blocked {
			t.Fatal("cancellation released profile ownership before refs drained")
		}
		release()
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("drained clone = %v, want canceled", err)
		}
		assertLifecycleClosed(t, pd)
		if _, err := os.Lstat(pm.profileDir("clone")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled clone created its destination: %v", err)
		}
		unlock, err := pm.lockProfiles(t.Context(), "source", "clone")
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	})
}

func TestCloneProfileCopiesIsolatedSnapshot(t *testing.T) {
	pm := newLifecycleManager(t, "source")
	epubBytes := minimalEPUBBytes(t, "Cloned")
	writeLifecycleFile(t, filepath.Join(pm.profileDir("source"), "book.epub"), epubBytes)
	writeLifecycleFile(t, filepath.Join(pm.profileDir("source"), ".hidden", "skip.txt"), []byte("hidden"))
	writeLifecycleFile(t, filepath.Join(pm.profileDir("source"), "notes", "keep.txt"), []byte("notes"))
	pd, err := pm.Get(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(pd.release)
	defer release()
	books := pd.Books.ListSummaries()
	if len(books) != 1 {
		t.Fatalf("books = %v", books)
	}
	want := storage.ProgressRecord{BookID: books[0].ID, UserID: "reader", Chapter: 2, Percent: 0.5}
	pd.Progress.stage(want)
	writeLifecycleFile(t, filepath.Join(pd.LibPath, ".sayumi", "keep.txt"), []byte("metadata"))
	release()
	if err := pm.CloneProfile(t.Context(), "source", "clone"); err != nil {
		t.Fatal(err)
	}
	assertLifecycleClosed(t, pd)
	for _, rel := range []string{"book.epub", "notes/keep.txt", ".sayumi/keep.txt"} {
		src, err := os.ReadFile(filepath.Join(pm.profileDir("source"), rel))
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.ReadFile(filepath.Join(pm.profileDir("clone"), rel))
		if err != nil || !bytes.Equal(src, dst) {
			t.Errorf("clone %s differs: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(pm.profileDir("clone"), ".hidden")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("hidden directory was copied: %v", err)
	}
	clone, err := pm.Get(t.Context(), "clone")
	if err != nil {
		t.Fatal(err)
	}
	defer clone.release()
	book, ok := clone.Books.Get(want.BookID)
	if !ok || book.FilePath != filepath.Join(clone.LibPath, "book.epub") {
		t.Fatalf("clone did not reconcile its own book path: %+v", book)
	}
	got, err := clone.DB.GetProgressContext(t.Context(), want.BookID, want.UserID)
	if err != nil || got.Percent != want.Percent || got.Chapter != want.Chapter {
		t.Fatalf("clone missed buffered source progress: %+v, %v", got, err)
	}
	source, err := pm.Get(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer source.release()
	if source.DB == clone.DB || source.Books == clone.Books || source.Store == clone.Store || source.Progress == clone.Progress || source.coverRoot == clone.coverRoot {
		t.Fatal("profiles share mutable dependencies")
	}
	want.Percent = 0.9
	if err := source.DB.SaveProgressContext(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err = clone.DB.GetProgressContext(t.Context(), want.BookID, want.UserID)
	if err != nil || got.Percent != 0.5 {
		t.Fatalf("source write leaked into clone: %+v, %v", got, err)
	}
}

func TestCloneProfileDepthCleanup(t *testing.T) {
	pm := newLifecycleManager(t, "source")
	deep := filepath.Join(pm.profileDir("source"), filepath.FromSlash(strings.Repeat("d/", maxCloneDepth+1)), "keep.txt")
	writeLifecycleFile(t, deep, []byte("source"))
	if err := pm.CloneProfile(t.Context(), "source", "clone"); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("deep clone = %v", err)
	}
	if _, err := os.Lstat(pm.profileDir("clone")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("partial clone directory not removed: %v", err)
	}
	if got, err := os.ReadFile(deep); err != nil || string(got) != "source" {
		t.Errorf("source changed after failed clone: %q, %v", got, err)
	}
}

func TestCloneProfileSymlinks(t *testing.T) {
	pm := newLifecycleManager(t, "source")
	outside := filepath.Join(pm.libraryRoot, "outside.txt")
	writeLifecycleFile(t, outside, []byte("outside"))
	if err := os.Symlink(outside, filepath.Join(pm.profileDir("source"), "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := pm.CloneProfile(t.Context(), "source", "clone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(pm.profileDir("clone"), "link.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source symlink was copied: %v", err)
	}
	dst := pm.profileDir("dangling")
	if err := os.Symlink(filepath.Join(pm.libraryRoot, "missing"), dst); err != nil {
		t.Fatal(err)
	}
	if err := pm.CloneProfile(t.Context(), "source", "dangling"); !errors.Is(err, os.ErrExist) {
		t.Errorf("dangling destination = %v, want exists", err)
	}
	if info, err := os.Lstat(dst); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("unowned destination symlink changed: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside" {
		t.Errorf("outside file changed: %q, %v", got, err)
	}
}

type cloneReadFunc func([]byte) (int, error)

func (f cloneReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestCopyReaderToFileCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		eof  error
	}{
		{name: "between reads"},
		{name: "with EOF", eof: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			src := cloneReadFunc(func(p []byte) (int, error) {
				reads++
				if reads > 1 {
					return 0, io.EOF
				}
				cancel()
				return copy(p, "partial"), tc.eof
			})
			dst := filepath.Join(t.TempDir(), "nested", "copy.txt")
			if err := copyReaderToFile(ctx, src, dst); !errors.Is(err, context.Canceled) {
				t.Errorf("copy = %v, want canceled", err)
			}
			if reads != 1 {
				t.Errorf("read %d times after cancellation", reads)
			}
			if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("partial file remains: %v", err)
			}
		})
	}
}

func TestCopyReaderToFileReadFailure(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "copy.txt")
	src := io.MultiReader(strings.NewReader("partial"), cloneReadFunc(func([]byte) (int, error) {
		return 0, io.ErrUnexpectedEOF
	}))
	if err := copyReaderToFile(t.Context(), src, dst); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("copy = %v", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed copy remains: %v", err)
	}
	if err := copyReaderToFile(t.Context(), strings.NewReader("complete"), dst); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "complete" {
		t.Errorf("successful copy = %q, %v", got, err)
	}
}

// Rollback belongs to the manager that claimed the destination. A handler
// removing it on every error also removes an orphan it explicitly refused to
// adopt, or even the source itself through a case alias on Windows.
func TestCloneProfileHandlerPreservesUnownedDestination(t *testing.T) {
	for _, name := range []string{"clone", "SOURCE"} {
		t.Run(name, func(t *testing.T) {
			pm := newLifecycleManager(t, "source")
			dst := pm.profileDir(name)
			if name == "SOURCE" {
				srcInfo, err := os.Stat(pm.profileDir("source"))
				if err != nil {
					t.Fatal(err)
				}
				dstInfo, err := os.Stat(dst)
				if errors.Is(err, os.ErrNotExist) {
					t.Skip("filesystem distinguishes profile-name case")
				}
				if err != nil || !os.SameFile(srcInfo, dstInfo) {
					t.Fatalf("case-alias fixture: %v", err)
				}
			} else if err := os.Mkdir(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			keep := filepath.Join(dst, "keep.txt")
			if err := os.WriteFile(keep, []byte("do not delete"), 0o644); err != nil {
				t.Fatal(err)
			}
			profiles, err := storage.OpenProfilesDB(pm.libraryRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = profiles.Close() }()
			if err := profiles.CreateProfileContext(t.Context(), "source", "unused"); err != nil {
				t.Fatal(err)
			}
			deps := &Dependencies{ProfileMgr: pm, ProfilesDB: profiles, sessions: newSessionStore(nil)}
			token, _, err := deps.sessions.create(t.Context(), "source", false)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/auth/clone", strings.NewReader(`{"newName":"`+name+`","pin":"123456"}`))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			w := httptest.NewRecorder()
			cloneProfileHandler(deps)(w, r)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "clone_error") {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if got, err := os.ReadFile(keep); err != nil || string(got) != "do not delete" {
				t.Errorf("unowned destination was changed: %q, %v", got, err)
			}
			if _, err := profiles.GetProfileContext(t.Context(), name); !errors.Is(err, storage.ErrNotFound) {
				t.Errorf("failed clone registration not rolled back: %v", err)
			}
			if _, err := profiles.GetProfileContext(t.Context(), "source"); err != nil {
				t.Errorf("source registration changed: %v", err)
			}
		})
	}
}
