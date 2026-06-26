package epub

import (
	"archive/zip"
	"container/list"
	"fmt"
	"io"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"
)

type LRUCache[K comparable, V any] struct {
	mu    sync.Mutex
	cap   int
	items map[K]*list.Element
	order *list.List
}

type lruPair[K comparable, V any] struct {
	key K
	val V
}

func newLRUCache[K comparable, V any](capacity int) *LRUCache[K, V] {
	return &LRUCache[K, V]{
		cap:   max(capacity, 1),
		items: make(map[K]*list.Element, capacity+4),
		order: list.New(),
	}
}

func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruPair[K, V]).val, true
}

func (c *LRUCache[K, V]) Put(key K, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		el.Value.(*lruPair[K, V]).val = val
		return
	}
	el := c.order.PushFront(&lruPair[K, V]{key, val})
	c.items[key] = el
	if c.order.Len() > c.cap {
		back := c.order.Back()
		if back != nil {
			item := back.Value.(*lruPair[K, V])
			c.order.Remove(back)
			delete(c.items, item.key)
		}
	}
}

func (c *LRUCache[K, V]) Delete(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	item := el.Value.(*lruPair[K, V])
	c.order.Remove(el)
	delete(c.items, key)
	return item.val, true
}

func (c *LRUCache[K, V]) DeleteFunc(keep func(K) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var next *list.Element
	for el := c.order.Back(); el != nil; el = next {
		next = el.Prev()
		item := el.Value.(*lruPair[K, V])
		if !keep(item.key) {
			c.order.Remove(el)
			delete(c.items, item.key)
		}
	}
}

// Clear drops every entry while holding the cache's own mutex. Callers that
// need to empty the cache must use this rather than swapping the *LRUCache
// pointer, since readers reach the cache through that pointer without any
// external lock.
func (c *LRUCache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[K]*list.Element, c.cap+4)
	c.order.Init()
}

type zipEntry struct {
	filePath string
	reader   *zip.ReadCloser
	index    map[string]*zip.File
	refs     int
	evicted  bool

	// ready is closed once the entry finishes loading (reader+index attached,
	// or loadErr set). A goroutine that finds an existing entry must wait on
	// ready before reading reader/index, since the entry may be a placeholder
	// installed by another goroutine whose zip.OpenReader is still running.
	ready   chan struct{}
	loadErr error
}

type zipLRU struct {
	cap   int
	items map[string]*list.Element
	order *list.List
}

func newZipLRU(capacity int) *zipLRU {
	return &zipLRU{
		cap:   max(capacity, 1),
		items: make(map[string]*list.Element, capacity+4),
		order: list.New(),
	}
}

func (z *zipLRU) touch(key string, e *zipEntry) {
	if el, ok := z.items[key]; ok {
		z.order.MoveToFront(el)
		return
	}
	el := z.order.PushFront(e)
	z.items[key] = el
}

func (z *zipLRU) evictOne() *zipEntry {
	el := z.order.Back()
	if el == nil {
		return nil
	}
	e := el.Value.(*zipEntry)
	z.order.Remove(el)
	delete(z.items, e.filePath)
	return e
}

func (z *zipLRU) remove(key string) {
	if el, ok := z.items[key]; ok {
		z.order.Remove(el)
		delete(z.items, key)
	}
}

func (z *zipLRU) len() int { return z.order.Len() }

type chapterRenderKey struct {
	filePath      string
	chapterIndex  int
	renderVersion string
}

type chapterTextKey struct {
	filePath     string
	chapterIndex int
}

type textPair struct {
	orig  string
	lower string
}

type cssFragmentKey struct {
	filePath string
	cssPath  string
}

// cssFragment holds the processed output of one linked stylesheet: the
// non-@font-face rules and the extracted @font-face blocks, both with resource
// URLs already rewritten. It is shared across all chapters of a book that link
// the same sheet.
type cssFragment struct {
	css      string
	fontFace string
}

type EPUBStore struct {
	mu        sync.Mutex
	lru       *zipLRU
	openFiles map[string]*zipEntry
	chapters  *LRUCache[chapterRenderKey, ChapterResponse]
	texts     *LRUCache[chapterTextKey, textPair]
	cssFrags  *LRUCache[cssFragmentKey, cssFragment]
}

func NewStore(maxSize int) *EPUBStore {
	if maxSize < 1 {
		maxSize = 10
	}
	return &EPUBStore{
		lru:       newZipLRU(maxSize),
		openFiles: make(map[string]*zipEntry),
		chapters:  newLRUCache[chapterRenderKey, ChapterResponse](maxSize * 10),
		texts:     newLRUCache[chapterTextKey, textPair](maxSize * 5),
		cssFrags:  newLRUCache[cssFragmentKey, cssFragment](maxSize * 4),
	}
}

func buildIndex(zr *zip.Reader) map[string]*zip.File {
	idx := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		normalized := strings.TrimPrefix(f.Name, "/")
		idx[normalized] = f
		lower := strings.ToLower(normalized)
		if lower != normalized {
			if _, exists := idx[lower]; !exists {
				idx[lower] = f
			}
		}
	}
	return idx
}

// acquire returns a referenced entry for filePath, opening the zip on a cache
// miss. The disk open runs WITHOUT s.mu held: a miss installs a loading
// placeholder (holding one ref, with a fresh ready channel), releases the lock
// to run zip.OpenReader, then re-acquires the lock to attach the reader.
// Concurrent callers for the same path find the placeholder and block on
// e.ready rather than opening the file again, so a burst of first requests
// collapses to a single open and opens of other books never serialize behind
// this one. The caller must Release the returned entry.
func (s *EPUBStore) acquire(filePath string) (*zipEntry, error) {
	s.mu.Lock()
	if e, ok := s.openFiles[filePath]; ok {
		e.refs++
		e.evicted = false
		s.lru.touch(filePath, e)
		s.evictExcess()
		s.mu.Unlock()
		// The entry may still be loading; wait without holding s.mu. We hold a
		// ref, so the reader cannot be closed out from under us.
		<-e.ready
		if e.loadErr != nil {
			// The loader failed and removed the entry from the store, so our
			// ref is on an orphaned placeholder with no reader to release.
			return nil, e.loadErr
		}
		return e, nil
	}

	// Miss: install a loading placeholder holding our ref, then open the file
	// with the lock released so other store operations proceed meanwhile.
	e := &zipEntry{filePath: filePath, refs: 1, ready: make(chan struct{})}
	s.openFiles[filePath] = e
	s.lru.touch(filePath, e)
	s.mu.Unlock()

	rc, err := zip.OpenReader(filePath)

	s.mu.Lock()
	if err != nil {
		e.loadErr = fmt.Errorf("open epub %s: %w", filePath, err)
		// Drop the placeholder so future opens retry instead of finding a
		// permanently failed entry. Guard cur == e in case it was replaced.
		if cur, ok := s.openFiles[filePath]; ok && cur == e {
			delete(s.openFiles, filePath)
			s.lru.remove(filePath)
		}
		s.mu.Unlock()
		close(e.ready)
		return nil, e.loadErr
	}
	e.reader = rc
	e.index = buildIndex(&rc.Reader)
	// If a concurrent open evicted this placeholder during the unlocked
	// zip.OpenReader window, our ref kept it alive but marked it evicted and
	// dropped it from the LRU order. Resurrect it so the first Release doesn't
	// close the just-opened reader after a single use: clear the flag and
	// re-touch before counting it against the cap.
	e.evicted = false
	s.lru.touch(filePath, e)
	// Reader attached and protected by our ref; safe to count against the cap.
	s.evictExcess()
	s.mu.Unlock()
	close(e.ready)
	return e, nil
}

func (s *EPUBStore) evictExcess() {
	for s.lru.len() > s.lru.cap {
		victim := s.lru.evictOne()
		if victim == nil {
			break
		}
		if victim.refs == 0 {
			delete(s.openFiles, victim.filePath)
			if victim.reader != nil {
				if err := victim.reader.Close(); err != nil {
					slog.Error("failed to close evicted epub reader", "path", victim.filePath, "err", err)
				}
			}
		} else {
			victim.evicted = true
		}
	}
}

func (s *EPUBStore) OpenIndexed(filePath string) (*zip.Reader, map[string]*zip.File, error) {
	e, err := s.acquire(filePath)
	if err != nil {
		return nil, nil, err
	}
	// Safe without s.mu: acquire only returns once e.ready is observed, which
	// happens-after the reader/index writes, and our ref keeps them alive.
	return &e.reader.Reader, e.index, nil
}

func (s *EPUBStore) Release(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.openFiles[filePath]
	if !ok {
		return
	}
	if e.refs <= 0 {
		slog.Error("epub store release called with non-positive refs", "path", filePath, "refs", e.refs)
		return
	}
	e.refs--
	if e.refs == 0 && e.evicted {
		if e.reader != nil {
			if err := e.reader.Close(); err != nil {
				slog.Error("failed to close released epub reader", "path", filePath, "err", err)
			}
		}
		delete(s.openFiles, filePath)
	}
}

func (s *EPUBStore) GetChapter(filePath string, chapterIndex int, renderVersion string) (ChapterResponse, bool) {
	return s.chapters.Get(chapterRenderKey{filePath: filePath, chapterIndex: chapterIndex, renderVersion: renderVersion})
}

func (s *EPUBStore) SetChapter(filePath string, chapterIndex int, renderVersion string, resp ChapterResponse) {
	s.chapters.Put(chapterRenderKey{filePath: filePath, chapterIndex: chapterIndex, renderVersion: renderVersion}, resp)
}

func (s *EPUBStore) CloseBook(filePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.openFiles[filePath]
	if !ok {
		return
	}
	s.lru.remove(filePath)
	if e.refs == 0 {
		if e.reader != nil {
			if err := e.reader.Close(); err != nil {
				slog.Error("failed to close book reader", "path", filePath, "err", err)
			}
		}
		delete(s.openFiles, filePath)
	} else {
		e.evicted = true
	}
}

// TryCloseForReplace fully releases any cached reader for filePath so the
// on-disk EPUB can be atomically replaced (an in-place metadata/cover edit).
// It returns false WITHOUT touching the entry when a request currently holds
// the book open (refs > 0): the file must not be swapped while a reader has it
// mapped (notably on Windows), so the caller surfaces a "close the book and
// retry" conflict instead. When the book is not open, or is cached but idle, it
// closes and drops the cached reader, clears the derived chapter/text/css
// caches for the book, and returns true.
func (s *EPUBStore) TryCloseForReplace(filePath string) bool {
	s.mu.Lock()
	if e, ok := s.openFiles[filePath]; ok {
		if e.refs > 0 {
			s.mu.Unlock()
			return false
		}
		s.lru.remove(filePath)
		if e.reader != nil {
			if err := e.reader.Close(); err != nil {
				slog.Error("failed to close book reader for replace", "path", filePath, "err", err)
			}
		}
		delete(s.openFiles, filePath)
	}
	s.mu.Unlock()

	// EvictBook locks the per-cache mutexes (not s.mu), so it runs after s.mu is
	// released to avoid a lock-order inversion.
	s.EvictBook(filePath)
	return true
}

func (s *EPUBStore) EvictBook(filePath string) {
	s.chapters.DeleteFunc(func(k chapterRenderKey) bool { return k.filePath != filePath })
	s.texts.DeleteFunc(func(k chapterTextKey) bool { return k.filePath != filePath })
	s.cssFrags.DeleteFunc(func(k cssFragmentKey) bool { return k.filePath != filePath })
}

type ResourceReader struct {
	rc          io.ReadCloser
	ContentType string
	Size        int64
	store       *EPUBStore
	filePath    string
	released    bool
}

func (r *ResourceReader) Read(p []byte) (int, error) { return r.rc.Read(p) }

func (r *ResourceReader) Close() error {
	err := r.rc.Close()
	if !r.released {
		r.released = true
		r.store.Release(r.filePath)
	}
	return err
}

func lookupInIndex(index map[string]*zip.File, name string) (*zip.File, error) {
	name = strings.TrimPrefix(name, "/")
	if f, ok := index[name]; ok {
		return f, nil
	}
	if f, ok := index[strings.ToLower(name)]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", name)
}

func normalizeResourcePath(resourcePath string) (string, error) {
	resourcePath = strings.TrimSpace(resourcePath)
	if resourcePath == "" || strings.Contains(resourcePath, `\`) {
		return "", fmt.Errorf("invalid resource path: %q", resourcePath)
	}

	if slices.Contains(strings.Split(resourcePath, "/"), "..") {
		return "", fmt.Errorf("invalid resource path: %q", resourcePath)
	}

	cleaned := path.Clean(resourcePath)
	if cleaned == "." || cleaned == "" || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid resource path: %q", resourcePath)
	}

	return cleaned, nil
}

func (s *EPUBStore) OpenResource(filePath, resourcePath string) (*ResourceReader, error) {
	cleanedPath, err := normalizeResourcePath(resourcePath)
	if err != nil {
		return nil, err
	}

	_, index, err := s.OpenIndexed(filePath)
	if err != nil {
		return nil, err
	}

	f, err := lookupInIndex(index, cleanedPath)
	if err != nil {
		s.Release(filePath)
		return nil, fmt.Errorf("resource %s: %w", cleanedPath, err)
	}

	rc, err := f.Open()
	if err != nil {
		s.Release(filePath)
		return nil, fmt.Errorf("open resource %s: %w", cleanedPath, err)
	}

	ct := ContentTypeByExt(cleanedPath)
	if ct == "" {
		ct = "application/octet-stream"
	}
	return &ResourceReader{
		rc:          rc,
		ContentType: ct,
		Size:        int64(f.UncompressedSize64),
		store:       s,
		filePath:    filePath,
	}, nil
}

// ContentTypeByExt returns a MIME type for common EPUB resource extensions.
func ContentTypeByExt(resourcePath string) string {
	ext := strings.ToLower(path.Ext(resourcePath))
	switch ext {
	case ".html", ".xhtml", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".smil":
		return "application/smil+xml"
	default:
		return ""
	}
}

func (s *EPUBStore) GetText(filePath string, chapterIndex int) (orig, lower string, ok bool) {
	p, ok := s.texts.Get(chapterTextKey{filePath: filePath, chapterIndex: chapterIndex})
	if !ok {
		return "", "", false
	}
	return p.orig, p.lower, true
}

func (s *EPUBStore) SetText(filePath string, chapterIndex int, orig, lower string) {
	s.texts.Put(chapterTextKey{filePath: filePath, chapterIndex: chapterIndex}, textPair{orig: orig, lower: lower})
}

func (s *EPUBStore) GetCSSFragment(filePath, cssPath string) (cssFragment, bool) {
	return s.cssFrags.Get(cssFragmentKey{filePath: filePath, cssPath: cssPath})
}

func (s *EPUBStore) SetCSSFragment(filePath, cssPath string, frag cssFragment) {
	s.cssFrags.Put(cssFragmentKey{filePath: filePath, cssPath: cssPath}, frag)
}

// Close releases all open zip readers. Must only be called after all
// in-flight requests have returned (i.e. after markClosingAndWait).
func (s *EPUBStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for fp, e := range s.openFiles {
		if e.reader == nil {
			continue
		}
		if err := e.reader.Close(); err != nil {
			slog.Error("failed to close epub reader during shutdown", "path", fp, "err", err)
		}
	}
	s.openFiles = make(map[string]*zipEntry)
	s.lru = newZipLRU(s.lru.cap)
	// Clear the content caches through their own mutex instead of swapping the
	// pointer fields. GetChapter/SetChapter/GetText/... read these fields
	// without holding s.mu, so reassigning them here would be a data race if a
	// request ever slipped past markClosingAndWait. Clear() takes each cache's
	// own lock — the one those readers actually synchronize on.
	s.chapters.Clear()
	s.texts.Clear()
	s.cssFrags.Clear()
}
