package epub

import (
	"archive/zip"
	"bytes"
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
	if !strings.Contains(opf, "<dc:creator>") || !strings.Contains(opf, "New Author") {
		t.Fatalf("creator not inserted: %s", opf)
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
	if strings.Contains(opf2, "<dc:creator>") {
		t.Fatalf("empty author should not insert creator: %s", opf2)
	}
	if !strings.Contains(opf2, "Solo2") {
		t.Fatalf("title not set: %s", opf2)
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
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte("application/epub+zip"))
	w2, _ := zw.Create("META-INF/container.xml")
	_, _ = w2.Write([]byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="missing.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	_ = zw.Close()
	_ = f.Close()

	title := "x"
	tmp, err := RewriteBook(path, MetadataEdit{Title: &title})
	if err == nil {
		t.Fatal("want error for missing OPF")
	}
	if tmp != "" {
		t.Fatalf("tmpPath should be empty on failure, got %q", tmp)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp: %s", e.Name())
		}
	}
}
