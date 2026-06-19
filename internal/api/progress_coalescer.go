package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sayumi/internal/storage"
)

// Progress writes are the hottest write path in the app: the reader streams a
// position update on every scroll. Writing each one synchronously hits a WAL
// fsync per update (the storage floor). The coalescer buffers the latest
// position per (book, user) in memory and flushes on a short timer, collapsing
// a burst of scroll updates into a single durable write.
//
// Durability tradeoff: a hard crash (or kill -9) loses at most one flush
// interval of reading position, which self-heals on the next scroll. A graceful
// shutdown drains fully via stop(), so normal exits lose nothing.
const (
	// progressFlushInterval bounds how stale the persisted position can be and
	// caps the lost-position window on a crash.
	progressFlushInterval = 3 * time.Second

	// progressMaxPending triggers an early flush when buffered keys exceed it.
	// Normal pending size is roughly the number of books open across the few
	// concurrent profiles, so this only trips under abnormal load (e.g. a client
	// rapidly cycling through many books) and bounds memory + flush batch size.
	progressMaxPending = 256

	// progressFlushWriteTimeout bounds a single coalesced DB write. The write
	// runs on the flusher goroutine with a background context (never a request
	// context) because it must outlive the request that staged it.
	progressFlushWriteTimeout = 10 * time.Second
)

// progressSaver is the subset of *storage.DB the coalescer needs. It exists so
// the coalescer can be unit-tested against a fake without a real database.
type progressSaver interface {
	SaveProgressContext(ctx context.Context, progress storage.ProgressRecord) error
}

type progressKey struct {
	bookID string
	userID string
}

// progressCoalescer buffers the latest progress record per (book, user) and
// flushes them to storage on a timer. It is owned by a single profile and must
// be stopped (drained) before that profile's DB is closed.
type progressCoalescer struct {
	db         progressSaver
	interval   time.Duration
	maxPending int

	mu      sync.Mutex
	pending map[progressKey]storage.ProgressRecord

	flushSignal chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func newProgressCoalescer(db progressSaver, interval time.Duration, maxPending int) *progressCoalescer {
	c := &progressCoalescer{
		db:          db,
		interval:    interval,
		maxPending:  maxPending,
		pending:     make(map[progressKey]storage.ProgressRecord),
		flushSignal: make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	c.wg.Add(1)
	go c.run()
	return c
}

// stage records the latest position for a (book, user), overwriting any
// not-yet-flushed value. It performs no I/O and is safe for concurrent use.
func (c *progressCoalescer) stage(rec storage.ProgressRecord) {
	key := progressKey{bookID: rec.BookID, userID: rec.UserID}

	c.mu.Lock()
	c.pending[key] = rec
	overCap := len(c.pending) >= c.maxPending
	c.mu.Unlock()

	if overCap {
		// Non-blocking: a single queued signal is enough to wake the flusher.
		select {
		case c.flushSignal <- struct{}{}:
		default:
		}
	}
}

// get returns the latest buffered position for a (book, user) if one is pending
// (not yet flushed). Callers use it for read-through so a GET right after a
// scroll does not return a stale persisted value.
func (c *progressCoalescer) get(bookID, userID string) (storage.ProgressRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.pending[progressKey{bookID: bookID, userID: userID}]
	return rec, ok
}

// stop drains all buffered writes and shuts the flusher goroutine down. It is
// idempotent and blocks until the final flush completes.
func (c *progressCoalescer) stop() {
	c.stopOnce.Do(func() { close(c.done) })
	c.wg.Wait()
}

func (c *progressCoalescer) run() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-c.flushSignal:
			c.flush()
		case <-c.done:
			c.flush() // final drain before exit
			return
		}
	}
}

func (c *progressCoalescer) flush() {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	batch := c.pending
	c.pending = make(map[progressKey]storage.ProgressRecord)
	c.mu.Unlock()

	// DB writes happen outside the lock so staging never blocks on fsync.
	for key, rec := range batch {
		if err := c.saveOne(rec); err != nil {
			slog.Error("coalesced progress save failed", "book", key.bookID, "user", key.userID, "err", err)
			// Re-buffer so a transient failure retries on the next tick, but
			// never clobber a newer value staged in the meantime.
			c.restage(key, rec)
		}
	}
}

func (c *progressCoalescer) saveOne(rec storage.ProgressRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), progressFlushWriteTimeout)
	defer cancel()
	return c.db.SaveProgressContext(ctx, rec)
}

func (c *progressCoalescer) restage(key progressKey, rec storage.ProgressRecord) {
	c.mu.Lock()
	if _, exists := c.pending[key]; !exists {
		c.pending[key] = rec
	}
	c.mu.Unlock()
}

// dropBook discards any pending (not-yet-flushed) progress for a book across all
// users. Call it when a book is deleted: the book's progress row is
// CASCADE-removed with the book, so a position staged within the last flush
// interval would otherwise fail SaveProgressContext on the progress.book_id
// foreign key and restage itself forever (a recurring failed WAL write every
// flush interval until the profile closes).
func (c *progressCoalescer) dropBook(bookID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.pending {
		if key.bookID == bookID {
			delete(c.pending, key)
		}
	}
}
