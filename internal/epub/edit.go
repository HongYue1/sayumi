package epub

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
// Untouched zip entries retain their raw compressed bytes, compression method,
// and order via (*zip.Writer).Copy, including the mandatory uncompressed
// "mimetype"-first entry. Only the OPF (recompressed) and cover image (stored)
// are rewritten. A raw XML token scan locates byte ranges for splicing rather
// than re-marshaling, preserving untouched extensions, prefixes, and layout.
//
// The caller must keep the source generation stable throughout this call. The
// reader opened here is independently owned, not borrowed from EPUBStore.
// The temp file is dot-prefixed so a concurrent library scan (which skips
// dotfiles) ignores the in-progress rewrite. On error, no temp path is returned.
func RewriteBook(srcPath string, edit MetadataEdit) (tmpPath string, err error) {
	if edit.isEmpty() {
		return "", errors.New("epub rewrite: no edits requested")
	}

	zr, err := zip.OpenReader(srcPath)
	var tmp *os.File
	tmpClosed := false
	defer func() {
		// OpenReader can return both a reader and an error. Close our owned
		// source before deciding whether a completed temp can be handed back.
		if zr != nil {
			if closeErr := zr.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close epub: %w", closeErr)
			}
		}
		if tmp == nil {
			return
		}
		if !tmpClosed {
			if closeErr := tmp.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close temp epub: %w", closeErr)
			}
		}
		if err != nil {
			// Named return assignments may already have cleared tmpPath. The
			// file's own name remains stable on every failure path.
			if rmErr := os.Remove(tmp.Name()); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				slog.Error("remove temp epub failed", "path", tmp.Name(), "err", rmErr)
			}
			tmpPath = ""
		}
	}()
	if err != nil {
		return "", fmt.Errorf("open epub: %w", err)
	}

	index := buildIndex(&zr.Reader)

	opfPath, err := findOPFPath(index)
	if err != nil {
		return "", fmt.Errorf("find OPF: %w", err)
	}
	// Match the reader's tolerated leading slash before resolving OPF-relative
	// references; archive replacement itself uses the selected entry identity.
	opfPath = strings.TrimPrefix(strings.TrimSpace(opfPath), "/")
	opfFile, err := lookupInIndex(index, opfPath)
	if err != nil {
		return "", fmt.Errorf("read OPF: %w", err)
	}
	// Enforce the smaller rewrite limit before opening and while reading;
	// checking after the generic 64 MiB read has already spent that memory.
	if opfFile.UncompressedSize64 > maxOPFBytes {
		return "", fmt.Errorf("opf too large: declared %d bytes", opfFile.UncompressedSize64)
	}
	opfBody, err := opfFile.Open()
	if err != nil {
		return "", fmt.Errorf("open OPF: %w", err)
	}
	opfData, err := readLimitedZipBody(opfPath, opfBody, maxOPFBytes)
	if err != nil {
		return "", fmt.Errorf("read OPF: %w", err)
	}

	opfDir := pathDir(opfPath)
	newOPF, coverZipName, coverIsNew, err := rewriteOPF(opfData, opfDir, edit, index)
	if err != nil {
		return "", fmt.Errorf("rewrite OPF: %w", err)
	}

	var coverFile *zip.File
	if coverZipName != "" && !coverIsNew {
		coverFile, err = lookupInIndex(index, coverZipName)
		if err != nil {
			return "", fmt.Errorf("find cover entry: %w", err)
		}
	}

	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	tmp, err = os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp epub: %w", err)
	}
	zw := zip.NewWriter(tmp)

	wroteOPF := false
	wroteCover := false
	for _, f := range zr.File {
		// Replace the entry we read, not every case variant or duplicate name.
		switch f {
		case opfFile:
			if err := writeZipBytes(zw, f, zip.Deflate, newOPF); err != nil {
				return "", fmt.Errorf("write OPF entry: %w", err)
			}
			wroteOPF = true
		case coverFile:
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
		return "", fmt.Errorf("opf entry %q not found while rewriting", opfPath)
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
	tmpClosed = true
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp epub: %w", err)
	}
	return tmp.Name(), nil
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
	dir, _, ok := strings.CutLast(p, "/")
	if !ok {
		return ""
	}
	return dir
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
		switch {
		case scan.title.start >= 0:
			ops = append(ops, editOPFText(opfData, scan.title, *edit.Title, false))
		case scan.metadataInsertAt >= 0:
			// A document may bind dc to another URI, or use another prefix.
			ins := []byte("\n    <dc:title xmlns:dc=\"http://purl.org/dc/elements/1.1/\">")
			ins = append(ins, escapeXMLText(*edit.Title)...)
			ins = append(ins, "</dc:title>"...)
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, ins})
		default:
			return nil, "", false, errors.New("opf has no <metadata> element to set the title")
		}
	}

	if edit.Author != nil {
		switch {
		case len(scan.creators) > 0:
			for _, creator := range scan.creators {
				ops = append(ops, editOPFText(opfData, creator, *edit.Author, true))
				if strings.TrimSpace(*edit.Author) != "" {
					break
				}
				// Clearing only the first creator would make parseOPF expose the
				// next one as the author, so clear all creator values and sort keys.
			}
		case strings.TrimSpace(*edit.Author) == "":
			// No creator exists: clearing the field is already satisfied.
		case scan.metadataInsertAt >= 0:
			ins := []byte("\n    <dc:creator xmlns:dc=\"http://purl.org/dc/elements/1.1/\">")
			ins = append(ins, escapeXMLText(*edit.Author)...)
			ins = append(ins, "</dc:creator>"...)
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, ins})
		default:
			return nil, "", false, errors.New("opf has no <metadata> element to set the author")
		}
	}

	if edit.CoverJPEG != nil {
		coverResolved := findCoverPath(pkg, manifest, opfDir)
		if coverResolved != "" {
			coverZipName = coverResolved
			_, lookupErr := lookupInIndex(index, coverResolved)
			coverIsNew = lookupErr != nil
			// Fulfill a missing declared entry at its existing href. Adding a
			// different image leaves earlier EPUB2/EPUB3 cover references stale.
			for _, it := range scan.items {
				if resolvePath(opfDir, it.href) != coverResolved || isJPEGMediaType(it.mediaType) {
					continue
				}
				tag := opfData[it.tagStart:it.tagEnd]
				fixed, found := replaceAttrValue(tag, "media-type", "image/jpeg")
				if !found {
					at := len(tag) - 1
					if tag[at-1] == '/' {
						at--
					}
					fixed = append(bytes.Clone(tag[:at]), ` media-type="image/jpeg"`...)
					fixed = append(fixed, tag[at:]...)
				}
				ops = append(ops, spliceOp{it.tagStart, it.tagEnd, fixed})
			}
		} else {
			if scan.manifestInsertAt < 0 || scan.metadataInsertAt < 0 {
				return nil, "", false, errors.New("opf missing <manifest>/<metadata> to add a cover")
			}
			href, zipName := uniqueCoverHref(opfDir, index)
			itemID := uniqueItemID(manifest)
			coverZipName = zipName
			coverIsNew = true
			itemXML := fmt.Sprintf("\n    <item xmlns=\"http://www.idpf.org/2007/opf\" id=%s href=%s media-type=\"image/jpeg\" properties=\"cover-image\"/>", xmlAttr(itemID), xmlAttr(href))
			ops = append(ops, spliceOp{scan.manifestInsertAt, scan.manifestInsertAt, []byte(itemXML)})
			metaXML := fmt.Sprintf("\n    <meta xmlns=\"http://www.idpf.org/2007/opf\" name=\"cover\" content=%s/>", xmlAttr(itemID))
			ops = append(ops, spliceOp{scan.metadataInsertAt, scan.metadataInsertAt, []byte(metaXML)})
		}
	}

	return applySplices(opfData, ops), coverZipName, coverIsNew, nil
}

func applySplices(src []byte, ops []spliceOp) []byte {
	if len(ops) == 0 {
		return src
	}
	slices.SortStableFunc(ops, func(a, b spliceOp) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.end, b.end))
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

// A text span includes its start tag, so self-closing fields and creator sort
// keys can be updated atomically without overlapping splices or losing attrs.
type opfTextSpan struct {
	start, content, end int
	name                string
	selfClose           bool
}

func editOPFText(src []byte, span opfTextSpan, value string, fileAs bool) spliceOp {
	tag := src[span.start:span.content]
	if fileAs {
		// parseOPF prefers file-as to text; keep both values in step.
		tag, _ = replaceAttrValue(tag, "file-as", value)
	}
	if span.selfClose && value == "" {
		return spliceOp{span.start, span.end, tag}
	}
	var repl []byte
	if span.selfClose {
		repl = append(bytes.Clone(tag[:len(tag)-2]), '>')
	} else {
		repl = bytes.Clone(tag)
	}
	repl = append(repl, escapeXMLText(value)...)
	if span.selfClose {
		repl = append(repl, "</"...)
		repl = append(repl, span.name...)
		repl = append(repl, '>')
	}
	return spliceOp{span.start, span.end, repl}
}

// opfItemSpan records a direct manifest item's start tag and parsed attributes.
type opfItemSpan struct {
	href, mediaType  string
	tagStart, tagEnd int
}

type opfScanResult struct {
	title            opfTextSpan
	creators         []opfTextSpan
	manifestInsertAt int
	metadataInsertAt int
	items            []opfItemSpan
}

// scanOPF records only direct package metadata fields and manifest items, as
// opfPackage does. Same-named extension elements must never become edit targets.
// RawToken retains the original qualified names and byte offsets for splicing.
// Unmarshal reads only the first root; reject extra roots and non-whitespace
// outside it while tracking ancestry across the complete token stream.
func scanOPF(data []byte) (opfScanResult, error) {
	res := opfScanResult{
		title:            opfTextSpan{start: -1},
		manifestInsertAt: -1,
		metadataInsertAt: -1,
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	type frame struct {
		name       xml.Name
		contentBeg int
		selfClose  bool
		tagStart   int
	}
	var stack []frame
	seenRoot := false
	for {
		startOff := int(dec.InputOffset())
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			if len(stack) != 0 {
				return res, fmt.Errorf("scan OPF: %w", io.ErrUnexpectedEOF)
			}
			break
		}
		if err != nil {
			return res, fmt.Errorf("scan OPF token: %w", err)
		}
		endOff := int(dec.InputOffset())
		switch t := tok.(type) {
		case xml.CharData:
			if len(stack) == 0 {
				text := []byte(t)
				if startOff == 0 {
					text = bytes.TrimPrefix(text, []byte("\ufeff"))
				}
				for _, c := range text {
					if !isXMLSpace(c) {
						return res, errors.New("text outside OPF package")
					}
				}
			}
		case xml.StartElement:
			if len(stack) == 0 {
				if seenRoot {
					return res, errors.New("multiple OPF roots")
				}
				seenRoot = true
			}
			selfClose := endOff-startOff >= 2 && data[endOff-2] == '/'
			stack = append(stack, frame{t.Name, endOff, selfClose, startOff})
			if len(stack) == 3 && stack[0].name.Local == "package" && stack[1].name.Local == "manifest" && t.Name.Local == "item" {
				item := opfItemSpan{tagStart: startOff, tagEnd: endOff}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "href":
						item.href = a.Value
					case "media-type":
						item.mediaType = a.Value
					}
				}
				res.items = append(res.items, item)
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != t.Name {
				return res, errors.New("mismatched OPF end tag")
			}
			open := stack[len(stack)-1]
			if len(stack) == 3 && stack[0].name.Local == "package" && stack[1].name.Local == "metadata" {
				name := t.Name.Local
				if t.Name.Space != "" {
					name = t.Name.Space + ":" + name
				}
				span := opfTextSpan{open.tagStart, open.contentBeg, startOff, name, open.selfClose}
				switch t.Name.Local {
				case "title":
					if res.title.start < 0 {
						res.title = span
					}
				case "creator":
					res.creators = append(res.creators, span)
				}
			}
			if len(stack) == 2 && stack[0].name.Local == "package" && !open.selfClose {
				// Synthetic ends for <metadata/> and <manifest/> are not valid
				// insertion points: inserting there would create sibling nodes.
				switch t.Name.Local {
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
			stack = stack[:len(stack)-1]
		}
	}
	return res, nil
}

// replaceAttrValue replaces the last case-sensitive local-name match, just as
// encoding/xml decodes attributes into opfPackage. It preserves the rest of the
// tag (other attributes, quoting style, spacing) exactly. It returns the
// modified tag and true when the attribute was found.
func replaceAttrValue(tag []byte, attrLocal, newVal string) ([]byte, bool) {
	matchStart, matchEnd := -1, -1
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
			matchStart, matchEnd = valStart, valEnd
		}
	}
	if matchStart < 0 {
		return tag, false
	}
	var out bytes.Buffer
	out.Write(tag[:matchStart])
	out.Write(escapeXMLText(newVal))
	out.Write(tag[matchEnd:])
	return out.Bytes(), true
}

func attrLocalMatches(name, local string) bool {
	if name == local {
		return true
	}
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after == local
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
	// bytes.Buffer never returns an error, so the EscapeText error is
	// safely ignored here.
	_ = xml.EscapeText(&b, []byte(s))
	return b.Bytes()
}

// xmlAttr renders a double-quoted XML attribute value with proper escaping.
func xmlAttr(s string) string {
	return "\"" + string(escapeXMLText(s)) + "\""
}
