package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
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

func TestApplyBookMetaPatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		curTitle  string
		curAuthor string
		req       updateBookRequest
		wantTitle string
		wantAuth  string
		wantErr   string
	}{
		{
			name:      "omit both keeps current",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{},
			wantTitle: "Old",
			wantAuth:  "Ada",
		},
		{
			name:      "title only",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Title: new("  New Title  ")},
			wantTitle: "New Title",
			wantAuth:  "Ada",
		},
		{
			name:      "author only including empty",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Author: new("  ")},
			wantTitle: "Old",
			wantAuth:  "",
		},
		{
			name:      "both fields",
			curTitle:  "Old",
			curAuthor: "Ada",
			req:       updateBookRequest{Title: new("T"), Author: new("B")},
			wantTitle: "T",
			wantAuth:  "B",
		},
		{
			name:     "empty title rejected",
			curTitle: "Old",
			req:      updateBookRequest{Title: new("   ")},
			wantErr:  "title must not be empty",
		},
		{
			name:     "title too long",
			curTitle: "Old",
			req:      updateBookRequest{Title: new(strings.Repeat("x", maxBookTitleLen+1))},
			wantErr:  "title too long",
		},
		{
			name:      "author too long",
			curTitle:  "Old",
			curAuthor: "A",
			req:       updateBookRequest{Author: new(strings.Repeat("y", maxBookAuthorLen+1))},
			wantErr:   "author too long",
		},
		{
			name:      "max length title ok",
			curTitle:  "Old",
			curAuthor: "A",
			req:       updateBookRequest{Title: new(strings.Repeat("z", maxBookTitleLen))},
			wantTitle: strings.Repeat("z", maxBookTitleLen),
			wantAuth:  "A",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotTitle, gotAuth, errMsg := applyBookMetaPatch(tc.curTitle, tc.curAuthor, tc.req)
			if tc.wantErr != "" {
				if errMsg != tc.wantErr {
					t.Fatalf("errMsg = %q, want %q", errMsg, tc.wantErr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("unexpected errMsg %q", errMsg)
			}
			if gotTitle != tc.wantTitle || gotAuth != tc.wantAuth {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotTitle, gotAuth, tc.wantTitle, tc.wantAuth)
			}
		})
	}
}

func bookEditPNG(t *testing.T, red uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(x, y, color.RGBA{R: red, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func bookEditProfile(t *testing.T, fileless bool) *profileDeps {
	t.Helper()
	pd := bookFileProfile(t)
	pd.refs = 1 // The auth middleware, not these handlers, owns this reference.
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	bookPath := filepath.Join(pd.LibPath, "book.epub")
	jpegData, err := library.EncodeCoverJPEG(t.Context(), enrichBookID, bookEditPNG(t, 40))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`},
		{"OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Enriched</dc:title><dc:creator>Ada</dc:creator><meta name="cover" content="cover"/></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="cover" href="cover.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"OPS/chapter.xhtml", "<html><body>Unchanged chapter</body></html>"},
		{"OPS/cover.jpg", string(jpegData)},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bookPath, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, size, err := library.HashFile(t.Context(), bookPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pd.DB.UpdateBookMetadataAndFileContext(t.Context(), enrichBookID, "Enriched", "Ada", hash, size); err != nil {
		t.Fatal(err)
	}
	if _, err := library.WriteCoverImageJPEG(pd.LibPath, enrichBookID, jpegData); err != nil {
		t.Fatal(err)
	}
	if fileless {
		if err := pd.DB.UpdateBookFilePathContext(t.Context(), enrichBookID, ""); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(bookPath); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pd.DB.ExecContext(t.Context(), `UPDATE books SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, enrichBookID); err != nil {
		t.Fatal(err)
	}
	book, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
	if err != nil {
		t.Fatal(err)
	}
	pd.Books.Add(book)
	t.Cleanup(func() {
		pd.Store.CloseBook(bookPath)
		for _, dir := range []string{pd.LibPath, filepath.Join(pd.LibPath, ".sayumi", "covers")} {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Error(err)
				continue
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".bak") || strings.HasSuffix(entry.Name(), ".tmp") {
					t.Errorf("edit left temporary file %s", filepath.Join(dir, entry.Name()))
				}
			}
		}
		if pd.refs != 1 {
			t.Errorf("handler changed middleware-owned profile reference: %d", pd.refs)
		}
		if pd.bookEditMu.TryLock() {
			pd.bookEditMu.Unlock()
		} else {
			t.Error("edit lock leaked")
		}
		if pd.bookReplaceMu.TryLock() {
			pd.bookReplaceMu.Unlock()
		} else {
			t.Error("replacement gate leaked")
		}
	})
	return pd
}

func bookEditRequest(t *testing.T, pd *profileDeps, cover bool) *http.Request {
	t.Helper()
	var r *http.Request
	if cover {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("cover", "cover.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(bookEditPNG(t, 220)); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/books/"+enrichBookID+"/cover", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
	} else {
		r = httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/books/"+enrichBookID, strings.NewReader(`{"title":"  New title  "}`))
	}
	r.SetPathValue("id", enrichBookID)
	return withProfileDeps(r, pd)
}

func serveBookEdit(w http.ResponseWriter, r *http.Request, cover bool) {
	if cover {
		uploadCoverHandler(nil)(w, r)
	} else {
		updateBookHandler(nil)(w, r)
	}
}

func assertBookEditUnchanged(t *testing.T, pd *profileDeps, before storage.BookRecord, epubBytes, coverBytes []byte) {
	t.Helper()
	after, err := pd.DB.GetBookContext(t.Context(), before.ID)
	if err != nil || after != before {
		t.Errorf("failed edit changed database: %+v, %v; want %+v", after, err, before)
	}
	cached, ok := pd.Books.Get(before.ID)
	if !ok || cached.BookSummary != before.BookSummary {
		t.Errorf("failed edit changed cache: %+v, found %v", cached, ok)
	}
	for path, want := range map[string][]byte{
		before.FilePath: epubBytes,
		filepath.Join(pd.LibPath, before.CoverPath): coverBytes,
	} {
		if path == "" {
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("failed edit changed %s: %v", path, err)
		}
	}
}

func TestBookEditMetadataValidationAndNoop(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		body     string
		fileless bool
		status   int
		code     string
	}{
		{name: "invalid JSON", body: `{"title":`, status: http.StatusBadRequest, code: "invalid_body"},
		{name: "second JSON value", body: `{"title":"New"} {}`, status: http.StatusBadRequest, code: "invalid_body"},
		{name: "trailing junk", body: `{"title":"New"}!`, status: http.StatusBadRequest, code: "invalid_body"},
		{name: "oversized JSON", body: `{"title":"` + strings.Repeat("x", maxJSONBodySize) + `"}`, status: http.StatusRequestEntityTooLarge, code: "too_large"},
		{name: "oversized trailing whitespace", body: `{}` + strings.Repeat(" ", maxJSONBodySize), status: http.StatusRequestEntityTooLarge, code: "too_large"},
		{name: "empty title", body: `{"title":"   "}`, status: http.StatusBadRequest, code: "invalid"},
		{name: "long title", body: `{"title":"` + strings.Repeat("x", maxBookTitleLen+1) + `"}`, status: http.StatusBadRequest, code: "invalid"},
		{name: "long author", body: `{"author":"` + strings.Repeat("x", maxBookAuthorLen+1) + `"}`, status: http.StatusBadRequest, code: "invalid"},
		{name: "omitted fields", body: `{}`, status: http.StatusOK},
		{name: "null fields", body: `{"title":null,"author":null}`, status: http.StatusOK},
		{name: "trimmed no-op", body: `{"title":"  Enriched  ","author":" Ada "}`, status: http.StatusOK},
		{name: "fileless no-op", body: `{}`, fileless: true, status: http.StatusOK},
		{name: "fileless change", body: `{"title":"New"}`, fileless: true, status: http.StatusUnprocessableEntity, code: "no_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, tc.fileless)
			before, _ := pd.Books.Get(enrichBookID)
			var original []byte
			if !tc.fileless {
				original = readBookEditFile(t, before.FilePath)
			}
			oldCover := readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath))
			r := bookEditRequest(t, pd, false)
			r.Body = io.NopCloser(strings.NewReader(tc.body))
			r.ContentLength = int64(len(tc.body))
			w := httptest.NewRecorder()
			updateBookHandler(nil)(w, r)
			var response apiError
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || response.Code != tc.code {
				t.Fatalf("response = %d %+v, want %d %s", w.Code, response, tc.status, tc.code)
			}
			assertBookEditUnchanged(t, pd, before, original, oldCover)
		})
	}
}

func TestBookEditCoverValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		field       string
		data        []byte
		noMultipart bool
		status      int
		code        string
	}{
		{name: "not multipart", noMultipart: true, status: http.StatusBadRequest, code: "invalid"},
		{name: "missing cover field", field: "wrong", data: bookEditPNG(t, 220), status: http.StatusBadRequest, code: "invalid"},
		{name: "invalid image", field: "cover", data: []byte("not an image"), status: http.StatusBadRequest, code: "invalid"},
		{name: "oversized cover", field: "cover", data: bytes.Repeat([]byte{'x'}, maxCoverUploadBytes+1), status: http.StatusRequestEntityTooLarge, code: "too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, false)
			before, _ := pd.Books.Get(enrichBookID)
			original := readBookEditFile(t, before.FilePath)
			oldCover := readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath))
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile(tc.field, "cover.png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(tc.data); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/books/"+enrichBookID+"/cover", &body)
			r.SetPathValue("id", enrichBookID)
			if !tc.noMultipart {
				r.Header.Set("Content-Type", form.FormDataContentType())
			}
			w := httptest.NewRecorder()
			uploadCoverHandler(nil)(w, withProfileDeps(r, pd))
			var response apiError
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || response.Code != tc.code {
				t.Fatalf("response = %d %+v, want %d %s", w.Code, response, tc.status, tc.code)
			}
			assertBookEditUnchanged(t, pd, before, original, oldCover)
		})
	}
}

func TestBookEditFailuresPreserveGeneration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		cover     bool
		fileless  bool
		duplicate bool
	}{
		{name: "metadata duplicate", duplicate: true},
		{name: "cover duplicate", cover: true, duplicate: true},
		{name: "metadata database failure"},
		{name: "cover database failure", cover: true},
		{name: "fileless cover database failure", cover: true, fileless: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, tc.fileless)
			before, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
			if err != nil {
				t.Fatal(err)
			}
			var epubBytes []byte
			if !tc.fileless {
				epubBytes, err = os.ReadFile(before.FilePath)
				if err != nil {
					t.Fatal(err)
				}
			}
			coverBytes, err := os.ReadFile(filepath.Join(pd.LibPath, before.CoverPath))
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantCode := http.StatusInternalServerError, "db_error"
			if tc.duplicate {
				edit := epub.MetadataEdit{Title: new("New title"), Author: new("Ada")}
				if tc.cover {
					jpegData, err := library.EncodeCoverJPEG(t.Context(), enrichBookID, bookEditPNG(t, 220))
					if err != nil {
						t.Fatal(err)
					}
					edit = epub.MetadataEdit{CoverJPEG: jpegData}
				}
				tmp, err := epub.RewriteBook(before.FilePath, edit)
				if err != nil {
					t.Fatal(err)
				}
				hash, size, err := library.HashFile(t.Context(), tmp)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(tmp); err != nil {
					t.Fatal(err)
				}
				other := before
				other.ID, other.FilePath, other.FileHash, other.FileSize = "duplicate", filepath.Join(pd.LibPath, "duplicate.epub"), hash, size
				if inserted, err := pd.DB.InsertBookContext(t.Context(), other); err != nil || inserted != other.ID {
					t.Fatalf("insert duplicate: %v, %v", inserted, err)
				}
				wantStatus, wantCode = http.StatusConflict, "duplicate"
			} else {
				// A real SQLite failure after file publication, not a mock that
				// sidesteps the handler's prepare/replace/commit ordering.
				if _, err := pd.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_edit BEFORE UPDATE ON books BEGIN SELECT RAISE(ABORT, 'injected edit failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			serveBookEdit(w, bookEditRequest(t, pd, tc.cover), tc.cover)
			var response apiError
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != wantStatus || response.Code != wantCode {
				t.Fatalf("response = %d %+v, want %d %s", w.Code, response, wantStatus, wantCode)
			}
			assertBookEditUnchanged(t, pd, before, epubBytes, coverBytes)
		})
	}
}

func TestBookEditRollbackRemovesNewCanonicalCover(t *testing.T) {
	t.Parallel()
	for _, fileless := range []bool{false, true} {
		name := "embedded"
		if fileless {
			name = "fileless"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, fileless)
			canonical := filepath.Join(pd.LibPath, library.CoverRelPath(enrichBookID))
			oldCover := readBookEditFile(t, canonical)
			// The writer publishes to the canonical path, not to cover_path.
			// A failed edit must remove its new sidecar and leave this legacy
			// cover referenced by the unchanged row alone.
			if err := os.Rename(canonical, filepath.Join(pd.LibPath, "legacy-cover.jpg")); err != nil {
				t.Fatal(err)
			}
			if _, err := pd.DB.ExecContext(t.Context(), `UPDATE books SET cover_path = 'legacy-cover.jpg' WHERE id = ?`, enrichBookID); err != nil {
				t.Fatal(err)
			}
			before, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
			if err != nil {
				t.Fatal(err)
			}
			pd.Books.Add(before)
			var original []byte
			if !fileless {
				original = readBookEditFile(t, before.FilePath)
			}
			if _, err := pd.DB.ExecContext(t.Context(), `CREATE TRIGGER fail_edit BEFORE UPDATE ON books BEGIN SELECT RAISE(ABORT, 'injected edit failure'); END`); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			uploadCoverHandler(nil)(w, bookEditRequest(t, pd, true))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("response = %d %s", w.Code, w.Body.String())
			}
			assertBookEditUnchanged(t, pd, before, original, oldCover)
			if _, err := os.Stat(canonical); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("uncommitted canonical cover remains: %v", err)
			}
		})
	}
}

func TestBookEditCoverWriteFailurePreservesEPUB(t *testing.T) {
	t.Parallel()
	pd := bookEditProfile(t, false)
	before, _ := pd.Books.Get(enrichBookID)
	original, err := os.ReadFile(before.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	coverPath := filepath.Join(pd.LibPath, before.CoverPath)
	if err := os.Remove(coverPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(coverPath, 0o755); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	uploadCoverHandler(nil)(w, bookEditRequest(t, pd, true))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(before.FilePath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("sidecar failure changed EPUB while database still describes the original: %v", err)
	}
}

type bookEditGateWriter struct {
	*httptest.ResponseRecorder
	t  *testing.T
	pd *profileDeps
}

func (w *bookEditGateWriter) WriteHeader(status int) {
	if w.pd.bookReplaceMu.TryRLock() {
		w.pd.bookReplaceMu.RUnlock()
		w.t.Error("cover publication did not retain the replacement write gate through its response")
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestBookEditFilelessCoverUsesReplacementGate(t *testing.T) {
	t.Parallel()
	pd := bookEditProfile(t, true)
	w := &bookEditGateWriter{ResponseRecorder: httptest.NewRecorder(), t: t, pd: pd}
	uploadCoverHandler(nil)(w, bookEditRequest(t, pd, true))
	if w.Code != http.StatusOK {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestBookEditReloadFailureDropsStaleCache(t *testing.T) {
	t.Parallel()
	pd := bookEditProfile(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pd.bookReplaceMu.Lock()
	_, err := refreshBookCache(ctx, pd, enrichBookID)
	pd.bookReplaceMu.Unlock()
	if err == nil {
		t.Fatal("reload with canceled context succeeded")
	}
	if _, ok := pd.Books.Get(enrichBookID); ok {
		t.Fatal("reload failure left a stale generation available to readers")
	}
}

func TestBookEditVersionAlwaysAdvances(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cover    bool
		fileless bool
	}{
		{name: "metadata after cover"},
		{name: "embedded cover", cover: true},
		{name: "fileless cover", cover: true, fileless: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, tc.fileless)
			// A future seed models a backward wall-clock adjustment and removes
			// dependence on whether this test straddles a second boundary. Cover
			// URLs contain only updated_at: even with no-cache, a mounted image
			// needs a changed src to refetch. Metadata must not reset that key.
			previous := "2099-01-01 00:00:00.000"
			if _, err := pd.DB.ExecContext(t.Context(), `UPDATE books SET updated_at = ? WHERE id = ?`, previous, enrichBookID); err != nil {
				t.Fatal(err)
			}
			before, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
			if err != nil {
				t.Fatal(err)
			}
			pd.Books.Add(before)
			for i := range 2 {
				r := bookEditRequest(t, pd, tc.cover)
				if !tc.cover && i == 1 {
					r.Body = io.NopCloser(strings.NewReader(`{"title":"Another title"}`))
				}
				w := httptest.NewRecorder()
				serveBookEdit(w, r, tc.cover)
				if w.Code != http.StatusOK {
					t.Fatalf("response = %d %s", w.Code, w.Body.String())
				}
				after, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
				if err != nil {
					t.Fatal(err)
				}
				if after.UpdatedAt <= previous {
					t.Fatalf("cover URL version did not advance: %q -> %q", previous, after.UpdatedAt)
				}
				if tc.fileless && after.FileHash != before.FileHash {
					t.Fatal("sidecar-only edit changed the EPUB hash")
				}
				cached, _ := pd.Books.Get(enrichBookID)
				var response BookResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if cached.UpdatedAt != after.UpdatedAt || response.UpdatedAt != after.UpdatedAt {
					t.Fatal("database, cache, and response cover versions disagree")
				}
				previous = after.UpdatedAt
			}
		})
	}
}

func readBookEditFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readBookEditEntry(t *testing.T, pd *profileDeps, filePath, entry string) []byte {
	t.Helper()
	reader, err := pd.Store.OpenResource(filePath, entry)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBookEditSuccessPublishesOneGeneration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cover    bool
		fileless bool
	}{
		{name: "metadata"},
		{name: "embedded cover", cover: true},
		{name: "fileless cover", cover: true, fileless: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, tc.fileless)
			otherProfile := bookEditProfile(t, false)
			otherBook, _ := otherProfile.Books.Get(enrichBookID)
			otherEPUB := readBookEditFile(t, otherBook.FilePath)
			otherCover := readBookEditFile(t, filepath.Join(otherProfile.LibPath, otherBook.CoverPath))
			before, _ := pd.Books.Get(enrichBookID)
			oldCover := readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath))
			if !tc.fileless {
				// Warm an idle ZIP reader: publication must evict its old metadata.
				readBookEditEntry(t, pd, before.FilePath, "OPS/book.opf")
			}
			pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "default", Chapter: 9, UpdatedAt: "2026-02-02 00:00:00"})
			if err := pd.DB.SetBookFlairCheckedContext(t.Context(), enrichBookID, "default", "reading", map[string]struct{}{"reading": {}}); err != nil {
				t.Fatal(err)
			}

			r := bookEditRequest(t, pd, tc.cover)
			wantTitle, wantAuthor := before.Title, before.Author
			if !tc.cover {
				r.Body = io.NopCloser(strings.NewReader(`{"title":"  New & <title> 新  ","author":""}`))
				wantTitle, wantAuthor = "New & <title> 新", ""
			}
			w := &bookEditGateWriter{ResponseRecorder: httptest.NewRecorder(), t: t, pd: pd}
			serveBookEdit(w, r, tc.cover)
			if w.Code != http.StatusOK {
				t.Fatalf("response = %d %s", w.Code, w.Body.String())
			}
			after, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
			if err != nil {
				t.Fatal(err)
			}
			cached, ok := pd.Books.Get(enrichBookID)
			if !ok || cached.BookSummary != after.BookSummary || after.Title != wantTitle || after.Author != wantAuthor {
				t.Fatalf("published cache/metadata disagree with database: %+v / %+v", cached, after)
			}
			var response BookResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Title != after.Title || response.Author != after.Author || response.UpdatedAt != after.UpdatedAt || response.Progress != 0.9 || response.LastReadAt != "2026-02-02 00:00:00" || response.FlairID != "reading" {
				t.Fatalf("edit response lost metadata/progress/flair: %+v", response)
			}
			wantCover := oldCover
			if tc.cover {
				wantCover, err = library.EncodeCoverJPEG(t.Context(), enrichBookID, bookEditPNG(t, 220))
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(readBookEditFile(t, filepath.Join(pd.LibPath, after.CoverPath)), wantCover) {
				t.Fatal("published sidecar differs from normalized upload/original")
			}
			if !tc.fileless {
				hash, size, err := library.HashFile(t.Context(), after.FilePath)
				if err != nil || hash != after.FileHash || size != after.FileSize || hash == before.FileHash {
					t.Fatalf("published EPUB fingerprint = %s/%d, %v; database = %s/%d", hash, size, err, after.FileHash, after.FileSize)
				}
				var opf struct {
					Metadata struct {
						Title  string `xml:"title"`
						Author string `xml:"creator"`
					} `xml:"metadata"`
				}
				if err := xml.Unmarshal(readBookEditEntry(t, pd, after.FilePath, "OPS/book.opf"), &opf); err != nil {
					t.Fatal(err)
				}
				if opf.Metadata.Title != wantTitle || opf.Metadata.Author != wantAuthor {
					t.Fatalf("EPUB and database metadata disagree: %+v", opf.Metadata)
				}
				if !bytes.Equal(readBookEditEntry(t, pd, after.FilePath, "OPS/cover.jpg"), wantCover) || string(readBookEditEntry(t, pd, after.FilePath, "OPS/chapter.xhtml")) != "<html><body>Unchanged chapter</body></html>" {
					t.Fatal("EPUB cover or untouched chapter changed incorrectly")
				}
			} else if after.FileHash != before.FileHash || after.FileSize != before.FileSize {
				t.Fatal("sidecar-only edit fabricated an EPUB fingerprint")
			}
			coverRequest := bookTestRequest(t, pd, http.MethodGet, "/api/books/"+enrichBookID+"/cover")
			coverRequest.Header.Set("If-None-Match", coverResponseETag(before.FileHash, before.UpdatedAt))
			coverResponse := httptest.NewRecorder()
			getCoverHandler(nil)(coverResponse, coverRequest)
			if coverResponse.Code != http.StatusOK || !bytes.Equal(coverResponse.Body.Bytes(), wantCover) || coverResponse.Header().Get("ETag") != coverResponseETag(after.FileHash, after.UpdatedAt) {
				t.Fatal("cover served old validator or wrong generation after edit")
			}
			assertBookEditUnchanged(t, otherProfile, otherBook, otherEPUB, otherCover)
		})
	}
}

func TestBookEditPinnedReaderConflictsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, cover := range []bool{false, true} {
		name := "metadata"
		if cover {
			name = "cover"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, false)
			before, _ := pd.Books.Get(enrichBookID)
			original := readBookEditFile(t, before.FilePath)
			oldCover := readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath))
			reader, err := pd.Store.OpenResource(before.FilePath, "OPS/chapter.xhtml")
			if err != nil {
				t.Fatal(err)
			}
			closeReader := sync.OnceFunc(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			})
			defer closeReader()
			w := httptest.NewRecorder()
			serveBookEdit(w, bookEditRequest(t, pd, cover), cover)
			var response apiError
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusConflict || response.Code != "book_open" {
				t.Fatalf("pinned reader response = %d %+v", w.Code, response)
			}
			assertBookEditUnchanged(t, pd, before, original, oldCover)
			closeReader()
			w = httptest.NewRecorder()
			serveBookEdit(w, bookEditRequest(t, pd, cover), cover)
			if w.Code != http.StatusOK {
				t.Fatalf("retry after releasing reader = %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func waitBookEditCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

func waitBookEditDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("book edit worker did not finish")
	}
}

// Pause preparation's first cancellation check, but not cover decoding, without
// adding process-global test hooks to production handlers or storage.
type bookEditPrepareContext struct {
	context.Context
	pd      *profileDeps
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (c *bookEditPrepareContext) Err() error {
	if c.pd.bookEditMu.TryLock() {
		c.pd.bookEditMu.Unlock()
		return c.Context.Err()
	}
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.Context.Err()
}

func TestBookEditDeletionWinsPreparationHandoff(t *testing.T) {
	t.Parallel()
	for _, cover := range []bool{false, true} {
		name := "metadata"
		if cover {
			name = "cover"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, false)
			before, _ := pd.Books.Get(enrichBookID)
			r := bookEditRequest(t, pd, cover)
			ctx := &bookEditPrepareContext{Context: r.Context(), pd: pd, entered: make(chan struct{}), release: make(chan struct{})}
			unblock := sync.OnceFunc(func() { close(ctx.release) })
			w := httptest.NewRecorder()
			editDone := make(chan struct{})
			go func() {
				defer close(editDone)
				serveBookEdit(w, r.WithContext(ctx), cover)
			}()
			defer func() {
				unblock()
				waitBookEditDone(t, editDone)
			}()
			waitBookEditDone(t, ctx.entered)
			if pd.bookReplaceMu.TryLock() {
				pd.bookReplaceMu.Unlock()
				t.Fatal("preparation does not exclude deletion")
			}
			if !pd.bookReplaceMu.TryRLock() {
				t.Fatal("preparation unnecessarily blocks normal readers")
			}
			pd.bookReplaceMu.RUnlock()
			deleteRequest := bookTestRequest(t, pd, http.MethodDelete, "/api/books/"+enrichBookID)
			deleted := httptest.NewRecorder()
			deleteDone := make(chan struct{})
			go func() {
				defer close(deleteDone)
				deleteBookHandler(nil)(deleted, deleteRequest)
			}()
			defer func() {
				unblock()
				waitBookEditDone(t, deleteDone)
			}()
			// With preparation still holding the read gate, a refused new read
			// proves deletion is queued for the write side before the handoff.
			waitBookEditCondition(t, "queued deletion", func() bool {
				if pd.bookReplaceMu.TryRLock() {
					pd.bookReplaceMu.RUnlock()
					return false
				}
				return true
			})
			unblock()
			waitBookEditDone(t, deleteDone)
			waitBookEditDone(t, editDone)
			if deleted.Code != http.StatusNoContent || w.Code != http.StatusNotFound {
				t.Fatalf("delete/edit responses = %d / %d: %s", deleted.Code, w.Code, w.Body.String())
			}
			if _, found, err := pd.DB.GetBookSummaryContext(t.Context(), enrichBookID); err != nil || found {
				t.Fatalf("deleted row returned: found %v, %v", found, err)
			}
			if _, ok := pd.Books.Get(enrichBookID); ok {
				t.Fatal("deleted cache entry returned")
			}
			for _, path := range []string{before.FilePath, filepath.Join(pd.LibPath, before.CoverPath)} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("deleted file resurrected: %s, %v", path, err)
				}
			}
		})
	}
}

func TestBookEditCommitSurvivesPostInstallCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cover    bool
		fileless bool
	}{
		{name: "metadata"},
		{name: "cover", cover: true},
		{name: "fileless cover", cover: true, fileless: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, tc.fileless)
			before, _ := pd.Books.Get(enrichBookID)
			oldCover := readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath))
			// Reserve the entire SQL pool. The first queued query is the edit's
			// post-install commit, giving a deterministic cancellation boundary.
			pd.DB.SetMaxOpenConns(1)
			conn, err := pd.DB.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			releaseConn := sync.OnceFunc(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			waits := pd.DB.Stats().WaitCount
			r := bookEditRequest(t, pd, tc.cover)
			ctx, cancel := context.WithCancel(r.Context())
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				serveBookEdit(w, r.WithContext(ctx), tc.cover)
			}()
			defer func() {
				cancel()
				releaseConn()
				waitBookEditDone(t, done)
			}()
			waitBookEditCondition(t, "post-install SQL commit", func() bool { return pd.DB.Stats().WaitCount > waits })
			if pd.bookReplaceMu.TryRLock() {
				pd.bookReplaceMu.RUnlock()
				t.Fatal("replacement gate released before database commit")
			}
			if !tc.fileless {
				hash, _, err := library.HashFile(t.Context(), before.FilePath)
				if err != nil || hash == before.FileHash {
					t.Fatalf("cancellation boundary preceded file installation: %v", err)
				}
			} else if bytes.Equal(readBookEditFile(t, filepath.Join(pd.LibPath, before.CoverPath)), oldCover) {
				t.Fatal("cancellation boundary preceded sidecar installation")
			}
			cancel()
			releaseConn()
			waitBookEditDone(t, done)
			if w.Code != http.StatusOK {
				t.Fatalf("post-install cancellation abandoned commit: %d %s", w.Code, w.Body.String())
			}
			after, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
			if err != nil {
				t.Fatal(err)
			}
			cached, ok := pd.Books.Get(enrichBookID)
			if !ok || cached.BookSummary != after.BookSummary || after.UpdatedAt == before.UpdatedAt {
				t.Fatal("canceled request left file/database/cache generations unreconciled")
			}
			if !tc.fileless {
				hash, size, err := library.HashFile(t.Context(), after.FilePath)
				if err != nil || hash != after.FileHash || size != after.FileSize {
					t.Fatalf("canceled edit committed an incorrect fingerprint: %v", err)
				}
			}
		})
	}
}

func TestBookEditRollbackFailureRetainsRecoveryCopy(t *testing.T) {
	t.Parallel()
	pd := bookEditProfile(t, false)
	path := library.CoverRelPath(enrichBookID)
	original := readBookEditFile(t, filepath.Join(pd.LibPath, path))
	backup, err := backupBookEditFile(t.Context(), pd.coverRoot, path)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.cleanup()
	if err := pd.coverRoot.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := pd.coverRoot.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	pd.bookReplaceMu.Lock()
	ok := rollbackBookEdit(pd, enrichBookID, &backup)
	pd.bookReplaceMu.Unlock()
	if ok || !backup.keep {
		t.Fatal("failed rollback did not preserve its backup")
	}
	backup.cleanup()
	if !bytes.Equal(readBookEditFile(t, filepath.Join(pd.LibPath, backup.tempPath)), original) {
		t.Fatal("original recovery bytes were lost")
	}
	if _, ok := pd.Books.Get(enrichBookID); ok {
		t.Fatal("failed rollback left stale resource tokens available")
	}
	// Explicitly remove the deliberately retained recovery fixture.
	if err := pd.coverRoot.Remove(backup.tempPath); err != nil {
		t.Fatal(err)
	}
}

func TestBookEditDeadlineClearedBeforeEditWait(t *testing.T) {
	t.Parallel()
	for _, cover := range []bool{false, true} {
		name := "metadata"
		if cover {
			name = "cover"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pd := bookEditProfile(t, false)
			pd.bookEditMu.Lock()
			unlock := sync.OnceFunc(pd.bookEditMu.Unlock)
			w := &searchDeadlineResponseWriter{ResponseRecorder: httptest.NewRecorder(), cleared: make(chan struct{})}
			r := bookEditRequest(t, pd, cover)
			done := make(chan struct{})
			go func() {
				defer close(done)
				serveBookEdit(w, r, cover)
			}()
			defer func() {
				unlock()
				waitBookEditDone(t, done)
			}()
			waitBookEditDone(t, w.cleared)
			unlock()
			waitBookEditDone(t, done)
			if w.Code != http.StatusOK {
				t.Fatalf("queued edit = %d %s", w.Code, w.Body.String())
			}
		})
	}
}
