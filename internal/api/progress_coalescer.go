package api

import (
	"context"
	"log/slog"
	"maps"
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
// Durability tradeoff: a hard crash (or kill -9) loses positions not yet
// persisted. With a healthy DB this is normally one flush interval plus write
// latency; failures extend that window. A graceful shutdown attempts a final
// drain via stop(), but cannot guarantee persistence when the DB keeps failing.
const (
	// progressFlushInterval schedules periodic persistence of buffered positions.
	progressFlushInterval = 3 * time.Second

	// progressMaxPending triggers an early flush when buffered keys reach it.
	// It is a soft trigger, not a memory limit: staging never waits for a slow
	// DB, and failed writes remain buffered for retry. Each key retains only
	// its latest position.
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

// A version identifies a staging operation, not its value or timestamp. Two
// updates may be identical within one second, or a key may be dropped and
// staged again while an older save is still in flight.
type stagedProgress struct {
	record  storage.ProgressRecord
	version uint64
}

// progressCoalescer buffers the latest progress record per (book, user) and
// flushes them to storage on a timer. It is owned by a single profile and must
// be stopped (drained) before that profile's DB is closed.
type progressCoalescer struct {
	db         progressSaver
	interval   time.Duration
	maxPending int

	mu          sync.Mutex
	pending     map[progressKey]stagedProgress
	nextVersion uint64

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
		pending:     make(map[progressKey]stagedProgress),
		flushSignal: make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
	c.wg.Go(c.run)
	return c
}

// stage records the latest position for a (book, user), overwriting any
// not-yet-flushed value. It performs no I/O and is safe for concurrent use.
func (c *progressCoalescer) stage(rec storage.ProgressRecord) {
	if rec.UpdatedAt == "" {
		rec.UpdatedAt = time.Now().UTC().Format(time.DateTime)
	}
	key := progressKey{bookID: rec.BookID, userID: rec.UserID}

	c.mu.Lock()
	c.nextVersion++
	c.pending[key] = stagedProgress{record: rec, version: c.nextVersion}
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
	entry, ok := c.pending[progressKey{bookID: bookID, userID: userID}]
	return entry.record, ok
}

// getAll returns a snapshot of one user's pending positions keyed by book ID.
// The copy keeps callers from observing or mutating the coalescer's live map.
func (c *progressCoalescer) getAll(userID string) map[string]storage.ProgressRecord {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make(map[string]storage.ProgressRecord)
	for key, entry := range c.pending {
		if key.userID == userID {
			result[key.bookID] = entry.record
		}
	}
	return result
}

// stop attempts a final drain and shuts the flusher goroutine down. Call it
// after staging callers have quiesced. It is idempotent and waits for that
// final flush; failures are logged rather than retried indefinitely at shutdown.
func (c *progressCoalescer) stop() {
	c.stopOnce.Do(func() { close(c.done) })
	c.wg.Wait()
}

func (c *progressCoalescer) run() {
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

// flush is called serially by run, preserving write order between batches.
func (c *progressCoalescer) flush() {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return
	}
	// Keep every unacknowledged position readable during the DB write. Swapping
	// pending out here would make get/getAll fall back to stale persisted data
	// until the save completes. The snapshot lets staging and deletion proceed
	// without holding the mutex across I/O.
	batch := maps.Clone(c.pending)
	c.mu.Unlock()

	for key, entry := range batch {
		err := c.saveOne(entry.record)
		if err == nil {
			c.discardSaved(key, entry.version)
			continue
		}
		// A foreign-key violation means the book row is gone (deleted, its
		// progress CASCADE-removed). Retrying would fail on every interval.
		// dropBook covers existing pending entries; this also covers a save
		// already in flight and a stage racing the handler's existence check.
		if isForeignKeyConstraint(err) {
			slog.Debug("dropping progress for deleted book", "book", key.bookID, "user", key.userID)
			c.discardSaved(key, entry.version)
			continue
		}
		slog.Error("coalesced progress save failed", "book", key.bookID, "user", key.userID, "err", err)
		// Leave a transient failure pending for the next tick. In particular,
		// never reinsert a record that dropBook removed while this save ran.
	}
}

func (c *progressCoalescer) saveOne(rec storage.ProgressRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), progressFlushWriteTimeout)
	defer cancel()
	return c.db.SaveProgressContext(ctx, rec)
}

func (c *progressCoalescer) discardSaved(key progressKey, version uint64) {
	c.mu.Lock()
	// Finishing an older save must not acknowledge a newer staging operation.
	if entry, ok := c.pending[key]; ok && entry.version == version {
		delete(c.pending, key)
	}
	c.mu.Unlock()
}

// dropBook discards buffered progress for a deleted book across all users,
// including read-through entries whose saves are in flight. An in-flight DB
// call cannot be undone here; the book deletion's cascade/FK protects storage,
// and a transient save failure must not resurrect the dropped entry.
func (c *progressCoalescer) dropBook(bookID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.pending {
		if key.bookID == bookID {
			delete(c.pending, key)
		}
	}
}
