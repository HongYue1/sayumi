package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
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
