package epub

import (
	"archive/zip"
	"strings"
	"testing"
)

func TestResolvePathAndReference(t *testing.T) {
	t.Parallel()

	if got := resolvePath("OEBPS", "ch1.xhtml"); got != "OEBPS/ch1.xhtml" {
		t.Fatalf("resolvePath = %q, want OEBPS/ch1.xhtml", got)
	}
	if got := resolvePath("OEBPS/Text", "../Images/a.png"); got != "OEBPS/Images/a.png" {
		t.Fatalf("resolvePath parent = %q", got)
	}
	// path.Clean must not leave ".." segments that climb out of the zip root.
	if got := resolvePath("OEBPS", "../../evil"); got != "evil" {
		t.Fatalf("resolvePath escape clean = %q, want evil", got)
	}
	if got := resolvePath("OEBPS", ""); got != "" {
		t.Fatalf("empty href = %q", got)
	}

	if got := resolveReference("OEBPS/nav.xhtml", "ch1.xhtml#sec2"); got != "OEBPS/ch1.xhtml#sec2" {
		t.Fatalf("resolveReference frag = %q", got)
	}
	if got := resolveReference("OEBPS/nav.xhtml", "#only"); got != "OEBPS/nav.xhtml#only" {
		t.Fatalf("resolveReference same-doc = %q", got)
	}
	if got := resolveReference("OEBPS/nav.xhtml", "ch1.xhtml?x=1#y"); got != "OEBPS/ch1.xhtml?x=1#y" {
		t.Fatalf("resolveReference query+frag = %q", got)
	}
}

func TestHasTokenAndLooksLikeISBN(t *testing.T) {
	t.Parallel()

	if !hasToken("cover-image foo", "cover-image") {
		t.Fatal("expected cover-image token")
	}
	if hasToken("cover", "cover-image") {
		t.Fatal("substring must not match token")
	}
	if !looksLikeISBN("978-0-306-40615-7") {
		t.Fatal("ISBN-13 with dashes")
	}
	if !looksLikeISBN("0306406152") {
		t.Fatal("ISBN-10")
	}
	if !looksLikeISBN("030640615X") {
		t.Fatal("ISBN-10 with X")
	}
	if looksLikeISBN("not-an-isbn") {
		t.Fatal("garbage accepted as ISBN")
	}
}

func TestParseNavHTMLDirectAndWrappedOL(t *testing.T) {
	t.Parallel()

	// Build HTML via concatenation so tool input never contains adjacent brace pairs.
	direct := strings.Join([]string{
		"<html><body>",
		"<nav epub:type=\"toc\"><ol>",
		"<li><a href=\"ch1.xhtml\">One</a></li>",
		"<li><a href=\"ch2.xhtml\">Two</a>",
		"<ol><li><a href=\"ch2.xhtml#s\">Two.a</a></li></ol>",
		"</li>",
		"</ol></nav>",
		"</body></html>",
	}, "")

	got := parseNavHTML([]byte(direct), "OEBPS/nav.xhtml")
	if len(got) != 2 {
		t.Fatalf("direct ol entries = %d, want 2: %+v", len(got), got)
	}
	if got[0].Title != "One" || got[0].Href != "OEBPS/ch1.xhtml" {
		t.Fatalf("entry0 = %+v", got[0])
	}
	if got[1].Title != "Two" || len(got[1].Children) != 1 || got[1].Children[0].Title != "Two.a" {
		t.Fatalf("entry1 = %+v", got[1])
	}

	// Wrapped <ol> (the bug fix): nav > div > ol must still parse.
	wrapped := strings.Join([]string{
		"<html><body>",
		"<nav role=\"doc-toc\"><div class=\"toc\"><h1>Contents</h1><ol>",
		"<li><a href=\"a.xhtml\">Alpha</a></li>",
		"</ol></div></nav>",
		"</body></html>",
	}, "")

	got = parseNavHTML([]byte(wrapped), "OEBPS/nav.xhtml")
	if len(got) != 1 || got[0].Title != "Alpha" || got[0].Href != "OEBPS/a.xhtml" {
		t.Fatalf("wrapped ol = %+v, want Alpha -> OEBPS/a.xhtml", got)
	}
}

func TestParseNCXDataNested(t *testing.T) {
	t.Parallel()

	ncx := strings.Join([]string{
		"<?xml version=\"1.0\"?>",
		"<ncx><navMap>",
		"<navPoint><navLabel><text>Root</text></navLabel><content src=\"c1.xhtml\"/>",
		"<navPoint><navLabel><text>Child</text></navLabel><content src=\"c1.xhtml#x\"/></navPoint>",
		"</navPoint>",
		"<navPoint><navLabel><text></text></navLabel><content src=\"skip.xhtml\"/></navPoint>",
		"</navMap></ncx>",
	}, "")

	got := parseNCXData([]byte(ncx), "OEBPS/toc.ncx")
	if len(got) != 1 {
		t.Fatalf("ncx roots = %d, want 1 (empty title skipped): %+v", len(got), got)
	}
	if got[0].Title != "Root" || got[0].Href != "OEBPS/c1.xhtml" {
		t.Fatalf("root = %+v", got[0])
	}
	if len(got[0].Children) != 1 || got[0].Children[0].Title != "Child" {
		t.Fatalf("children = %+v", got[0].Children)
	}
	if !strings.HasSuffix(got[0].Children[0].Href, "#x") {
		t.Fatalf("child href = %q, want fragment", got[0].Children[0].Href)
	}
}

func TestFindOPFPathPrefersPackageMediaType(t *testing.T) {
	t.Parallel()

	container := strings.Join([]string{
		"<?xml version=\"1.0\"?>",
		"<container><rootfiles>",
		"<rootfile full-path=\"OEBPS/other.xml\" media-type=\"text/xml\"/>",
		"<rootfile full-path=\"OEBPS/content.opf\" media-type=\"application/oebps-package+xml\"/>",
		"</rootfiles></container>",
	}, "")
	files := map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      minimalOPF("Title A", "Author A", "chap.xhtml"),
		"OEBPS/chap.xhtml":       "<html><body><p>hi</p></body></html>",
	}
	index := testZipIndex(t, files)
	got, err := findOPFPath(index)
	if err != nil {
		t.Fatalf("findOPFPath: %v", err)
	}
	if got != "OEBPS/content.opf" {
		t.Fatalf("opf path = %q, want OEBPS/content.opf", got)
	}
}

func TestParseMinimalEPUB(t *testing.T) {
	t.Parallel()

	container := strings.Join([]string{
		"<?xml version=\"1.0\"?>",
		"<container version=\"1.0\" xmlns=\"urn:oasis:names:tc:opendocument:xmlns:container\">",
		"<rootfiles><rootfile full-path=\"OEBPS/package.opf\" media-type=\"application/oebps-package+xml\"/>",
		"</rootfiles></container>",
	}, "")
	nav := strings.Join([]string{
		"<html xmlns:epub=\"http://www.idpf.org/2007/ops\"><body>",
		"<nav epub:type=\"toc\"><div><ol>",
		"<li><a href=\"Text/ch1.xhtml\">Chapter One</a></li>",
		"</ol></div></nav></body></html>",
	}, "")
	opf := strings.Join([]string{
		"<?xml version=\"1.0\"?>",
		"<package dir=\"ltr\">",
		"<metadata>",
		"<title>Hello</title><creator>Ada</creator><language>en</language>",
		"<meta name=\"cover\" content=\"cover-img\"/>",
		"</metadata>",
		"<manifest>",
		"<item id=\"c1\" href=\"Text/ch1.xhtml\" media-type=\"application/xhtml+xml\"/>",
		"<item id=\"nav\" href=\"nav.xhtml\" media-type=\"application/xhtml+xml\" properties=\"nav\"/>",
		"<item id=\"cover-img\" href=\"Images/cover.jpg\" media-type=\"image/jpeg\" properties=\"cover-image\"/>",
		"</manifest>",
		"<spine toc=\"\" page-progression-direction=\"ltr\">",
		"<itemref idref=\"c1\"/>",
		"</spine></package>",
	}, "")

	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": container,
		"OEBPS/package.opf":      opf,
		"OEBPS/Text/ch1.xhtml":   "<html><body><p>Chapter</p></body></html>",
		"OEBPS/nav.xhtml":        nav,
		"OEBPS/Images/cover.jpg": "fake-jpeg-bytes",
	}

	path := writeTestEPUB(t, files)
	rc, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = rc.Close() }()

	meta, err := Parse(&rc.Reader)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if meta.Title != "Hello" || meta.Author != "Ada" {
		t.Fatalf("meta title/author = %q / %q", meta.Title, meta.Author)
	}
	if meta.Direction != "ltr" {
		t.Fatalf("direction = %q", meta.Direction)
	}
	if len(meta.Spine) != 1 || meta.Spine[0].Href != "OEBPS/Text/ch1.xhtml" {
		t.Fatalf("spine = %+v", meta.Spine)
	}
	if meta.CoverPath != "OEBPS/Images/cover.jpg" {
		t.Fatalf("cover = %q", meta.CoverPath)
	}
	if len(meta.TOC) != 1 || meta.TOC[0].Title != "Chapter One" {
		t.Fatalf("toc = %+v (wrapped nav ol must work end-to-end)", meta.TOC)
	}
}

func minimalOPF(title, author, chapterHref string) string {
	return strings.Join([]string{
		"<?xml version=\"1.0\"?>",
		"<package>",
		"<metadata><title>" + title + "</title><creator>" + author + "</creator></metadata>",
		"<manifest>",
		"<item id=\"c1\" href=\"" + chapterHref + "\" media-type=\"application/xhtml+xml\"/>",
		"</manifest>",
		"<spine><itemref idref=\"c1\"/></spine>",
		"</package>",
	}, "")
}

func testZipIndex(t *testing.T, files map[string]string) map[string]*zip.File {
	t.Helper()
	path := writeTestEPUB(t, files)
	rc, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return buildIndex(&rc.Reader)
}
