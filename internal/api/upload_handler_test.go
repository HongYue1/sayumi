package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

// newUploadTestDeps wires the real collaborators the upload handler reaches
// through: it stages into LibPath, hashes and imports with the Scanner, warms
// the epub Store, and enriches the response from the DB and the coalescer. A
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

// countLibraryEPUBs counts library files, skipping the dot-prefixed staging
// temporaries so a leftover stage would not be miscounted as an import.
func countLibraryEPUBs(t *testing.T, libPath string) int {
	t.Helper()

	entries, err := os.ReadDir(libPath)
	if err != nil {
		t.Fatalf("read library dir: %v", err)
	}
	count := 0
	for _, entry := range entries {
		name := entry.Name()
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
