package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalJPEG is a tiny valid JPEG (1x1) so cover bytes are non-empty.
var minimalJPEG = []byte{
	0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01,
	0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xff, 0xdb, 0x00, 0x43,
	0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09,
	0x09, 0x08, 0x0a, 0x0c, 0x14, 0x0d, 0x0c, 0x0b, 0x0b, 0x0c, 0x19, 0x12,
	0x13, 0x0f, 0x14, 0x1d, 0x1a, 0x1f, 0x1e, 0x1d, 0x1a, 0x1c, 0x1c, 0x20,
	0x24, 0x2e, 0x27, 0x20, 0x22, 0x2c, 0x23, 0x1c, 0x1c, 0x28, 0x37, 0x29,
	0x2c, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1f, 0x27, 0x39, 0x3d, 0x38, 0x32,
	0x3c, 0x2e, 0x33, 0x34, 0x32, 0xff, 0xc0, 0x00, 0x0b, 0x08, 0x00, 0x01,
	0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xff, 0xc4, 0x00, 0x14, 0x00, 0x01,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x03, 0xff, 0xc4, 0x00, 0x14, 0x10, 0x01, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3f, 0x00,
	0x7f, 0xff, 0xd9,
}

func writeMinimalEPUB(t *testing.T, opf string, extra map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "book.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()
	zw := zip.NewWriter(f)

	// mimetype must be first and stored uncompressed for EPUB validity checks.
	mh := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	w, err := zw.CreateHeader(mh)
	if err != nil {
		t.Fatalf("mimetype hdr: %v", err)
	}
	if _, err := w.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("mimetype: %v", err)
	}

	write := func(name string, data []byte) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))
	write("OEBPS/content.opf", []byte(opf))
	write("OEBPS/ch.xhtml", []byte(`<?xml version="1.0"?><html><body><p>hi</p></body></html>`))
	for name, data := range extra {
		write(name, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

// mustReadEPUBEntry reads a named entry from an on-disk EPUB (test helper).
// Named to avoid colliding with package-level readZipEntry(*zip.File).
func mustReadEPUBEntry(t *testing.T, epubPath, name string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		if cerr := zr.Close(); cerr != nil {
			t.Errorf("close zip: %v", cerr)
		}
	}()
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, "/") == name {
			data, err := readZipEntry(f)
			if err != nil {
				t.Fatalf("read entry %q: %v", name, err)
			}
			return data
		}
	}
	t.Fatalf("missing entry %q", name)
	return nil
}

func zipEntryNames(t *testing.T, epubPath string) []string {
	t.Helper()
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		if cerr := zr.Close(); cerr != nil {
			t.Errorf("close zip: %v", cerr)
		}
	}()
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, strings.TrimPrefix(f.Name, "/"))
	}
	return names
}

const opfWithMeta = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:test:1</dc:identifier>
    <dc:title>Old Title</dc:title>
    <dc:creator>Old Author</dc:creator>
    <dc:language>en</dc:language>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="cover.png" media-type="image/png"/>
  </manifest>
  <spine>
    <itemref idref="ch"/>
  </spine>
</package>`

const opfNoCoverNoCreator = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:test:2</dc:identifier>
    <dc:title>Solo</dc:title>
    <dc:language>en</dc:language>
  </metadata>
  <manifest>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch"/>
  </spine>
</package>`

func TestRewriteBookEmptyEdit(t *testing.T) {
	t.Parallel()
	if _, err := RewriteBook("nope.epub", MetadataEdit{}); err == nil {
		t.Fatal("empty edit: want error")
	}
}

func TestRewriteBookTitleAuthorAndCoverReplace(t *testing.T) {
	t.Parallel()

	src := writeMinimalEPUB(t, opfWithMeta, map[string][]byte{
		"OEBPS/cover.png": []byte("PNGOLD"),
	})
	title := `New & "Fancy" <Title>`
	author := "Ada O'Connor"
	tmp, err := RewriteBook(src, MetadataEdit{
		Title:     &title,
		Author:    &author,
		CoverJPEG: minimalJPEG,
	})
	if err != nil {
		t.Fatalf("RewriteBook: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })

	if filepath.Dir(tmp) != filepath.Dir(src) {
		t.Fatalf("temp dir %q != src dir %q", filepath.Dir(tmp), filepath.Dir(src))
	}
	if !strings.HasPrefix(filepath.Base(tmp), ".") {
		t.Fatalf("temp base should be dot-prefixed: %q", filepath.Base(tmp))
	}

	// mimetype first + stored.
	names := zipEntryNames(t, tmp)
	if len(names) == 0 || names[0] != "mimetype" {
		t.Fatalf("first entry = %v, want mimetype first", names)
	}
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		t.Fatalf("open rewritten: %v", err)
	}
	defer func() {
		if cerr := zr.Close(); cerr != nil {
			t.Errorf("close rewritten zip: %v", cerr)
		}
	}()
	if zr.File[0].Method != zip.Store {
		t.Fatalf("mimetype method = %d, want Store", zr.File[0].Method)
	}

	opf := string(mustReadEPUBEntry(t, tmp, "OEBPS/content.opf"))
	if !strings.Contains(opf, "New &amp; &quot;Fancy&quot; &lt;Title&gt;") &&
		!strings.Contains(opf, "New &amp; &#34;Fancy&#34; &lt;Title&gt;") {
		if !strings.Contains(opf, "Fancy") || !strings.Contains(opf, "&amp;") || !strings.Contains(opf, "&lt;Title&gt;") {
			t.Fatalf("title not escaped/replaced: %s", opf)
		}
	}
	if strings.Contains(opf, "Old Title") {
		t.Fatalf("old title remains: %s", opf)
	}
	if !strings.Contains(opf, "Ada O&#39;Connor") && !strings.Contains(opf, "Ada O&apos;Connor") && !strings.Contains(opf, "Ada O'Connor") {
		if !strings.Contains(opf, "Ada O") {
			t.Fatalf("author missing: %s", opf)
		}
	}
	if strings.Contains(opf, "Old Author") {
		t.Fatalf("old author remains: %s", opf)
	}
	// media-type should flip png → jpeg for the cover item.
	if !strings.Contains(opf, `media-type="image/jpeg"`) {
		t.Fatalf("cover media-type not jpeg: %s", opf)
	}

	cover := mustReadEPUBEntry(t, tmp, "OEBPS/cover.png")
	if !bytes.Equal(cover, minimalJPEG) {
		t.Fatalf("cover bytes not replaced (len=%d)", len(cover))
	}
}

func TestRewriteBookInsertAuthorAndNewCover(t *testing.T) {
	t.Parallel()

	src := writeMinimalEPUB(t, opfNoCoverNoCreator, nil)
	author := "New Author"
	tmp, err := RewriteBook(src, MetadataEdit{
		Author:    &author,
		CoverJPEG: minimalJPEG,
	})
	if err != nil {
		t.Fatalf("RewriteBook: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })

	opf := string(mustReadEPUBEntry(t, tmp, "OEBPS/content.opf"))
	meta, _, err := parseOPF([]byte(opf), "OEBPS")
	if err != nil || meta.Author != author {
		t.Fatalf("inserted creator did not round-trip: %q, %v", meta.Author, err)
	}
	if !strings.Contains(opf, `name="cover"`) {
		t.Fatalf("cover meta missing: %s", opf)
	}
	if !strings.Contains(opf, "sayumi-cover") {
		t.Fatalf("cover item missing: %s", opf)
	}

	// New cover entry present with JPEG bytes.
	found := false
	for _, name := range zipEntryNames(t, tmp) {
		if strings.Contains(name, "sayumi-cover") && strings.HasSuffix(name, ".jpg") {
			if !bytes.Equal(mustReadEPUBEntry(t, tmp, name), minimalJPEG) {
				t.Fatalf("new cover bytes mismatch")
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("new cover zip entry missing: %v", zipEntryNames(t, tmp))
	}

	// Empty author with no creator: no-op path (still need a non-empty edit).
	empty := ""
	title := "Solo2"
	tmp2, err := RewriteBook(src, MetadataEdit{Title: &title, Author: &empty})
	if err != nil {
		t.Fatalf("empty author: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp2) })
	opf2 := string(mustReadEPUBEntry(t, tmp2, "OEBPS/content.opf"))
	meta2, pkg2, err := parseOPF([]byte(opf2), "OEBPS")
	if err != nil || meta2.Title != title || meta2.Author != "" || len(pkg2.Metadata.Creators) != 0 {
		t.Fatalf("empty-author edit did not round-trip: %+v, %v", meta2, err)
	}
}

func TestApplySplicesAndReplaceAttr(t *testing.T) {
	t.Parallel()

	src := []byte("aaaBBBccc")
	got := applySplices(src, []spliceOp{
		{3, 6, []byte("XXX")},
		{6, 6, []byte("-")},
	})
	if string(got) != "aaaXXX-ccc" {
		t.Fatalf("applySplices = %q", got)
	}

	tag := []byte(`<item id="c" href="cover.png" media-type="image/png"/>`)
	fixed, ok := replaceAttrValue(tag, "media-type", "image/jpeg")
	if !ok {
		t.Fatal("replaceAttrValue want ok")
	}
	if !bytes.Contains(fixed, []byte(`media-type="image/jpeg"`)) {
		t.Fatalf("fixed = %s", fixed)
	}
	if !bytes.Contains(fixed, []byte(`href="cover.png"`)) {
		t.Fatalf("other attrs lost: %s", fixed)
	}
}

func TestRewriteBookCleansTempOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.epub")
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="missing.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
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
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	title := "x"
	tmp, err := RewriteBook(path, MetadataEdit{Title: &title})
	if err == nil {
		t.Fatal("want error for missing OPF")
	}
	if tmp != "" {
		t.Fatalf("tmpPath should be empty on failure, got %q", tmp)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp: %s", e.Name())
		}
	}
}

// parseOPF prefers a creator's file-as attribute over its text. Author edits
// must update that sort key too, without discarding unrelated creator metadata.
func TestRewriteBookUpdatesCreatorFileAs(t *testing.T) {
	t.Parallel()

	const opf = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Old Title</dc:title>
    <dc:creator opf:file-as="Author, Old" opf:role="aut">Old Author</dc:creator>
  </metadata>
  <manifest>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch"/></spine>
</package>`

	src := writeMinimalEPUB(t, opf, nil)
	newAuthor := "New Author"
	tmp, err := RewriteBook(src, MetadataEdit{Author: &newAuthor})
	if err != nil {
		t.Fatalf("RewriteBook: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })

	got := string(mustReadEPUBEntry(t, tmp, "OEBPS/content.opf"))
	if strings.Contains(got, "Author, Old") {
		t.Errorf("stale opf:file-as survived the author edit:\n%s", got)
	}
	if !strings.Contains(got, "New Author") {
		t.Errorf("new author not written:\n%s", got)
	}
	// Unrelated attributes on the same tag must be preserved.
	if !strings.Contains(got, `opf:role="aut"`) {
		t.Errorf("opf:role lost from the creator tag:\n%s", got)
	}

	// The authoritative check: this package must read back what it wrote.
	meta, _, err := parseOPF([]byte(got), "OEBPS")
	if err != nil {
		t.Fatalf("re-parse rewritten OPF: %v", err)
	}
	if meta.Author != newAuthor {
		t.Errorf("re-parsed author = %q, want %q", meta.Author, newAuthor)
	}
}

// A missing declared cover must become readable at the path selected by the
// parser. Checking only for a new .jpg file can miss a stale earlier reference.
func TestRewriteBookAddsCoverWhenDeclaredEntryIsMissing(t *testing.T) {
	t.Parallel()

	const opf = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Ghost Cover</dc:title>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="cover-img" href="cover.jpeg" media-type="image/jpeg"/>
    <item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch"/></spine>
</package>`

	// Note: no OEBPS/cover.jpeg entry is written.
	src := writeMinimalEPUB(t, opf, nil)
	tmp, err := RewriteBook(src, MetadataEdit{CoverJPEG: minimalJPEG})
	if err != nil {
		t.Fatalf("RewriteBook with a declared-but-missing cover: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmp) })

	rewritten := mustReadEPUBEntry(t, tmp, "OEBPS/content.opf")
	meta, _, err := parseOPF(rewritten, "OEBPS")
	if err != nil || meta.CoverPath != "OEBPS/cover.jpeg" {
		t.Fatalf("declared cover did not round-trip: %q, %v", meta.CoverPath, err)
	}
	coverEntry := meta.CoverPath
	if got := mustReadEPUBEntry(t, tmp, coverEntry); !bytes.Equal(got, minimalJPEG) {
		t.Errorf("cover entry %q does not hold the supplied JPEG", coverEntry)
	}
}

// RawToken emits a synthetic EndElement for a self-closed <metadata/> at the
// offset AFTER the whole element, so "insert just before the end tag" landed
// outside it: the new <dc:title> became a sibling of <metadata> under
// <package>, producing an invalid OPF that this package reads back as empty.
func TestRewriteBookRejectsSelfClosedMetadataRatherThanCorruptingIt(t *testing.T) {
	t.Parallel()

	const opf = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata/>
  <manifest><item id="ch" href="ch.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="ch"/></spine>
</package>`

	src := writeMinimalEPUB(t, opf, nil)
	newTitle := "Brand New"
	tmp, err := RewriteBook(src, MetadataEdit{Title: &newTitle})
	if err == nil {
		t.Cleanup(func() { _ = os.Remove(tmp) })
		// If a future change makes this succeed, the result must at least be a
		// document whose title actually round-trips.
		got := mustReadEPUBEntry(t, tmp, "OEBPS/content.opf")
		meta, _, perr := parseOPF(got, "OEBPS")
		if perr != nil || meta.Title != newTitle {
			t.Fatalf("edit produced an OPF that does not round-trip: title=%q err=%v\n%s",
				meta.Title, perr, got)
		}
		return
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("expected a metadata-related refusal, got: %v", err)
	}
}

// A missing OPF fails before CreateTemp; corrupting a later local header reaches
// the cleanup path after the output exists, without a timing-dependent I/O fault.
func TestRewriteBookFailedCopyRemovesTemp(t *testing.T) {
	t.Parallel()
	src := writeMinimalEPUB(t, opfWithMeta, nil)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	nameAt := bytes.Index(data, []byte("OEBPS/ch.xhtml"))
	if nameAt < 30 || !bytes.Equal(data[nameAt-30:nameAt-26], []byte{'P', 'K', 3, 4}) {
		t.Fatal("fixture has no expected chapter local header")
	}
	data[nameAt-30] = 0
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Replacement"
	tmp, err := RewriteBook(src, MetadataEdit{Title: &title})
	if err == nil || !strings.Contains(err.Error(), "copy entry") {
		t.Fatalf("rewrite = (%q, %v); want copy failure", tmp, err)
	}
	if tmp != "" {
		t.Errorf("failure returned temp path %q", tmp)
	}
	entries, err := os.ReadDir(filepath.Dir(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(src) {
		t.Errorf("failure left files beside source: %v", entries)
	}
	got, err := os.ReadFile(src)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("failure modified source: %v", err)
	}
}

func TestRewriteBookPreservesUnselectedEntries(t *testing.T) {
	t.Parallel()
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate_exact_name=%t", duplicate), func(t *testing.T) {
			extra := map[string][]byte{
				"OEBPS/CONTENT.OPF": []byte("unselected package"),
				"OEBPS/cover.png":   []byte("selected cover"),
				"OEBPS/COVER.PNG":   []byte("unselected image"),
			}
			if duplicate {
				extra["OEBPS/content.opf"] = []byte(opfWithMeta)
			}
			src := writeMinimalEPUB(t, opfWithMeta, extra)
			before, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			r, err := zip.OpenReader(src)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := r.Close(); err != nil {
					t.Error(err)
				}
			})
			index := buildIndex(&r.Reader)
			title := "Changed"
			tmp, err := RewriteBook(src, MetadataEdit{Title: &title, CoverJPEG: minimalJPEG})
			if err != nil {
				t.Fatal(err)
			}
			out, err := zip.OpenReader(tmp)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := out.Close(); err != nil {
					t.Error(err)
				}
			})
			if len(r.File) != len(out.File) {
				t.Fatalf("entry count = %d; want %d", len(out.File), len(r.File))
			}
			for i, f := range r.File {
				g := out.File[i]
				if g.Name != f.Name {
					t.Fatalf("entry order changed at %d: %q -> %q", i, f.Name, g.Name)
				}
				if f == index["OEBPS/content.opf"] || f == index["OEBPS/cover.png"] {
					continue
				}
				rawBefore, err := f.OpenRaw()
				if err != nil {
					t.Fatal(err)
				}
				rawAfter, err := g.OpenRaw()
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(rawBefore)
				if err != nil {
					t.Fatal(err)
				}
				a, err := io.ReadAll(rawAfter)
				if err != nil {
					t.Fatal(err)
				}
				if f.Method != g.Method || !bytes.Equal(a, b) {
					t.Errorf("unselected entry %d (%q) changed", i, f.Name)
				}
			}
			after, err := os.ReadFile(src)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatalf("successful rewrite modified source: %v", err)
			}
		})
	}
}

func TestRewriteBookRejectsOversizeOPFBeforeOpening(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	container, err := zw.Create("META-INF/container.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(container, `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`); err != nil {
		t.Fatal(err)
	}
	// Unsupported compression proves the size guard runs before opening a body.
	if _, err := zw.CreateRaw(&zip.FileHeader{Name: "content.opf", Method: 99, UncompressedSize64: maxOPFBytes + 1}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "large.epub")
	if err := os.WriteFile(src, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "New"
	tmp, err := RewriteBook(src, MetadataEdit{Title: &title})
	if tmp != "" || err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("rewrite = (%q, %v); want size refusal before compression error", tmp, err)
	}
}

func TestRewriteOPFMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	const title = `New & <Title>`
	for _, tt := range []struct {
		name, before, metadata, author, preserved string
	}{
		{"nested_fields", "", `<meta><title>nested title</title><creator>nested author</creator></meta><dc:title>Old</dc:title><dc:creator>Old</dc:creator>`, "New Author", `<title>nested title</title>`},
		{"outside_metadata", `<guide><title>guide title</title><creator>guide author</creator></guide>`, `<dc:title>Old</dc:title><dc:creator>Old</dc:creator>`, "New Author", `<title>guide title</title>`},
		{"self_closed_creator", "", `<dc:title/><dc:title>Alternate</dc:title><dc:creator opf:file-as="Old sort"/><dc:creator>Other</dc:creator>`, "New Author", `<dc:creator>Other</dc:creator>`},
		{"clear_authors", "", `<dc:title>Old</dc:title><dc:creator opf:file-as="Old sort">Old</dc:creator><dc:creator>Other</dc:creator>`, "", `<dc:title>New &amp; &lt;Title&gt;</dc:title>`},
		{"attribute_case", "", `<dc:title>Old</dc:title><dc:creator FILE-AS="extension" opf:file-as="Old sort">Old</dc:creator>`, "New Author", `FILE-AS="extension"`},
		{"attribute_local_collision", "", `<dc:title>Old</dc:title><dc:creator xmlns:x="urn:extension" x:file-as="extension" opf:file-as="Old sort">Old</dc:creator>`, "New Author", `x:file-as="extension"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(`<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` + tt.before + `<metadata>` + tt.metadata + `</metadata><manifest></manifest><spine/></package>`)
			newTitle := title
			got, _, _, err := rewriteOPF(data, "", MetadataEdit{Title: &newTitle, Author: &tt.author}, nil)
			if err != nil {
				t.Fatal(err)
			}
			meta, _, err := parseOPF(got, "")
			if err != nil || meta.Title != title || meta.Author != tt.author {
				t.Errorf("readback = title %q, author %q, err %v; want %q, %q", meta.Title, meta.Author, err, title, tt.author)
			}
			if !bytes.Contains(got, []byte(tt.preserved)) {
				t.Errorf("lost unrelated content %q in %s", tt.preserved, got)
			}
		})
	}
}

func TestRewriteOPFInsertedDublinCoreNamespace(t *testing.T) {
	t.Parallel()
	for _, binding := range []string{"", ` xmlns:dc="urn:unrelated"`} {
		t.Run(fmt.Sprintf("binding=%q", binding), func(t *testing.T) {
			data := []byte(`<package xmlns="http://www.idpf.org/2007/opf"` + binding + `><metadata></metadata><manifest></manifest></package>`)
			title, author := "New title", "New author"
			got, _, _, err := rewriteOPF(data, "", MetadataEdit{Title: &title, Author: &author}, nil)
			if err != nil {
				t.Fatal(err)
			}
			dec := xml.NewDecoder(bytes.NewReader(got))
			found := 0
			for {
				token, err := dec.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if start, ok := token.(xml.StartElement); ok && (start.Name.Local == "title" || start.Name.Local == "creator") {
					found++
					if start.Name.Space != "http://purl.org/dc/elements/1.1/" {
						t.Errorf("inserted %s has namespace %q", start.Name.Local, start.Name.Space)
					}
				}
			}
			if found != 2 {
				t.Fatalf("inserted fields = %d; want 2", found)
			}
		})
	}
}

func TestRewriteBookCoverReadback(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, opf string
		extra     map[string][]byte
	}{
		{"missing_declared_entry", opfWithMeta, nil},
		{"missing_media_type", strings.Replace(opfWithMeta, `media-type="image/png"`, `properties="cover-image"`, 1), map[string][]byte{"OEBPS/cover.png": []byte("old")}},
		{"nested_item", strings.Replace(opfWithMeta, "<manifest>", `<guide><item href="cover.png" media-type="image/png"/></guide><manifest>`, 1), map[string][]byte{"OEBPS/cover.png": []byte("old")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := writeMinimalEPUB(t, tt.opf, tt.extra)
			tmp, err := RewriteBook(src, MetadataEdit{CoverJPEG: minimalJPEG})
			if err != nil {
				t.Fatal(err)
			}
			opf := mustReadEPUBEntry(t, tmp, "OEBPS/content.opf")
			meta, pkg, err := parseOPF(opf, "OEBPS")
			if err != nil {
				t.Fatal(err)
			}
			if got := mustReadEPUBEntry(t, tmp, meta.CoverPath); !bytes.Equal(got, minimalJPEG) {
				t.Fatalf("parser-selected cover %q has wrong bytes", meta.CoverPath)
			}
			for _, item := range pkg.Manifest.Items {
				if resolvePath("OEBPS", item.Href) == meta.CoverPath && item.MediaType != "image/jpeg" {
					t.Errorf("cover media type = %q; want image/jpeg", item.MediaType)
				}
			}
			if tt.name == "nested_item" && !bytes.Contains(opf, []byte(`<guide><item href="cover.png" media-type="image/png"/></guide>`)) {
				t.Error("unrelated guide item changed")
			}
		})
	}
}

func TestRewriteOPFRejectsTrailingStructure(t *testing.T) {
	t.Parallel()
	title := "New"
	for _, tail := range []string{"<package><metadata></metadata></package>", "<unfinished>", "trailing text"} {
		t.Run(tail, func(t *testing.T) {
			data := []byte(opfWithMeta + tail)
			if _, _, _, err := rewriteOPF(data, "OEBPS", MetadataEdit{Title: &title}, nil); err == nil {
				t.Fatal("accepted malformed content after the package")
			}
		})
	}
}

func TestRewriteOPFInsertedCoverNamespace(t *testing.T) {
	t.Parallel()
	data := []byte(`<opf:package xmlns:opf="http://www.idpf.org/2007/opf"><opf:metadata></opf:metadata><opf:manifest></opf:manifest></opf:package>`)
	got, cover, isNew, err := rewriteOPF(data, "OPS", MetadataEdit{CoverJPEG: minimalJPEG}, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta, _, err := parseOPF(got, "OPS")
	if err != nil || !isNew || cover == "" || meta.CoverPath != cover {
		t.Fatalf("cover did not round-trip: %q, %t, %+v, %v", cover, isNew, meta, err)
	}
	dec := xml.NewDecoder(bytes.NewReader(got))
	found := 0
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := token.(xml.StartElement); ok && (start.Name.Local == "meta" || start.Name.Local == "item") {
			found++
			if start.Name.Space != "http://www.idpf.org/2007/opf" {
				t.Errorf("inserted %s has namespace %q", start.Name.Local, start.Name.Space)
			}
		}
	}
	if found != 2 {
		t.Fatalf("inserted nodes = %d; want 2", found)
	}
}

// Exercise raw offsets with malformed XML, namespace prefixes, self-closing
// fields, and extension nodes. A successful edit must be immutable, parseable,
// authoritative on readback, and byte-stable when applied a second time.
func FuzzRewriteOPFRoundTrip(f *testing.F) {
	f.Add(opfWithMeta)
	f.Add(opfNoCoverNoCreator)
	f.Add(`<package><metadata><title/><creator file-as="Old"/></metadata><manifest></manifest></package>`)
	f.Add(`<package><metadata><meta><title>extension</title></meta><title>Old</title></metadata><manifest></manifest></package>`)
	f.Add("\ufeff" + opfWithMeta)
	f.Add(opfWithMeta + "<unfinished>")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		data := []byte(raw)
		title, author := `Edited & <Title>`, `Edited "Author"`
		edit := MetadataEdit{Title: &title, Author: &author, CoverJPEG: minimalJPEG}
		got, cover, _, err := rewriteOPF(data, "OEBPS", edit, nil)
		if string(data) != raw {
			t.Fatal("rewrite mutated the input")
		}
		if err != nil {
			return
		}
		meta, _, err := parseOPF(got, "OEBPS")
		if err != nil || meta.Title != title || meta.Author != author || cover == "" || meta.CoverPath != cover {
			t.Fatalf("successful edit did not round-trip: %+v, %q, %v\n%s", meta, cover, err, got)
		}
		next, nextCover, _, err := rewriteOPF(got, "OEBPS", edit, nil)
		if err != nil || nextCover != cover || !bytes.Equal(next, got) {
			t.Fatalf("repeated edit changed the result: %v", err)
		}
	})
}
