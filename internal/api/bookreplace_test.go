package api

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sayumi/internal/epub"
	"sayumi/internal/library"
	"sayumi/internal/storage"
)

// replaceTestEPUB builds a minimal EPUB whose spine is one document per href.
func replaceTestEPUB(t *testing.T, title string, hrefs ...string) []byte {
	t.Helper()
	var manifest, spine strings.Builder
	for i, href := range hrefs {
		fmt.Fprintf(&manifest, `<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, href)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
	}
	type entry struct{ name, body string }
	entries := make([]entry, 0, 3+len(hrefs))
	entries = append(entries,
		entry{"mimetype", "application/epub+zip"},
		entry{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`},
		entry{"OPS/book.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` +
			title + `</dc:title><dc:creator>Ada</dc:creator><dc:language>en</dc:language></metadata><manifest>` +
			manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`},
	)
	for _, href := range hrefs {
		entries = append(entries, entry{"OPS/" + href, "<html><body><p>" + href + "</p></body></html>"})
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range entries {
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
	return archive.Bytes()
}

// replaceTestProfile installs a three-chapter book with progress for two users
// and two bookmarks, all keyed to that spine.
func replaceTestProfile(t *testing.T) (*profileDeps, storage.BookRecord) {
	t.Helper()
	pd := bookEditProfile(t, false)
	book, ok := pd.Books.Get(enrichBookID)
	if !ok {
		t.Fatal("seed book missing")
	}
	pd.Store.CloseBook(book.FilePath)
	if err := os.WriteFile(book.FilePath, replaceTestEPUB(t, "Enriched", "c1.xhtml", "c2.xhtml", "c3.xhtml"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, size, err := library.HashFile(t.Context(), book.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pd.DB.UpdateBookMetadataAndFileContext(t.Context(), enrichBookID, "Enriched", "Ada", hash, size); err != nil {
		t.Fatal(err)
	}
	spine := []epub.SpineEntry{{Href: "OPS/c1.xhtml"}, {Href: "OPS/c2.xhtml"}, {Href: "OPS/c3.xhtml"}}
	zr, err := zip.OpenReader(book.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if meta, err := epub.Parse(&zr.Reader); err == nil {
		spine = meta.Spine // Use the parser's href form, whatever it is.
	}
	_ = zr.Close()
	spineJSON, err := json.Marshal(spine)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pd.DB.ExecContext(t.Context(), `UPDATE books SET spine_json = ?, chapter_count = ? WHERE id = ?`,
		string(spineJSON), len(spine), enrichBookID); err != nil {
		t.Fatal(err)
	}
	book, err = pd.DB.GetBookContext(t.Context(), enrichBookID)
	if err != nil {
		t.Fatal(err)
	}
	pd.Books.Add(book)

	for _, p := range []storage.ProgressRecord{
		{BookID: enrichBookID, UserID: "default", Chapter: 2, Percent: 0.5, CFI: sql.NullString{String: "cfi:1/2", Valid: true}},
		{BookID: enrichBookID, UserID: "other", Chapter: 0, Percent: 0.25},
	} {
		if err := pd.DB.SaveProgressContext(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []storage.BookmarkRecord{
		{ID: "bm-1", BookID: enrichBookID, UserID: "default", Chapter: 1, Percent: 0.1, CFI: sql.NullString{String: "cfi:3", Valid: true}, Label: "one"},
		{ID: "bm-2", BookID: enrichBookID, UserID: "default", Chapter: 2, Percent: 0.9, Label: "two"},
	} {
		if err := pd.DB.InsertBookmarkContext(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	return pd, book
}

func replaceRequest(t *testing.T, pd *profileDeps, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("epub", "novel.epub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/books/"+enrichBookID+"/replace", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.SetPathValue("id", enrichBookID)
	return withProfileDeps(r, pd)
}

func TestReplaceBookKeepsProgressAndBookmarks(t *testing.T) {
	t.Parallel()
	pd, before := replaceTestProfile(t)

	// Front matter is inserted before the old chapters and new chapters are
	// appended: every old chapter moves by one, and must be followed.
	newFile := replaceTestEPUB(t, "Novel Vol 1 (updated)", "front.xhtml", "c1.xhtml", "c2.xhtml", "c3.xhtml", "c4.xhtml")
	w := httptest.NewRecorder()
	replaceBookHandler(nil)(w, replaceRequest(t, pd, newFile))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var resp BookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != enrichBookID || resp.ChapterCount != 5 {
		t.Fatalf("response id/chapters = %q/%d, want %q/5", resp.ID, resp.ChapterCount, enrichBookID)
	}
	if resp.Title != "Enriched" {
		t.Fatalf("title = %q, want the library title to survive", resp.Title)
	}

	after, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
	if err != nil {
		t.Fatal(err)
	}
	if after.FileHash == before.FileHash || after.ChapterCount != 5 || after.UpdatedAt == before.UpdatedAt {
		t.Fatalf("row not advanced: hash changed=%v chapters=%d updated=%q", after.FileHash != before.FileHash, after.ChapterCount, after.UpdatedAt)
	}
	if cached, _ := pd.Books.Get(enrichBookID); cached.BookSummary != after.BookSummary {
		t.Fatal("cache not refreshed from the committed row")
	}
	spine, _, err := pd.Books.GetSpine(t.Context(), enrichBookID)
	if err != nil || len(spine) != 5 {
		t.Fatalf("cached spine = %d entries, err %v; want 5", len(spine), err)
	}

	// The installed bytes are the upload with the library title carried in.
	zr, err := zip.OpenReader(after.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := epub.Parse(&zr.Reader)
	_ = zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Enriched" || len(meta.Spine) != 5 {
		t.Fatalf("installed file title/chapters = %q/%d", meta.Title, len(meta.Spine))
	}

	progress, err := pd.DB.GetProgressContext(t.Context(), enrichBookID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Chapter != 3 || progress.Percent != 0.5 || progress.CFI.String != "cfi:1/2" {
		t.Fatalf("progress = %+v, want chapter 3 at 0.5 with its anchor", progress)
	}
	other, err := pd.DB.GetProgressContext(t.Context(), enrichBookID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if other.Chapter != 1 || other.Percent != 0.25 {
		t.Fatalf("other user's progress = %+v, want chapter 1 at 0.25", other)
	}

	marks, err := pd.DB.ListBookmarksContext(t.Context(), enrichBookID, "default")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]storage.BookmarkRecord{}
	for _, m := range marks {
		got[m.ID] = m
	}
	if m := got["bm-1"]; m.Chapter != 2 || m.CFI.String != "cfi:3" || m.Label != "one" {
		t.Fatalf("bm-1 = %+v, want chapter 2 with anchor and label", m)
	}
	if m := got["bm-2"]; m.Chapter != 3 || m.Percent != 0.9 {
		t.Fatalf("bm-2 = %+v, want chapter 3 at 0.9", m)
	}
}

func TestReplaceBookRemapsStagedProgress(t *testing.T) {
	t.Parallel()
	pd, _ := replaceTestProfile(t)
	pd.Progress.stage(storage.ProgressRecord{BookID: enrichBookID, UserID: "default", Chapter: 1, Percent: 0.7, CFI: sql.NullString{String: "cfi:9", Valid: true}})

	w := httptest.NewRecorder()
	replaceBookHandler(nil)(w, replaceRequest(t, pd, replaceTestEPUB(t, "Enriched", "front.xhtml", "c1.xhtml", "c2.xhtml", "c3.xhtml")))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	staged, ok := pd.Progress.get(enrichBookID, "default")
	if !ok {
		t.Fatal("staged progress dropped")
	}
	if staged.Chapter != 2 || staged.CFI.String != "cfi:9" {
		t.Fatalf("staged progress = %+v, want chapter 2 with anchor", staged)
	}
}

// A reader tab open across a replace still holds the old chapter numbering.
// Its next save must be refused rather than applied over the remapped row.
func TestReplaceBookRefusesProgressFromTheReplacedFile(t *testing.T) {
	t.Parallel()
	pd, _ := replaceTestProfile(t)

	readGeneration := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		getProgressHandler(nil)(w, progressRequest(pd, http.MethodGet, ""))
		var got progressBody
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Generation == "" {
			t.Fatal("read did not report a generation")
		}
		return got.Generation
	}
	storedChapter := func() int {
		t.Helper()
		if staged, ok := pd.Progress.get(enrichBookID, "default"); ok {
			return staged.Chapter
		}
		prog, err := pd.DB.GetProgressContext(t.Context(), enrichBookID, "default")
		if err != nil {
			t.Fatal(err)
		}
		return prog.Chapter
	}

	before := readGeneration()

	w := httptest.NewRecorder()
	replaceBookHandler(nil)(w, replaceRequest(t, pd, replaceTestEPUB(t, "Enriched", "front.xhtml", "c1.xhtml", "c2.xhtml", "c3.xhtml")))
	if w.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body %s", w.Code, w.Body.String())
	}
	// The stale tab's chapter 2 is where the remap has just moved it from.
	if got := storedChapter(); got != 3 {
		t.Fatalf("remapped chapter = %d, want 3", got)
	}
	stale := `{"chapter":2,"percent":0.5,"generation":"` + before + `"}`

	w = httptest.NewRecorder()
	putProgressHandler(nil)(w, progressRequest(pd, http.MethodPut, stale))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "stale_generation") {
		t.Fatalf("stale PUT = %d %s, want 409 stale_generation", w.Code, w.Body.String())
	}
	if got := storedChapter(); got != 3 {
		t.Fatalf("stale PUT moved the position to %d", got)
	}

	w = httptest.NewRecorder()
	beaconProgressHandler(nil)(w, progressRequest(pd, http.MethodPost, stale))
	if w.Code != http.StatusNoContent {
		t.Fatalf("stale beacon = %d, want 204", w.Code)
	}
	if got := storedChapter(); got != 3 {
		t.Fatalf("stale beacon moved the position to %d", got)
	}

	// A tab that reopened the book reads the new generation and saves again.
	after := readGeneration()
	if after == before {
		t.Fatal("generation did not change with the file")
	}
	w = httptest.NewRecorder()
	putProgressHandler(nil)(w, progressRequest(pd, http.MethodPut,
		`{"chapter":1,"percent":0.25,"generation":"`+after+`"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("fresh PUT = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := storedChapter(); got != 1 {
		t.Fatalf("fresh PUT stored chapter %d, want 1", got)
	}
}

func TestReplaceBookRejectsInvalidUploadUnchanged(t *testing.T) {
	t.Parallel()
	pd, before := replaceTestProfile(t)
	original := readBookEditFile(t, before.FilePath)

	for name, data := range map[string][]byte{
		"not a zip":   []byte("plain text"),
		"empty spine": replaceTestEPUB(t, "Enriched"),
	} {
		w := httptest.NewRecorder()
		replaceBookHandler(nil)(w, replaceRequest(t, pd, data))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, w.Code)
		}
	}
	if !bytes.Equal(readBookEditFile(t, before.FilePath), original) {
		t.Fatal("rejected replacement changed the installed file")
	}
	after, err := pd.DB.GetBookContext(t.Context(), enrichBookID)
	if err != nil {
		t.Fatal(err)
	}
	if after.BookSummary != before.BookSummary {
		t.Fatal("rejected replacement changed the book row")
	}
	entries, err := os.ReadDir(pd.LibPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sayumi-upload-") {
			t.Fatalf("staging file left behind: %s", filepath.Join(pd.LibPath, e.Name()))
		}
	}
}

func TestReplaceBookUnknownBook(t *testing.T) {
	t.Parallel()
	pd, _ := replaceTestProfile(t)
	r := replaceRequest(t, pd, replaceTestEPUB(t, "x", "c1.xhtml"))
	r.SetPathValue("id", "missing")
	w := httptest.NewRecorder()
	replaceBookHandler(nil)(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestBuildChapterRemap(t *testing.T) {
	t.Parallel()
	spine := func(hrefs ...string) []epub.SpineEntry {
		out := make([]epub.SpineEntry, len(hrefs))
		for i, h := range hrefs {
			out[i] = epub.SpineEntry{Href: h}
		}
		return out
	}

	t.Run("append only keeps indices and anchors", func(t *testing.T) {
		t.Parallel()
		m := buildChapterRemap(spine("a", "b"), spine("a", "b", "c"))
		want := []storage.ChapterMove{{Index: 0, KeepAnchor: true}, {Index: 1, KeepAnchor: true}}
		if m.NewCount != 3 || fmt.Sprint(m.Moves) != fmt.Sprint(want) {
			t.Fatalf("remap = %+v", m)
		}
	})
	t.Run("removed chapter has no counterpart", func(t *testing.T) {
		t.Parallel()
		m := buildChapterRemap(spine("a", "b", "c"), spine("a", "c"))
		if m.Moves[1].Index != -1 || m.Moves[2] != (storage.ChapterMove{Index: 1, KeepAnchor: true}) {
			t.Fatalf("remap = %+v", m)
		}
	})
	t.Run("fully renamed spine falls back to index", func(t *testing.T) {
		t.Parallel()
		m := buildChapterRemap(spine("a", "b"), spine("x", "y", "z"))
		for i, move := range m.Moves {
			if move.Index != -1 {
				t.Fatalf("move %d = %+v, want the index fallback", i, move)
			}
		}
	})
}
