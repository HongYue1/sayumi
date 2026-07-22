package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const maxTOCDepth = 50

type BookMeta struct {
	Title       string       `json:"title"`
	Author      string       `json:"author"`
	Language    string       `json:"language"`
	Publisher   string       `json:"publisher"`
	Description string       `json:"description"`
	PubDate     string       `json:"pubDate"`
	ISBN        string       `json:"isbn"`
	CoverPath   string       `json:"coverPath"`
	Direction   string       `json:"direction"`
	Spine       []SpineEntry `json:"spine"`
	TOC         []TocEntry   `json:"toc"`
}

type SpineEntry struct {
	Href      string `json:"href"`
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	Linear    bool   `json:"linear"`
}

type TocEntry struct {
	Title    string     `json:"title"`
	Href     string     `json:"href"`
	Depth    int        `json:"depth"`
	Children []TocEntry `json:"children,omitempty"`
}

func Parse(zr *zip.Reader) (BookMeta, error) {
	// Build the entry index once up front so every OPF/TOC lookup below resolves
	// in O(1). The previous path rescanned zr.File linearly — and allocated a
	// lowercased copy of every entry name — on each lookup, which adds up on
	// EPUBs with thousands of entries during a library scan.
	index := buildIndex(zr)

	opfPath, err := findOPFPath(index)
	if err != nil {
		return BookMeta{}, fmt.Errorf("find OPF: %w", err)
	}

	opfData, err := readZipFileIndexed(index, opfPath)
	if err != nil {
		return BookMeta{}, fmt.Errorf("read OPF: %w", err)
	}

	opfDir := path.Dir(opfPath)
	meta, pkg, err := parseOPF(opfData, opfDir)
	if err != nil {
		return BookMeta{}, fmt.Errorf("parse OPF: %w", err)
	}

	if toc := parseTOCFromZip(index, pkg, opfDir); len(toc) > 0 {
		meta.TOC = toc
	} else {
		meta.TOC = []TocEntry{}
	}

	if meta.Spine == nil {
		meta.Spine = []SpineEntry{}
	}

	return meta, nil
}

type containerXML struct {
	XMLName   xml.Name `xml:"container"`
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

func findOPFPath(index map[string]*zip.File) (string, error) {
	data, err := readZipFileIndexed(index, "META-INF/container.xml")
	if err != nil {
		return "", fmt.Errorf("read container.xml: %w", err)
	}

	var container containerXML
	if err := xml.Unmarshal(data, &container); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}

	for _, rootfile := range container.Rootfiles {
		fullPath := strings.TrimSpace(rootfile.FullPath)
		if fullPath == "" {
			continue
		}
		if strings.EqualFold(rootfile.MediaType, "application/oebps-package+xml") ||
			strings.HasSuffix(strings.ToLower(fullPath), ".opf") {
			return fullPath, nil
		}
	}

	for _, rootfile := range container.Rootfiles {
		fullPath := strings.TrimSpace(rootfile.FullPath)
		if fullPath != "" {
			return fullPath, nil
		}
	}

	return "", errors.New("no rootfile found in container.xml")
}

type opfPackage struct {
	XMLName   xml.Name    `xml:"package"`
	Direction string      `xml:"dir,attr"`
	Metadata  opfMetadata `xml:"metadata"`
	Manifest  struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Direction string       `xml:"page-progression-direction,attr"`
		Toc       string       `xml:"toc,attr"`
		ItemRefs  []opfItemRef `xml:"itemref"`
	} `xml:"spine"`
}

type opfMetadata struct {
	Titles       []string        `xml:"title"`
	Creators     []opfCreator    `xml:"creator"`
	Languages    []string        `xml:"language"`
	Publishers   []string        `xml:"publisher"`
	Descriptions []string        `xml:"description"`
	Dates        []string        `xml:"date"`
	Identifiers  []opfIdentifier `xml:"identifier"`
	Metas        []opfMeta       `xml:"meta"`
}

type opfCreator struct {
	Value  string `xml:",chardata"`
	FileAs string `xml:"file-as,attr"`
	Role   string `xml:"role,attr"`
}

type opfIdentifier struct {
	Value  string `xml:",chardata"`
	Scheme string `xml:"scheme,attr"`
}

type opfMeta struct {
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Value    string `xml:",chardata"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfItemRef struct {
	IDRef  string `xml:"idref,attr"`
	Linear string `xml:"linear,attr"`
}

func parseOPF(data []byte, opfDir string) (BookMeta, opfPackage, error) {
	var pkg opfPackage
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return BookMeta{}, pkg, fmt.Errorf("unmarshal OPF: %w", err)
	}

	manifest := make(map[string]opfItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		if item.ID == "" {
			continue
		}
		manifest[item.ID] = item
	}

	meta := BookMeta{
		Title:       firstNonEmpty(pkg.Metadata.Titles),
		Language:    firstNonEmpty(pkg.Metadata.Languages),
		Publisher:   firstNonEmpty(pkg.Metadata.Publishers),
		Description: firstNonEmpty(pkg.Metadata.Descriptions),
		PubDate:     firstNonEmpty(pkg.Metadata.Dates),
	}

	for _, creator := range pkg.Metadata.Creators {
		if fileAs := strings.TrimSpace(creator.FileAs); fileAs != "" {
			meta.Author = fileAs
			break
		}
		if value := strings.TrimSpace(creator.Value); value != "" {
			meta.Author = value
			break
		}
	}

	for _, ident := range pkg.Metadata.Identifiers {
		value := strings.TrimSpace(ident.Value)
		scheme := strings.ToLower(strings.TrimSpace(ident.Scheme))
		if value == "" {
			continue
		}
		if scheme == "isbn" || looksLikeISBN(value) {
			meta.ISBN = value
			break
		}
	}

	meta.Direction = "ltr"
	if strings.EqualFold(pkg.Spine.Direction, "rtl") || strings.EqualFold(pkg.Direction, "rtl") {
		meta.Direction = "rtl"
	}

	meta.CoverPath = findCoverPath(pkg, manifest, opfDir)

	// Preallocate to the itemref count (an upper bound, since some refs may not
	// resolve). Large books carry thousands of spine entries; growing from nil
	// would reallocate and copy the backing array ~log2(n) times during parse.
	meta.Spine = make([]SpineEntry, 0, len(pkg.Spine.ItemRefs))
	for _, ref := range pkg.Spine.ItemRefs {
		item, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}

		href := resolvePath(opfDir, item.Href)
		if href == "" {
			continue
		}

		meta.Spine = append(meta.Spine, SpineEntry{
			Href:      href,
			ID:        item.ID,
			MediaType: item.MediaType,
			Linear:    !strings.EqualFold(ref.Linear, "no"),
		})
	}

	return meta, pkg, nil
}

func findCoverPath(pkg opfPackage, manifest map[string]opfItem, opfDir string) string {
	for _, meta := range pkg.Metadata.Metas {
		if strings.EqualFold(meta.Name, "cover") && meta.Content != "" {
			item, ok := manifest[meta.Content]
			if ok && strings.HasPrefix(strings.ToLower(item.MediaType), "image/") {
				return resolvePath(opfDir, item.Href)
			}
		}
	}

	for _, item := range pkg.Manifest.Items {
		if hasToken(item.Properties, "cover-image") {
			return resolvePath(opfDir, item.Href)
		}
	}

	for _, item := range pkg.Manifest.Items {
		lowerID := strings.ToLower(item.ID)
		if strings.Contains(lowerID, "cover") && strings.HasPrefix(strings.ToLower(item.MediaType), "image/") {
			return resolvePath(opfDir, item.Href)
		}
	}

	return ""
}

func parseTOCFromZip(index map[string]*zip.File, pkg opfPackage, opfDir string) []TocEntry {
	if toc := parseNav(index, pkg, opfDir); len(toc) > 0 {
		return toc
	}
	return parseNCX(index, pkg, opfDir)
}

func parseNav(index map[string]*zip.File, pkg opfPackage, opfDir string) []TocEntry {
	var navHref string
	for _, item := range pkg.Manifest.Items {
		if hasToken(item.Properties, "nav") {
			navHref = resolvePath(opfDir, item.Href)
			break
		}
	}
	if navHref == "" {
		return nil
	}

	data, err := readZipFileIndexed(index, navHref)
	if err != nil {
		return nil
	}

	return parseNavHTML(data, navHref)
}

func parseNavHTML(data []byte, navPath string) []TocEntry {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil
	}

	navNode := findNavTOC(doc)
	if navNode == nil {
		return nil
	}

	// Find the first <ol> anywhere under the nav (not only a direct child).
	// Real EPUB 3 NAVs often wrap the list in <div>/<section>/heading chrome.
	ol := findFirstOL(navNode, 0)
	if ol == nil {
		return nil
	}
	return parseOLNode(ol, navPath, 0)
}

// findFirstOL returns the first <ol> under root, depth-bounded like other
// hostile-HTML walks in this package.
func findFirstOL(root *html.Node, depth int) *html.Node {
	if root == nil || depth > maxSanitizeDepth {
		return nil
	}
	if root.Type == html.ElementNode && root.DataAtom == atom.Ol {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirstOL(child, depth+1); found != nil {
			return found
		}
	}
	return nil
}

func findNavTOC(node *html.Node) *html.Node {
	return findNavTOCDepth(node, 0)
}

func findNavTOCDepth(node *html.Node, depth int) *html.Node {
	if node == nil || depth > maxSanitizeDepth {
		return nil
	}

	if node.Type == html.ElementNode && node.DataAtom == atom.Nav {
		for _, attr := range node.Attr {
			switch {
			case attr.Namespace == "epub" && attr.Key == "type" && hasToken(attr.Val, "toc"):
				return node
			case attr.Namespace == "" && attr.Key == "epub:type" && hasToken(attr.Val, "toc"):
				return node
			case attr.Namespace == "" && strings.EqualFold(attr.Key, "role") && hasToken(attr.Val, "doc-toc"):
				return node
			}
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findNavTOCDepth(child, depth+1); found != nil {
			return found
		}
	}
	return nil
}

func parseOLNode(ol *html.Node, basePath string, depth int) []TocEntry {
	if depth > maxTOCDepth {
		return nil
	}

	var entries []TocEntry

	for child := ol.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.DataAtom != atom.Li {
			continue
		}

		var href string
		var title string
		var childOL *html.Node

		// Search the entire <li> subtree rather than only direct children.
		// Valid EPUB 3 NAV documents may wrap <a> or title text in arbitrary
		// block elements (e.g. <div>, <p>), and some generators nest the
		// sub-<ol> inside a wrapper element as well. A shallow scan silently
		// drops those entries.
		var scanLi func(*html.Node, int)
		scanLi = func(n *html.Node, wrapperDepth int) {
			if wrapperDepth > maxSanitizeDepth {
				return
			}
			for gc := n.FirstChild; gc != nil; gc = gc.NextSibling {
				if gc.Type != html.ElementNode {
					continue
				}
				switch gc.DataAtom {
				case atom.A:
					if href == "" {
						for _, attr := range gc.Attr {
							if strings.EqualFold(attr.Key, "href") {
								href = attr.Val
								break
							}
						}
					}
					if title == "" {
						title = strings.TrimSpace(nodeText(gc))
					}
				case atom.Span:
					if title == "" {
						title = strings.TrimSpace(nodeText(gc))
					}
				case atom.Ol:
					if childOL == nil {
						childOL = gc
					}
				default:
					// Recurse into any other element (div, p, section, etc.)
					// that might wrap the anchor or title text. Bound wrapper
					// recursion separately from logical TOC depth so a malformed
					// nav cannot overflow the stack before maxTOCDepth applies.
					scanLi(gc, wrapperDepth+1)
				}
			}
		}
		scanLi(child, 0)

		if title == "" {
			continue
		}

		entry := TocEntry{
			Title: title,
			Href:  resolveReference(basePath, href),
			Depth: depth,
		}

		if childOL != nil {
			entry.Children = parseOLNode(childOL, basePath, depth+1)
		}

		entries = append(entries, entry)
	}

	return entries
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	if node == nil {
		return ""
	}

	type frame struct {
		node  *html.Node
		depth int
	}
	stack := make([]frame, 0, 16)
	stack = append(stack, frame{node: node})
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]

		if current.node.Type == html.TextNode {
			builder.WriteString(current.node.Data)
		}
		if current.depth >= maxSanitizeDepth {
			continue
		}
		for child := current.node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, frame{node: child, depth: current.depth + 1})
		}
	}

	return builder.String()
}

func parseNCX(index map[string]*zip.File, pkg opfPackage, opfDir string) []TocEntry {
	tocID := pkg.Spine.Toc
	var ncxHref string

	if tocID != "" {
		for _, item := range pkg.Manifest.Items {
			if item.ID == tocID {
				ncxHref = resolvePath(opfDir, item.Href)
				break
			}
		}
	}

	if ncxHref == "" {
		for _, item := range pkg.Manifest.Items {
			if item.MediaType == "application/x-dtbncx+xml" {
				ncxHref = resolvePath(opfDir, item.Href)
				break
			}
		}
	}

	if ncxHref == "" {
		return nil
	}

	data, err := readZipFileIndexed(index, ncxHref)
	if err != nil {
		return nil
	}

	return parseNCXData(data, ncxHref)
}

type ncxDocument struct {
	XMLName xml.Name  `xml:"ncx"`
	NavMap  ncxNavMap `xml:"navMap"`
}

type ncxNavMap struct {
	Points []ncxNavPoint `xml:"navPoint"`
}

type ncxNavPoint struct {
	Label   ncxLabel      `xml:"navLabel"`
	Content ncxContent    `xml:"content"`
	Points  []ncxNavPoint `xml:"navPoint"`
}

type ncxLabel struct {
	Text string `xml:"text"`
}

type ncxContent struct {
	Src string `xml:"src,attr"`
}

func parseNCXData(data []byte, ncxPath string) []TocEntry {
	var doc ncxDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil
	}

	return buildNavPoints(doc.NavMap.Points, ncxPath, 0)
}

func buildNavPoints(points []ncxNavPoint, basePath string, depth int) []TocEntry {
	if depth > maxTOCDepth {
		return nil
	}

	// Sized to the navPoint count at this level (upper bound; entries with empty
	// titles are skipped). An empty result stays len 0 and is dropped by the
	// Children `omitempty` tag, so output is unchanged versus a nil slice.
	entries := make([]TocEntry, 0, len(points))

	for _, point := range points {
		title := strings.TrimSpace(point.Label.Text)
		if title == "" {
			continue
		}

		entry := TocEntry{
			Title: title,
			Href:  resolveReference(basePath, point.Content.Src),
			Depth: depth,
		}

		if len(point.Points) > 0 {
			entry.Children = buildNavPoints(point.Points, basePath, depth+1)
		}

		entries = append(entries, entry)
	}

	return entries
}

func resolvePath(dir, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}

	refPath, _, _ := splitResourceReference(href)
	refPath = strings.TrimSpace(refPath)
	if refPath == "" {
		return ""
	}

	if dir == "" || dir == "." {
		return strings.TrimPrefix(path.Clean("/"+refPath), "/")
	}

	return strings.TrimPrefix(path.Clean(path.Join("/", dir, refPath)), "/")
}

func resolveReference(basePath, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}

	refPath, rawQuery, fragment := splitResourceReference(href)
	resolvedPath := ""
	if strings.TrimSpace(refPath) == "" {
		resolvedPath = strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(basePath, "/")), "/")
	} else {
		resolvedPath = resolvePath(path.Dir(basePath), refPath)
	}

	var builder strings.Builder
	if resolvedPath != "" {
		builder.WriteString(resolvedPath)
	}
	if rawQuery != "" {
		builder.WriteByte('?')
		builder.WriteString(rawQuery)
	}
	if fragment != "" {
		builder.WriteByte('#')
		builder.WriteString(fragment)
	}

	return builder.String()
}

func hasToken(value, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}

	return slices.Contains(strings.Fields(strings.ToLower(value)), target)
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func looksLikeISBN(s string) bool {
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")

	switch len(s) {
	case 10:
		for _, c := range s[:9] {
			if c < '0' || c > '9' {
				return false
			}
		}
		last := s[9]
		return (last >= '0' && last <= '9') || last == 'X' || last == 'x'
	case 13:
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	default:
		return false
	}
}
