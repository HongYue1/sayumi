package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MetadataEdit describes an in-place EPUB edit applied by RewriteBook. A nil
// Title/Author pointer leaves that Dublin Core field unchanged; a non-nil
// pointer sets it (the caller validates -- Title is expected non-empty, Author
// may be empty). When CoverJPEG is non-nil, the bytes (an already-normalized
// JPEG, the same the importer/upload path produces) replace the EPUB's existing
// cover image entry, or -- when the book declares no cover -- a new cover image
// entry plus manifest item and EPUB2 <meta name="cover"> reference are added.
type MetadataEdit struct {
	Title     *string
	Author    *string
	CoverJPEG []byte
}

func (e MetadataEdit) isEmpty() bool {
	return e.Title == nil && e.Author == nil && e.CoverJPEG == nil
}

// maxOPFBytes bounds the package document we are willing to rewrite in memory.
// An OPF is metadata, not content, so a few MB is already far past any genuine
// EPUB; the cap stops a crafted file from forcing an unbounded rewrite buffer.
const maxOPFBytes = 8 << 20

// RewriteBook builds a copy of the EPUB at srcPath with the requested metadata
// and/or cover edits applied to its OPF package document (and cover image
// entry), writing the result to a sibling temp file whose path it returns. It
// does NOT modify srcPath: the caller must close any open reader of srcPath and
// then atomically rename the temp file over it (and remove the temp file if it
// decides not to).
//
// Every untouched zip entry is copied verbatim with (*zip.Writer).Copy, which
// preserves its raw compressed bytes, compression method, and order -- so the
// mandatory uncompressed "mimetype"-first entry is retained byte-for-byte. Only
// the OPF (recompressed) and the cover image (stored) are rewritten. The OPF is
// edited by splicing byte ranges located with a raw XML token scan rather than
// by re-marshaling, because encoding/xml normalizes/reorders nodes and would
// corrupt many real-world package documents.
//
// The temp file is dot-prefixed so a concurrent library scan (which skips
// dotfiles) ignores the in-progress rewrite.
func RewriteBook(srcPath string, edit MetadataEdit) (tmpPath string, err error) {
	if edit.isEmpty() {
		return "", errors.New("epub rewrite: no edits requested")
	}

	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return "", fmt.Errorf("open epub: %w", err)
	}
	defer func() {
		if closeErr := zr.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close epub: %w", closeErr)
		}
	}()

	index := buildIndex(&zr.Reader)

	opfPath, err := findOPFPath(index)
	if err != nil {
		return "", fmt.Errorf("find OPF: %w", err)
	}
	// findOPFPath returns the raw container rootfile path, which may carry a
	// leading slash; normalize it to the same form buildIndex stores and the
	// rewrite loop compares against (zip entry names, leading slash trimmed).
	opfPath = strings.TrimPrefix(strings.TrimSpace(opfPath), "/")
	opfData, err := readZipFileIndexed(index, opfPath)
	if err != nil {
		return "", fmt.Errorf("read OPF: %w", err)
	}
	if len(opfData) > maxOPFBytes {
		return "", fmt.Errorf("OPF too large: %d bytes", len(opfData))
	}

	opfDir := pathDir(opfPath)
	newOPF, coverZipName, coverIsNew, err := rewriteOPF(opfData, opfDir, edit, index)
	if err != nil {
		return "", fmt.Errorf("rewrite OPF: %w", err)
	}

	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp epub: %w", err)
	}
	tmpPath = tmp.Name()
	committed := false
	defer func() {
		if committed {
			return
		}
		if closeErr := tmp.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close temp epub: %w", closeErr)
		}
		if rmErr := os.Remove(tmpPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Error("remove temp epub failed", "path", tmpPath, "err", rmErr)
		}
		tmpPath = ""
	}()

	zw := zip.NewWriter(tmp)

	wroteOPF := false
	wroteCover := false
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		switch {
		case strings.EqualFold(name, opfPath):
			if err := writeZipBytes(zw, f, zip.Deflate, newOPF); err != nil {
				return "", fmt.Errorf("write OPF entry: %w", err)
			}
			wroteOPF = true
		case coverZipName != "" && !coverIsNew && strings.EqualFold(name, coverZipName):
			if err := writeZipBytes(zw, f, zip.Store, edit.CoverJPEG); err != nil {
				return "", fmt.Errorf("write cover entry: %w", err)
			}
			wroteCover = true
		default:
			if err := zw.Copy(f); err != nil {
				return "", fmt.Errorf("copy entry %q: %w", f.Name, err)
			}
		}
	}

	if !wroteOPF {
		return "", fmt.Errorf("OPF entry %q not found while rewriting", opfPath)
	}

	if coverZipName != "" && coverIsNew {
		hdr := &zip.FileHeader{Name: coverZipName, Method: zip.Store}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return "", fmt.Errorf("create new cover entry: %w", err)
		}
		if _, err := w.Write(edit.CoverJPEG); err != nil {
			return "", fmt.Errorf("write new cover entry: %w", err)
		}
		wroteCover = true
	}

	if edit.CoverJPEG != nil && !wroteCover {
		return "", errors.New("cover entry was not written")
	}

	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("finalize epub: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync epub: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp epub: %w", err)
	}
	committed = true
	return tmpPath, nil
}

// writeZipBytes writes data as a fresh entry that reuses the original entry's
// name and modification time but the given compression method.
func writeZipBytes(zw *zip.Writer, src *zip.File, method uint16, data []byte) error {
	hdr := &zip.FileHeader{
		Name:     src.Name,
		Method:   method,
		Modified: src.Modified,
	}
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// pathDir returns the directory portion of a forward-slash zip path ("" when
// the path has no directory), mirroring how OPF hrefs are resolved relative to
// the package document's folder.
func pathDir(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

// spliceOp replaces src[start:end] with repl. Zero-length ranges (start==end)
// are insertions. Ops are applied in ascending order and must not overlap.
type spliceOp struct {
	start, end int
	repl       []byte
}

// rewriteOPF returns the edited OPF bytes plus, when a cover is being embedded,
// the resolved zip entry name that should receive the JPEG and whether that
// entry is new (must be appended) or already exists (must be overwritten).
func rewriteOPF(opfData []byte, opfDir string, edit MetadataEdit, index map[string]*zip.File) (newOPF []byte, coverZipName string, coverIsNew bool, err error) {
	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, "", false, fmt.Errorf("parse OPF: %w", err)
	}
	manifest := make(map[string]opfItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		if item.ID != "" {
			manifest[item.ID] = item
		}
	}

	scan, err := scanOPF(opfData)
	if err != nil {
		return nil, "", false, err
	}

	var ops []spliceOp

	if edit.Title != nil {
		repl := escapeXMLText(*edit.Title)
		switch {
		case scan.titleStart >= 0:
			ops = append(ops, spliceOp{scan.titleStart, scan.titleEnd, repl})
		case scan.metadataInsertAt >= 0:
			ins := append([]byte("\n    <dc:title>"), repl...)
			ins = append(ins, "</dc:title>"...)
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, ins})
		default:
			return nil, "", false, errors.New("OPF has no <metadata> element to set the title")
		}
	}

	if edit.Author != nil {
		repl := escapeXMLText(*edit.Author)
		switch {
		case scan.creatorStart >= 0:
			ops = append(ops, spliceOp{scan.creatorStart, scan.creatorEnd, repl})
		case strings.TrimSpace(*edit.Author) == "":
			// Clearing an author that has no <dc:creator> element: nothing to do.
		case scan.metadataInsertAt >= 0:
			ins := append([]byte("\n    <dc:creator>"), repl...)
			ins = append(ins, "</dc:creator>"...)
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, ins})
		default:
			return nil, "", false, errors.New("OPF has no <metadata> element to set the author")
		}
	}

	if edit.CoverJPEG != nil {
		coverResolved := findCoverPath(pkg, manifest, opfDir)
		if coverResolved != "" {
			coverZipName = coverResolved
			coverIsNew = false
			// Keep the manifest media-type consistent with the JPEG we write into
			// the (possibly non-JPEG) existing cover entry.
			for _, it := range scan.items {
				if resolvePath(opfDir, it.href) != coverResolved {
					continue
				}
				if !isJPEGMediaType(mediaTypeForHref(pkg, it.href, opfDir)) {
					tag := opfData[it.tagStart:it.tagEnd]
					if fixed, ok := replaceAttrValue(tag, "media-type", "image/jpeg"); ok {
						ops = append(ops, spliceOp{it.tagStart, it.tagEnd, fixed})
					}
				}
				break
			}
		} else {
			if scan.manifestInsertAt < 0 || scan.metadataInsertAt < 0 {
				return nil, "", false, errors.New("OPF missing <manifest>/<metadata> to add a cover")
			}
			href, zipName := uniqueCoverHref(opfDir, index)
			itemID := uniqueItemID(manifest)
			coverZipName = zipName
			coverIsNew = true
			itemXML := fmt.Sprintf("\n    <item id=%s href=%s media-type=\"image/jpeg\" properties=\"cover-image\"/>", xmlAttr(itemID), xmlAttr(href))
			ops = append(ops, spliceOp{scan.manifestInsertAt, scan.manifestInsertAt, []byte(itemXML)})
			metaXML := fmt.Sprintf("\n    <meta name=\"cover\" content=%s/>", xmlAttr(itemID))
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, []byte(metaXML)})
		}
	}

	return applySplices(opfData, ops), coverZipName, coverIsNew, nil
}

func applySplices(src []byte, ops []spliceOp) []byte {
	if len(ops) == 0 {
		return src
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].start != ops[j].start {
			return ops[i].start < ops[j].start
		}
		return ops[i].end < ops[j].end
	})
	var out bytes.Buffer
	out.Grow(len(src) + 256)
	pos := 0
	for _, op := range ops {
		if op.start < pos {
			// Defensive: overlapping ops should never be produced; skip rather
			// than corrupt the document.
			continue
		}
		out.Write(src[pos:op.start])
		out.Write(op.repl)
		pos = op.end
	}
	out.Write(src[pos:])
	return out.Bytes()
}

// opfItemSpan records a manifest <item>'s raw start-tag byte range plus its raw
// href attribute, so a targeted attribute splice can be applied later.
type opfItemSpan struct {
	href             string
	tagStart, tagEnd int
}

type opfScanResult struct {
	titleStart, titleEnd     int
	creatorStart, creatorEnd int
	manifestInsertAt         int
	metadataInsertAt         int
	items                    []opfItemSpan
}

// scanOPF walks the raw OPF tokens recording byte offsets we need to splice:
// the chardata range of the first <title> and <creator>, the start offset of
// the </manifest> and </metadata> end tags (insertion points), and every
// <item> start-tag span. It uses RawToken (no namespace resolution, no
// auto-matching) so offsets map exactly onto the original bytes; element local
// names are compared without prefixes.
func scanOPF(data []byte) (opfScanResult, error) {
	res := opfScanResult{
		titleStart: -1, titleEnd: -1,
		creatorStart: -1, creatorEnd: -1,
		manifestInsertAt: -1, metadataInsertAt: -1,
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	type frame struct {
		local      string
		contentBeg int
		selfClose  bool
	}
	var stack []frame

	for {
		startOff := int(dec.InputOffset())
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("scan OPF token: %w", err)
		}
		endOff := int(dec.InputOffset())

		switch t := tok.(type) {
		case xml.StartElement:
			selfClose := endOff-startOff >= 2 && data[endOff-1] == '>' && data[endOff-2] == '/'
			stack = append(stack, frame{local: t.Name.Local, contentBeg: endOff, selfClose: selfClose})
			if t.Name.Local == "item" {
				href := ""
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						href = a.Value
						break
					}
				}
				res.items = append(res.items, opfItemSpan{href: href, tagStart: startOff, tagEnd: endOff})
			}
		case xml.EndElement:
			beg := -1
			selfClose := false
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].local == t.Name.Local {
					beg = stack[i].contentBeg
					selfClose = stack[i].selfClose
					stack = stack[:i]
					break
				}
			}
			switch t.Name.Local {
			case "title":
				if res.titleStart < 0 && beg >= 0 && !selfClose {
					res.titleStart, res.titleEnd = beg, startOff
				}
			case "creator":
				if res.creatorStart < 0 && beg >= 0 && !selfClose {
					res.creatorStart, res.creatorEnd = beg, startOff
				}
			case "manifest":
				if res.manifestInsertAt < 0 {
					res.manifestInsertAt = startOff
				}
			case "metadata":
				if res.metadataInsertAt < 0 {
					res.metadataInsertAt = startOff
				}
			}
		}
	}
	return res, nil
}

// replaceAttrValue replaces the value of the attribute whose local name matches
// attrLocal within a single raw start-tag's bytes, preserving the rest of the
// tag (other attributes, quoting style, spacing) exactly. It returns the
// modified tag and true when the attribute was found.
func replaceAttrValue(tag []byte, attrLocal, newVal string) ([]byte, bool) {
	n := len(tag)
	i := 0
	if i < n && tag[i] == '<' {
		i++
	}
	for i < n && !isXMLSpace(tag[i]) && tag[i] != '>' && tag[i] != '/' {
		i++
	}
	for i < n {
		for i < n && isXMLSpace(tag[i]) {
			i++
		}
		if i >= n || tag[i] == '>' || tag[i] == '/' {
			break
		}
		nameStart := i
		for i < n && tag[i] != '=' && !isXMLSpace(tag[i]) && tag[i] != '>' && tag[i] != '/' {
			i++
		}
		name := string(tag[nameStart:i])
		for i < n && isXMLSpace(tag[i]) {
			i++
		}
		if i >= n || tag[i] != '=' {
			continue
		}
		i++ // '='
		for i < n && isXMLSpace(tag[i]) {
			i++
		}
		if i >= n {
			break
		}
		quote := tag[i]
		if quote != '"' && quote != '\'' {
			break
		}
		i++
		valStart := i
		for i < n && tag[i] != quote {
			i++
		}
		if i >= n {
			break
		}
		valEnd := i
		i++ // closing quote
		if attrLocalMatches(name, attrLocal) {
			var out bytes.Buffer
			out.Write(tag[:valStart])
			out.Write(escapeXMLText(newVal))
			out.Write(tag[valEnd:])
			return out.Bytes(), true
		}
	}
	return tag, false
}

func attrLocalMatches(name, local string) bool {
	if strings.EqualFold(name, local) {
		return true
	}
	if _, after, ok := strings.Cut(name, ":"); ok {
		return strings.EqualFold(after, local)
	}
	return false
}

func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isJPEGMediaType(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	return mt == "image/jpeg" || mt == "image/jpg"
}

func mediaTypeForHref(pkg opfPackage, href, opfDir string) string {
	target := resolvePath(opfDir, href)
	for _, it := range pkg.Manifest.Items {
		if resolvePath(opfDir, it.Href) == target {
			return it.MediaType
		}
	}
	return ""
}

// uniqueCoverHref returns an OPF-relative href (and the resolved zip entry name)
// for a freshly added cover image that collides with no existing zip entry.
func uniqueCoverHref(opfDir string, index map[string]*zip.File) (href, zipName string) {
	for i := 0; ; i++ {
		name := "sayumi-cover.jpg"
		if i > 0 {
			name = fmt.Sprintf("sayumi-cover-%d.jpg", i)
		}
		resolved := resolvePath(opfDir, name)
		_, exists := index[resolved]
		_, existsLower := index[strings.ToLower(resolved)]
		if !exists && !existsLower {
			return name, resolved
		}
	}
}

func uniqueItemID(manifest map[string]opfItem) string {
	for i := 0; ; i++ {
		id := "sayumi-cover-image"
		if i > 0 {
			id = fmt.Sprintf("sayumi-cover-image-%d", i)
		}
		if _, exists := manifest[id]; !exists {
			return id
		}
	}
}

// escapeXMLText escapes a string for use as element character data or as a
// double-quoted attribute value (encoding/xml escapes <, >, &, ', ", and
// whitespace control chars).
func escapeXMLText(s string) []byte {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.Bytes()
}

// xmlAttr renders a double-quoted XML attribute value with proper escaping.
func xmlAttr(s string) string {
	return "\"" + string(escapeXMLText(s)) + "\""
}
