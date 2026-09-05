package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"sayumi/internal/epub"
)

type spineTestResult struct {
	spine []epub.SpineEntry
	found bool
	err   error
}

func TestBookCacheRejectsStaleLoadErrors(t *testing.T) {
	for _, failure := range []struct {
		name string
		raw  string
		err  error
	}{
		{name: "read", err: errors.New("old read failed")},
		{name: "missing", err: ErrNotFound},
		{name: "parse", raw: "not-json"},
	} {
		for _, change := range []string{"replace", "remove", "replace_and_warm"} {
			t.Run(failure.name+"/"+change, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					old := sampleBook("book", "old", "/lib/book.epub")
					old.SpineJSON = ""
					cache := newMemoryBookCache(old)
					started := make(chan struct{})
					release := make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					defer unblock()
					cache.loadBookContent = func(context.Context, string) (string, string, error) {
						close(started)
						<-release
						return failure.raw, "[]", failure.err
					}

					result := make(chan spineTestResult, 1)
					go func() {
						spine, found, err := cache.GetSpine(t.Context(), old.ID)
						result <- spineTestResult{spine: spine, found: found, err: err}
					}()
					<-started
					if change == "remove" {
						cache.Remove(old.ID)
					} else {
						cache.Add(bookWithSpine(t, old.ID, "new", old.FilePath, []epub.SpineEntry{{Href: "new.xhtml"}}))
						if change == "replace_and_warm" {
							if _, found, err := cache.GetSpine(t.Context(), old.ID); err != nil || !found {
								t.Fatalf("warm replacement: found=%v err=%v", found, err)
							}
						}
					}
					unblock()
					got := <-result
					if got.err != nil {
						t.Fatalf("superseded load returned stale error: %v", got.err)
					}
					if change == "remove" {
						if got.found || got.spine != nil {
							t.Fatalf("removed book returned (%v, %v)", got.spine, got.found)
						}
					} else if !got.found || len(got.spine) != 1 || got.spine[0].Href != "new.xhtml" {
						t.Fatalf("replacement spine = (%v, %v), want new.xhtml", got.spine, got.found)
					}
				})
			})
		}
	}
}

func TestBookCacheSpineWaitersCancelIndependently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		book := sampleBook("book", "hash", "/lib/book.epub")
		book.SpineJSON = ""
		cache := newMemoryBookCache(book)
		started := make(chan struct{})
		release := make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		var loads atomic.Int32
		cache.loadBookContent = func(ctx context.Context, _ string) (string, string, error) {
			if loads.Add(1) == 1 {
				close(started)
			}
			<-release
			return "[]", "[]", ctx.Err()
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		canceled := make(chan error, 1)
		go func() {
			_, _, err := cache.GetSpine(ctx, book.ID)
			canceled <- err
		}()
		<-started

		waiting := make(chan spineTestResult, 1)
		go func() {
			spine, found, err := cache.GetSpine(t.Context(), book.ID)
			waiting <- spineTestResult{spine: spine, found: found, err: err}
		}()
		synctest.Wait()
		cancel()
		if err := <-canceled; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v", err)
		}
		unblock()
		got := <-waiting
		if got.err != nil || !got.found || got.spine == nil || len(got.spine) != 0 {
			t.Fatalf("remaining waiter = (%v, %v, %v)", got.spine, got.found, got.err)
		}
		if got := loads.Load(); got != 1 {
			t.Fatalf("shared content loads = %d, want 1", got)
		}
	})
}

func TestBookCacheCurrentLoadFailureCanRetry(t *testing.T) {
	readErr := errors.New("temporary read failure")
	for _, failure := range []struct {
		name string
		raw  string
		err  error
	}{
		{name: "read", err: readErr},
		{name: "parse", raw: "not-json"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			book := sampleBook("book", "hash", "/lib/book.epub")
			book.SpineJSON = ""
			cache := newMemoryBookCache(book)
			var loads atomic.Int32
			cache.loadBookContent = func(context.Context, string) (string, string, error) {
				if loads.Add(1) == 1 {
					return failure.raw, "[]", failure.err
				}
				return "[]", "[]", nil
			}
			if _, _, err := cache.GetSpine(t.Context(), book.ID); err == nil {
				t.Fatal("current generation's failure was suppressed")
			} else if failure.err != nil && !errors.Is(err, failure.err) {
				t.Fatalf("read error lost its cause: %v", err)
			}
			for range 2 {
				spine, found, err := cache.GetSpine(t.Context(), book.ID)
				if err != nil || !found || spine == nil || len(spine) != 0 {
					t.Fatalf("retry = (%v, %v, %v)", spine, found, err)
				}
			}
			if got := loads.Load(); got != 2 {
				t.Fatalf("content loads = %d, want one failure and one cached success", got)
			}
		})
	}
}

func TestBookCacheLoadSpineRechecksMemo(t *testing.T) {
	book := sampleBook("book", "hash", "/lib/book.epub")
	book.SpineJSON = ""
	cache := newMemoryBookCache(book)
	cached := []epub.SpineEntry{{Href: "cached.xhtml"}}
	cache.spines[book.ID] = cached
	cache.loadBookContent = func(context.Context, string) (string, string, error) {
		t.Error("late singleflight leader reloaded an already cached spine")
		return "[]", "[]", nil
	}

	// A caller can miss the fast path, then enter singleflight after the first
	// load has completed and its flight has been removed. Recheck inside the load.
	got, err := cache.loadSpine(t.Context(), book.ID, book, cache.generations[book.ID])
	if err != nil || !got.found || len(got.spine) != 1 || &got.spine[0] != &cached[0] {
		t.Fatalf("late load = (%+v, %v), want the existing shared spine", got, err)
	}
}
