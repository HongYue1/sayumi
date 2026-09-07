package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

// newUploadTestDeps wires the real collaborators the upload handler reaches
// through: it stages into LibPath, hashes and imports with the Scanner, warms
// the book cache, and enriches the response from the DB and the coalescer. A
// fake would not exercise the dedup lookup, which is the point of these tests.
func newUploadTestDeps(t *testing.T) *profileDeps {
	t.Helper()

	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test DB: %v", err)
		}
	})

	books, err := storage.NewBookCache(t.Context(), db)
	if err != nil {
		t.Fatalf("build book cache: %v", err)
	}
	store := epub.NewStore(1)
	t.Cleanup(store.Close)

	libPath := t.TempDir()
	coalescer := newProgressCoalescer(db, time.Hour, 16)
	t.Cleanup(coalescer.stop)

	return &profileDeps{
		DB:       db,
		Books:    books,
		Store:    store,
		Scanner:  library.NewScanner(libPath, db),
		LibPath:  libPath,
		Progress: coalescer,
	}
}

// minimalEPUBBytes builds the smallest EPUB that survives validateEPUB and the
// importer's metadata parse, in memory so the same bytes can be uploaded twice.
func minimalEPUBBytes(t *testing.T, title string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// mimetype must be first and stored uncompressed for EPUB validity checks.
	head, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := head.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}

	write := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)
	write("OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:test:`+title+`</dc:identifier>
    <dc:title>`+title+`</dc:title>
    <dc:creator>Tester</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch"/>
  </spine>
</package>`)
	write("OEBPS/ch.xhtml", `<?xml version="1.0"?><html><body><p>`+title+`</p></body></html>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("close EPUB zip: %v", err)
	}
	return buf.Bytes()
}

func uploadEPUB(t *testing.T, pd *profileDeps, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := multipartUploadBody(t, filename, content)
	req := httptest.NewRequest(http.MethodPost, "/api/books/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	uploadBookHandler(nil)(recorder, withProfileDeps(req, pd))
	return recorder
}

// countLibraryEPUBs counts installed books and fails on leaked staging files.
func countLibraryEPUBs(t *testing.T, libPath string) int {
	t.Helper()

	entries, err := os.ReadDir(libPath)
	if err != nil {
		t.Fatalf("read library dir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".sayumi-upload-") {
			t.Errorf("upload left a staging file: %s", name)
		}
		if !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".epub") {
			count++
		}
	}
	return count
}

func TestUploadBookHandlerAddsNewBook(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	recorder := uploadEPUB(t, pd, "Fresh.epub", minimalEPUBBytes(t, "Fresh"))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	// duplicate is omitempty: a real addition must not carry the flag at all,
	// so the client cannot mistake it for a deduped upload.
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := payload["duplicate"]; present {
		t.Errorf("response carries duplicate = %v, want the field omitted", payload["duplicate"])
	}
	if payload["title"] != "Fresh" {
		t.Errorf("title = %v, want Fresh", payload["title"])
	}
	if got := countLibraryEPUBs(t, pd.LibPath); got != 1 {
		t.Errorf("library holds %d EPUBs, want 1", got)
	}
}

// The request can disappear after SQLite commits but before cache publication.
// Hold only the publication gate so the test observes a real committed import,
// then cancel before the handler is allowed to reload it.
func TestUploadBookHandlerCompletesCommittedImportAfterCancellation(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	if !pd.acquire() {
		t.Fatal("acquire middleware-owned reference")
	}
	t.Cleanup(pd.release)
	content := minimalEPUBBytes(t, "Committed")
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	body, contentType := multipartUploadBody(t, "Committed.epub", content)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/books/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()

	pd.bookReplaceMu.Lock()
	unlock := sync.OnceFunc(pd.bookReplaceMu.Unlock)
	done := make(chan struct{})
	go func() {
		defer close(done)
		uploadBookHandler(nil)(recorder, withProfileDeps(req, pd))
	}()
	defer func() {
		unlock()
		waitUploadDone(t, done)
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	var id, path string
	for id == "" {
		var found bool
		var err error
		id, path, found, err = pd.DB.GetBookIDByHashContext(t.Context(), hash)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("upload did not commit while cache publication was blocked")
		case <-tick.C:
		}
	}
	cancel()
	unlock()
	waitUploadDone(t, done)

	book, found := pd.Books.Get(id)
	if !found || book.Title != "Committed" {
		t.Errorf("committed upload missing from cache: %+v, found=%v", book, found)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Errorf("committed EPUB lost: %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("canceled request wrote a response: %s", recorder.Body.String())
	}
	pd.lifetimeMu.Lock()
	refs := pd.refs
	pd.lifetimeMu.Unlock()
	if refs != 1 {
		t.Errorf("handler changed middleware-owned references: %d", refs)
	}
}

func TestUploadBookHandlerConcurrentDuplicates(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	content := minimalEPUBBytes(t, "Concurrent")
	body, contentType := multipartUploadBody(t, "Same.epub", content)
	const uploads = 8
	responses := make([]*httptest.ResponseRecorder, uploads)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range uploads {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/books/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		responses[i] = httptest.NewRecorder()
		group.Go(func() {
			<-start
			uploadBookHandler(nil)(responses[i], withProfileDeps(req, pd))
		})
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	close(start)
	waitUploadDone(t, done)

	created := 0
	var id string
	for i, response := range responses {
		if response.Code != http.StatusCreated && response.Code != http.StatusOK {
			t.Fatalf("upload %d: status=%d body=%s", i, response.Code, response.Body.String())
		}
		var book BookResponse
		if err := json.Unmarshal(response.Body.Bytes(), &book); err != nil {
			t.Fatal(err)
		}
		if response.Code == http.StatusCreated {
			created++
		}
		if book.Duplicate != (response.Code == http.StatusOK) {
			t.Errorf("upload %d: inconsistent duplicate marker", i)
		}
		if id == "" {
			id = book.ID
		}
		if book.ID == "" || book.ID != id {
			t.Errorf("upload %d: canonical ID=%q, want %q", i, book.ID, id)
		}
	}
	if created != 1 || pd.Books.Len() != 1 || countLibraryEPUBs(t, pd.LibPath) != 1 {
		t.Errorf("deduplication: created=%d cached=%d", created, pd.Books.Len())
	}
	summary, found, err := pd.DB.GetBookSummaryContext(t.Context(), id)
	if err != nil || !found {
		t.Fatalf("canonical row missing: found=%v err=%v", found, err)
	}
	if got, err := os.ReadFile(summary.FilePath); err != nil || !bytes.Equal(got, content) {
		t.Errorf("canonical EPUB lost or replaced: %v", err)
	}
}

func TestUploadBookHandlerFilenameCollisionsAndPortability(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"Same.epub", "NUL.epub", ".sayumi-upload-owned.epub"} {
		t.Run(filename, func(t *testing.T) {
			pd := newUploadTestDeps(t)
			for _, title := range []string{"First", "Second"} {
				content := minimalEPUBBytes(t, title)
				response := uploadEPUB(t, pd, filename, content)
				if response.Code != http.StatusCreated {
					t.Fatalf("upload %s: status=%d body=%s", title, response.Code, response.Body.String())
				}
				var book BookResponse
				if err := json.Unmarshal(response.Body.Bytes(), &book); err != nil {
					t.Fatal(err)
				}
				summary, found, err := pd.DB.GetBookSummaryContext(t.Context(), book.ID)
				if err != nil || !found || summary.Title != title {
					t.Fatalf("uploaded row: %+v found=%v err=%v", summary, found, err)
				}
				if got, err := os.ReadFile(summary.FilePath); err != nil || !bytes.Equal(got, content) {
					t.Errorf("installed EPUB content: %v", err)
				}
			}
			first := filepath.Join(pd.LibPath, sanitizeFilename(filename))
			if got, err := os.ReadFile(first); err != nil || !bytes.Equal(got, minimalEPUBBytes(t, "First")) {
				t.Errorf("second upload clobbered first: %v", err)
			}
			if countLibraryEPUBs(t, pd.LibPath) != 2 || pd.Books.Len() != 2 {
				t.Error("distinct content did not remain two discoverable books")
			}
		})
	}
}

func TestUploadBookHandlerWarmsDuplicateCacheMiss(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	content := minimalEPUBBytes(t, "Cache miss")
	first := uploadEPUB(t, pd, "First.epub", content)
	var initial BookResponse
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil || first.Code != http.StatusCreated {
		t.Fatalf("initial upload: status=%d err=%v", first.Code, err)
	}
	pd.Books.Remove(initial.ID)
	duplicate := uploadEPUB(t, pd, "Other.epub", content)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate: status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	if book, found := pd.Books.Get(initial.ID); !found || book.Title != "Cache miss" {
		t.Errorf("duplicate did not warm the authoritative row: %+v, %v", book, found)
	}
	if countLibraryEPUBs(t, pd.LibPath) != 1 {
		t.Error("duplicate installed another file")
	}
}

func TestUploadBookHandlerRejectsInvalidEPUB(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	response := uploadEPUB(t, pd, "invalid.epub", []byte("not a ZIP archive"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), pd.LibPath) {
		t.Error("validation response exposed a filesystem path")
	}
	if countLibraryEPUBs(t, pd.LibPath) != 0 || pd.Books.Len() != 0 {
		t.Error("invalid upload left an installed book")
	}
}

func TestUploadBookHandlerIsolatesProfiles(t *testing.T) {
	t.Parallel()

	first, second := newUploadTestDeps(t), newUploadTestDeps(t)
	content := minimalEPUBBytes(t, "Private")
	for _, pd := range []*profileDeps{first, second} {
		response := uploadEPUB(t, pd, "Private.epub", content)
		if response.Code != http.StatusCreated {
			t.Fatalf("profile-local upload: status=%d body=%s", response.Code, response.Body.String())
		}
		if pd.Books.Len() != 1 || countLibraryEPUBs(t, pd.LibPath) != 1 {
			t.Error("upload crossed profile cache or filesystem boundaries")
		}
	}
}

func TestRemoveDuplicateUploadPreservesCanonicalFile(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"same path", "case alias", "distinct case-sensitive path", "distinct hard link"} {
		t.Run(name, func(t *testing.T) {
			pd := newUploadTestDeps(t)
			canonical := filepath.Join(pd.LibPath, "Canonical.epub")
			if err := os.WriteFile(canonical, []byte("canonical"), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := canonical
			switch name {
			case "case alias":
				dest = filepath.Join(pd.LibPath, "canonical.epub")
				if pd.DB.PathKey(dest) != pd.DB.PathKey(canonical) {
					t.Skip("volume treats differently cased paths as distinct")
				}
			case "distinct case-sensitive path":
				dest = filepath.Join(pd.LibPath, "canonical.epub")
				if pd.DB.PathKey(dest) == pd.DB.PathKey(canonical) {
					t.Skip("volume folds path case")
				}
				if err := os.WriteFile(dest, []byte("canonical"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "distinct hard link":
				dest = filepath.Join(pd.LibPath, "redundant.epub")
				if err := os.Link(canonical, dest); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}
			if err := removeDuplicateUpload(pd.DB, dest, canonical); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(canonical); err != nil || string(got) != "canonical" {
				t.Errorf("cleanup removed the canonical file: %q, %v", got, err)
			}
			if strings.HasPrefix(name, "distinct ") {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Errorf("redundant distinct path retained: %v", err)
				}
			}
		})
	}
}

func waitUploadDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("upload handler did not finish")
	}
}

// Re-uploading the same bytes is a success that added nothing. The status is
// 200 instead of 201, but the client's fetch wrapper never sees the status, so
// the response has to say so in the body.
func TestUploadBookHandlerFlagsDuplicate(t *testing.T) {
	t.Parallel()

	pd := newUploadTestDeps(t)
	content := minimalEPUBBytes(t, "Twice")
	if first := uploadEPUB(t, pd, "Twice.epub", content); first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want %d; body = %s",
			first.Code, http.StatusCreated, first.Body.String())
	}

	recorder := uploadEPUB(t, pd, "Twice.epub", content)
	if recorder.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want %d; body = %s",
			recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var resp BookResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Duplicate {
		t.Errorf("duplicate = false, want true; body = %s", recorder.Body.String())
	}
	if resp.Title != "Twice" {
		t.Errorf("title = %q, want Twice", resp.Title)
	}

	// The deduped upload must not leave a second copy behind.
	if got := countLibraryEPUBs(t, pd.LibPath); got != 1 {
		t.Errorf("library holds %d EPUBs, want 1", got)
	}
}
