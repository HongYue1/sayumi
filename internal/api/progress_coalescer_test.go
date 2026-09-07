package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"sayumi/internal/storage"
)

type fakeProgressSaver struct {
	mu    sync.Mutex
	saves []storage.ProgressRecord
	err   error
}

func (f *fakeProgressSaver) SaveProgressContext(_ context.Context, p storage.ProgressRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saves = append(f.saves, p)
	return nil
}

func (f *fakeProgressSaver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

func (f *fakeProgressSaver) last() (storage.ProgressRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		return storage.ProgressRecord{}, false
	}
	return f.saves[len(f.saves)-1], true
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

// A burst of writes for one key collapses to a single save, and the last value
// wins. A long interval ensures only the stop() drain flushes.
func TestProgressCoalescerCoalescesUntilFlush(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, time.Hour, 1000)
	t.Cleanup(c.stop)

	for i := range 5 {
		c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: i, Percent: float64(i) / 10})
	}
	if got := fake.count(); got != 0 {
		t.Fatalf("expected no synchronous saves before flush, got %d", got)
	}

	c.stop()

	if got := fake.count(); got != 1 {
		t.Fatalf("expected 1 coalesced save, got %d", got)
	}
	last, _ := fake.last()
	if last.Chapter != 4 {
		t.Fatalf("expected last-write-wins chapter 4, got %d", last.Chapter)
	}
}

// A staged-but-unflushed value is visible via get() for read-through.
func TestProgressCoalescerReadThrough(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, time.Hour, 1000)
	t.Cleanup(c.stop)

	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 7})

	rec, ok := c.get("b1", "default")
	if !ok || rec.Chapter != 7 {
		t.Fatalf("read-through miss: ok=%v rec=%+v", ok, rec)
	}
	if _, ok := c.get("missing", "default"); ok {
		t.Fatal("expected miss for unknown key")
	}
	if rec.UpdatedAt == "" {
		t.Fatal("expected staged progress to carry a read timestamp")
	}
}

func TestProgressCoalescerReadThroughAll(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, time.Hour, 1000)
	t.Cleanup(c.stop)

	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
	c.stage(storage.ProgressRecord{BookID: "b2", UserID: "default", Chapter: 2})
	c.stage(storage.ProgressRecord{BookID: "b3", UserID: "other", Chapter: 3})

	pending := c.getAll("default")
	if len(pending) != 2 || pending["b1"].Chapter != 1 || pending["b2"].Chapter != 2 {
		t.Fatalf("unexpected pending snapshot: %+v", pending)
	}
	delete(pending, "b1")
	if _, ok := c.get("b1", "default"); !ok {
		t.Fatal("mutating the snapshot must not mutate the coalescer")
	}
}

// The periodic ticker flushes without an explicit stop.
func TestProgressCoalescerFlushesOnInterval(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, 5*time.Millisecond, 1000)
	t.Cleanup(c.stop)

	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
	waitFor(t, func() bool { return fake.count() == 1 })
}

// Exceeding the soft cap triggers an early flush.
func TestProgressCoalescerSizeCapTriggersFlush(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, time.Hour, 3)
	t.Cleanup(c.stop)

	for i := range 3 {
		c.stage(storage.ProgressRecord{BookID: fmt.Sprintf("b%d", i), UserID: "default", Chapter: i})
	}
	waitFor(t, func() bool { return fake.count() == 3 })
}

// dropBook discards pending positions for a deleted book (across users) so a
// later flush never retries a write for a CASCADE-removed book_id, while
// leaving other books' pending positions intact.
func TestProgressCoalescerDropBook(t *testing.T) {
	fake := &fakeProgressSaver{}
	c := newProgressCoalescer(fake, time.Hour, 1000)
	t.Cleanup(c.stop)

	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "other", Chapter: 2})
	c.stage(storage.ProgressRecord{BookID: "b2", UserID: "default", Chapter: 3})

	c.dropBook("b1")

	if _, ok := c.get("b1", "default"); ok {
		t.Fatal("expected b1/default to be dropped")
	}
	if _, ok := c.get("b1", "other"); ok {
		t.Fatal("expected b1/other to be dropped")
	}
	if rec, ok := c.get("b2", "default"); !ok || rec.Chapter != 3 {
		t.Fatalf("expected b2/default to remain, got ok=%v rec=%+v", ok, rec)
	}

	c.flush()
	if got := fake.count(); got != 1 {
		t.Fatalf("expected only the surviving book to flush, got %d saves", got)
	}
	if last, _ := fake.last(); last.BookID != "b2" {
		t.Fatalf("expected surviving save for b2, got %q", last.BookID)
	}
}

// A failed write is re-buffered and retried on the next flush rather than lost.
func TestProgressCoalescerRetriesFailedWrite(t *testing.T) {
	fake := &fakeProgressSaver{err: errors.New("boom")}
	c := newProgressCoalescer(fake, time.Hour, 1000)
	t.Cleanup(c.stop)

	c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 2})
	c.flush() // fails -> re-buffered

	if rec, ok := c.get("b1", "default"); !ok || rec.Chapter != 2 {
		t.Fatalf("expected failed write to remain buffered, got ok=%v rec=%+v", ok, rec)
	}

	fake.mu.Lock()
	fake.err = nil
	fake.mu.Unlock()

	c.stop() // drains successfully
	if got := fake.count(); got != 1 {
		t.Fatalf("expected 1 save after retry, got %d", got)
	}
}

type progressSaveFunc func(context.Context, storage.ProgressRecord) error

func (f progressSaveFunc) SaveProgressContext(ctx context.Context, rec storage.ProgressRecord) error {
	return f(ctx, rec)
}

// Blocking the first save exposes the pre-commit window without depending on
// disk speed. Releasing is idempotent so cleanup also works after a failed check.
func blockFirstProgressSave(next progressSaver, firstErr error) (progressSaver, <-chan struct{}, func()) {
	started := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	saver := progressSaveFunc(func(ctx context.Context, rec storage.ProgressRecord) error {
		var err error
		once.Do(func() {
			close(started)
			select {
			case <-resume:
				err = firstErr
			case <-ctx.Done():
				err = ctx.Err()
			}
		})
		if err != nil {
			return err
		}
		return next.SaveProgressContext(ctx, rec)
	})
	return saver, started, sync.OnceFunc(func() { close(resume) })
}

func TestProgressCoalescerReadThroughDuringFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeProgressSaver{}
		saver, started, unblock := blockFirstProgressSave(fake, nil)
		c := newProgressCoalescer(saver, time.Hour, 1000)
		t.Cleanup(func() {
			unblock()
			c.stop()
		})

		c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
		c.stage(storage.ProgressRecord{BookID: "b2", UserID: "default", Chapter: 2})
		c.stage(storage.ProgressRecord{BookID: "b1", UserID: "other", Chapter: 3})
		c.flushSignal <- struct{}{}
		<-started

		// The batch has left the staging path but none of it is persisted yet.
		if rec, ok := c.get("b1", "default"); !ok || rec.Chapter != 1 {
			t.Errorf("in-flight read-through: ok=%v rec=%+v", ok, rec)
		}
		pending := c.getAll("default")
		if len(pending) != 2 || pending["b1"].Chapter != 1 || pending["b2"].Chapter != 2 {
			t.Errorf("in-flight snapshot = %+v, want both default-user positions", pending)
		}
		if other := c.getAll("other"); len(other) != 1 || other["b1"].Chapter != 3 {
			t.Errorf("other user's in-flight snapshot = %+v", other)
		}
		if got := c.getAll("missing"); len(got) != 0 {
			t.Errorf("unknown user's snapshot = %+v, want empty", got)
		}
		delete(pending, "b1")
		if _, ok := c.get("b1", "default"); !ok {
			t.Error("mutating the in-flight snapshot changed live progress")
		}

		unblock()
		c.stop()
		if got := fake.count(); got != 3 {
			t.Errorf("saved %d records, want 3", got)
		}
		if got := c.getAll("default"); len(got) != 0 {
			t.Errorf("successfully persisted records remain buffered: %+v", got)
		}
	})
}

func TestProgressCoalescerFlushPreservesConcurrentStage(t *testing.T) {
	for _, outcome := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "transient failure", err: errors.New("temporary write failure")},
	} {
		for _, update := range []string{"newer position", "identical position", "drop then identical position"} {
			t.Run(outcome.name+"/"+update, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					fake := &fakeProgressSaver{}
					saver, started, unblock := blockFirstProgressSave(fake, outcome.err)
					c := newProgressCoalescer(saver, time.Hour, 1000)
					t.Cleanup(func() {
						unblock()
						c.stop()
					})

					first := storage.ProgressRecord{
						BookID: "b1", UserID: "default", Chapter: 1,
						UpdatedAt: "2026-01-01 00:00:00",
					}
					c.stage(first)
					c.flushSignal <- struct{}{}
					<-started
					latest := first
					switch update {
					case "newer position":
						latest.Chapter = 9
					case "drop then identical position":
						c.dropBook(first.BookID)
					}
					c.stage(latest)
					unblock()
					synctest.Wait()

					// Equal values and second-resolution timestamps cannot identify
					// which staging operation an earlier save has acknowledged.
					if rec, ok := c.get(first.BookID, first.UserID); !ok || rec != latest {
						t.Fatalf("new stage was lost: ok=%v rec=%+v, want %+v", ok, rec, latest)
					}
					c.stop()
					if last, ok := fake.last(); !ok || last != latest {
						t.Errorf("final saved position: ok=%v rec=%+v, want %+v", ok, last, latest)
					}
					wantSaves := 2
					if outcome.err != nil {
						wantSaves = 1
					}
					if got := fake.count(); got != wantSaves {
						t.Errorf("saved %d records, want %d", got, wantSaves)
					}
				})
			})
		}
	}
}

func TestProgressCoalescerDropBookDuringFailedFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeProgressSaver{}
		saver, started, unblock := blockFirstProgressSave(fake, errors.New("temporary write failure"))
		c := newProgressCoalescer(saver, time.Hour, 1000)
		t.Cleanup(func() {
			unblock()
			c.stop()
		})

		c.stage(storage.ProgressRecord{BookID: "deleted", UserID: "default", Chapter: 1})
		c.flushSignal <- struct{}{}
		<-started
		c.dropBook("deleted")
		unblock()
		synctest.Wait()

		if rec, ok := c.get("deleted", "default"); ok {
			t.Errorf("failed write resurrected dropped progress: %+v", rec)
		}
		c.stop()
		if got := fake.count(); got != 0 {
			t.Errorf("saved %d records after dropping the failed write, want 0", got)
		}
	})
}

func TestProgressCoalescerStopWaitsForInFlightWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeProgressSaver{}
		saver, started, unblock := blockFirstProgressSave(fake, nil)
		c := newProgressCoalescer(saver, time.Hour, 1000)
		t.Cleanup(func() {
			unblock()
			c.stop()
		})

		c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
		c.flushSignal <- struct{}{}
		<-started
		stopped := make(chan struct{}, 2)
		for range 2 {
			go func() {
				c.stop()
				stopped <- struct{}{}
			}()
		}
		synctest.Wait()
		if got := len(stopped); got != 0 {
			t.Fatalf("%d stop calls returned before the in-flight save completed", got)
		}

		unblock()
		synctest.Wait()
		if got := len(stopped); got != 2 {
			t.Fatalf("%d stop calls completed, want both", got)
		}
		if got := fake.count(); got != 1 {
			t.Errorf("saved %d records, want 1", got)
		}
	})
}

func TestProgressCoalescerFinalWriteTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var saveErr error
		var deadline time.Time
		saver := progressSaveFunc(func(ctx context.Context, _ storage.ProgressRecord) error {
			deadline, _ = ctx.Deadline()
			<-ctx.Done()
			saveErr = ctx.Err()
			return saveErr
		})
		c := newProgressCoalescer(saver, time.Hour, 1000)
		t.Cleanup(c.stop)
		c.stage(storage.ProgressRecord{BookID: "b1", UserID: "default", Chapter: 1})
		start := time.Now()
		c.stop()

		if want := start.Add(progressFlushWriteTimeout); !deadline.Equal(want) {
			t.Errorf("write deadline = %v, want %v", deadline, want)
		}
		if !errors.Is(saveErr, context.DeadlineExceeded) {
			t.Errorf("save error = %v, want deadline exceeded", saveErr)
		}
		if elapsed := time.Since(start); elapsed != progressFlushWriteTimeout {
			t.Errorf("final write took %v, want %v", elapsed, progressFlushWriteTimeout)
		}
	})
}

// countingProgressDB wraps a real *storage.DB so the test exercises the actual
// modernc foreign-key error rather than a stand-in, while still counting how
// many times the coalescer attempted the write.
type countingProgressDB struct {
	*storage.DB
	mu       sync.Mutex
	attempts int
}

func (d *countingProgressDB) SaveProgressContext(ctx context.Context, p storage.ProgressRecord) error {
	d.mu.Lock()
	d.attempts++
	d.mu.Unlock()
	return d.DB.SaveProgressContext(ctx, p)
}

func (d *countingProgressDB) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.attempts
}

// A position staged for a book that no longer exists fails progress.book_id on
// every flush. Re-buffering it cost a failed WAL write plus an error log every
// flush interval for the life of the profile; the record must be dropped after
// the first attempt instead. dropBook only covers positions staged before the
// delete — this covers a record already swapped into a flush batch, and one
// staged between putProgressHandler's existence check and its stage() call.
func TestProgressCoalescerDropsForeignKeyFailureInsteadOfRetrying(t *testing.T) {
	raw, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	db := &countingProgressDB{DB: raw}

	c := newProgressCoalescer(db, time.Hour, progressMaxPending)
	t.Cleanup(c.stop)

	// No book row was ever inserted, so this violates progress.book_id.
	c.stage(storage.ProgressRecord{BookID: "ghost-book", UserID: "default", Chapter: 1, Percent: 0.5})

	// Complete both flushes before checking; observing an attempt start does
	// not establish that its database error has been handled yet.
	c.flush()
	c.flush()
	c.stop()
	if got := db.count(); got != 1 {
		t.Fatalf("save attempts = %d, want exactly 1 (a permanent failure must not be retried)", got)
	}
	if _, ok := c.get("ghost-book", "default"); ok {
		t.Fatal("record for a deleted book is still pending after its flush")
	}
}
