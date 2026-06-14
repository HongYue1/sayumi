package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const ChapterRenderVersion = "2026-03-22-1"

type ChapterResponse struct {
	ChapterIndex int    `json:"chapterIndex"`
	HTML         string `json:"html"`
	CSS          string `json:"css"`
	FontFaceCSS  string `json:"fontFaceCSS"`
	Direction    string `json:"direction"`
	WritingMode  string `json:"writingMode"`
	ResourceBase string `json:"resourceBase"`
}

func ProcessChapter(
	store *EPUBStore,
	filePath string,
	spine []SpineEntry,
	chapterIndex int,
	bookID string,
	bookDirection string,
	resourceToken string,
) (ChapterResponse, error) {
	if chapterIndex < 0 || chapterIndex >= len(spine) {
		return ChapterResponse{}, fmt.Errorf("chapter index %d out of range (0-%d)", chapterIndex, len(spine)-1)
	}

	if cached, ok := store.GetChapter(filePath, chapterIndex, ChapterRenderVersion); ok {
		return cached, nil
	}

	entry := spine[chapterIndex]
	hrefPath := entry.Href
	if idx := strings.Index(hrefPath, "#"); idx != -1 {
		hrefPath = hrefPath[:idx]
	}

	_, index, err := store.OpenIndexed(filePath)
	if err != nil {
		return ChapterResponse{}, fmt.Errorf("open epub: %w", err)
	}
	defer store.Release(filePath)

	rawHTML, err := readZipFileIndexed(index, hrefPath)
	if err != nil {
		return ChapterResponse{}, fmt.Errorf("read chapter %s: %w", hrefPath, err)
	}

	chapterDir := path.Dir(hrefPath)
	if chapterDir == "." {
		chapterDir = ""
	}
	resourceBase := fmt.Sprintf("/api/books/%s/resources", bookID)

	resp, err := processChapterHTML(
		rawHTML,
		chapterDir,
		resourceBase,
		chapterIndex,
		bookDirection,
		index,
		store,
		filePath,
		resourceToken,
	)
	if err != nil {
		return ChapterResponse{}, err
	}

	store.SetChapter(filePath, chapterIndex, ChapterRenderVersion, resp)
	return resp, nil
}

// writingModeRe matches the CSS writing-mode property set to a vertical value.
// Anchoring on the property name avoids false positives from class names,
// comments, or string literals that contain "vertical-rl" / "vertical-lr".
var writingModeRe = regexp.MustCompile(`(?i)writing-mode\s*:\s*(vertical-rl|vertical-lr)`)

func processChapterHTML(
	rawHTML []byte,
	chapterDir string,
	resourceBase string,
	chapterIndex int,
	bookDirection string,
	index map[string]*zip.File,
	store *EPUBStore,
	filePath string,
	resourceToken string,
) (ChapterResponse, error) {
	doc, err := html.Parse(bytes.NewReader(rawHTML))
	if err != nil {
		return ChapterResponse{}, fmt.Errorf("parse html: %w", err)
	}

	Sanitize(doc)

	htmlNode, headNode, bodyNode := findStructural(doc)

	direction := bookDirection
	if direction == "" {
		direction = "ltr"
	}
	writingMode := "horizontal-tb"

	if htmlNode != nil {
		for _, attr := range htmlNode.Attr {
			if strings.EqualFold(attr.Key, "dir") {
				direction = strings.ToLower(attr.Val)
			}
		}
	}

	if bodyNode != nil {
		for _, attr := range bodyNode.Attr {
			if strings.EqualFold(attr.Key, "dir") {
				direction = strings.ToLower(attr.Val)
			}
		}
	}

	// Clamp to the two values the renderer understands. "auto" and unknown
	// values from EPUB HTML attributes fall back to the book-level default.
	switch direction {
	case "ltr", "rtl":
	default:
		direction = bookDirection
		if direction == "" {
			direction = "ltr"
		}
	}

	var cssBuilder strings.Builder
	var fontFaceBuilder strings.Builder
	extractCSS(headNode, chapterDir, resourceBase, index, store, filePath, &cssBuilder, &fontFaceBuilder, resourceToken)

	css := cssBuilder.String()
	// Head CSS takes priority for writing-mode. EPUBs that instead set it on a
	// body <style> block or an inline style attribute must still be detected, so
	// when head CSS doesn't resolve a vertical mode we collect it from the body.
	// That collection is folded into the URL-rewrite traversal below (wmOut) so
	// the body subtree is walked once per render instead of twice.
	var bodyWritingMode string
	var wmOut *string
	if m := writingModeRe.FindStringSubmatch(css); m != nil {
		writingMode = strings.ToLower(m[1])
	} else {
		wmOut = &bodyWritingMode
	}

	if bodyNode != nil {
		rewriteNodeURLs(bodyNode, chapterDir, resourceBase, resourceToken, wmOut)
		if wmOut != nil && bodyWritingMode != "" {
			writingMode = bodyWritingMode
		}
	} else {
		// No <body>: rewrite the whole document and, matching prior behavior,
		// leave writing-mode at the default.
		rewriteNodeURLs(doc, chapterDir, resourceBase, resourceToken, nil)
	}

	bodyHTML, err := extractBodyHTML(bodyNode, doc)
	if err != nil {
		return ChapterResponse{}, fmt.Errorf("render chapter %d body: %w", chapterIndex, err)
	}

	return ChapterResponse{
		ChapterIndex: chapterIndex,
		HTML:         bodyHTML,
		CSS:          css,
		FontFaceCSS:  fontFaceBuilder.String(),
		Direction:    direction,
		WritingMode:  writingMode,
		ResourceBase: resourceBase,
	}, nil
}

func findStructural(root *html.Node) (htmlNode, headNode, bodyNode *html.Node) {
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Html:
				htmlNode = n
			case atom.Head:
				headNode = n
			case atom.Body:
				bodyNode = n
			}
		}
		if htmlNode != nil && headNode != nil && bodyNode != nil {
			return true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if walk(child) {
				return true
			}
		}
		return false
	}
	walk(root)
	return
}

func extractCSS(
	headNode *html.Node,
	chapterDir string,
	resourceBase string,
	index map[string]*zip.File,
	store *EPUBStore,
	filePath string,
	cssOut *strings.Builder,
	fontFaceOut *strings.Builder,
	resourceToken string,
) {
	if headNode == nil {
		return
	}

	var toRemove []*html.Node

	for child := headNode.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}

		if child.DataAtom == atom.Style {
			cssText := nodeText(child)
			separateFontFaces(cssText, chapterDir, resourceBase, cssOut, fontFaceOut, resourceToken)
			toRemove = append(toRemove, child)
			continue
		}

		if child.DataAtom == atom.Link {
			rel := getAttr(child, "rel")
			href := getAttr(child, "href")
			if hasToken(rel, "stylesheet") && href != "" {
				cssPath := resolvePath(chapterDir, href)
				if frag, ok := store.GetCSSFragment(filePath, cssPath); ok {
					// Shared stylesheets are decompressed and rewritten once
					// per book, then replayed for every chapter that links the
					// same sheet. Safe because resourceBase and resourceToken
					// are deterministic per book (the same invariant the
					// chapter cache already relies on).
					cssOut.WriteString(frag.css)
					fontFaceOut.WriteString(frag.fontFace)
				} else if cssData, err := readZipFileIndexed(index, cssPath); err != nil {
					slog.Warn("stylesheet not found in epub", "path", cssPath, "err", err)
				} else {
					var cssFrag, fontFaceFrag strings.Builder
					separateFontFaces(string(cssData), path.Dir(cssPath), resourceBase, &cssFrag, &fontFaceFrag, resourceToken)
					frag := cssFragment{css: cssFrag.String(), fontFace: fontFaceFrag.String()}
					store.SetCSSFragment(filePath, cssPath, frag)
					cssOut.WriteString(frag.css)
					fontFaceOut.WriteString(frag.fontFace)
				}
				toRemove = append(toRemove, child)
			}
		}
	}

	for _, node := range toRemove {
		headNode.RemoveChild(node)
	}
}

var (
	fontFaceRegex = regexp.MustCompile(`(?is)@font-face\s*\{(?:[^{}]|/\*.*?\*/)*\}`)
	cssURLRegex   = regexp.MustCompile(`url\(\s*['"]?(?:[^'"\s)]+)['"]?\s*\)`)
)

// cssImportStringRegex matches bare @import "path" / @import 'path' rules.
// RE2 (Go's regexp engine) does not support backreferences, so the closing
// quote is captured as group 4 and validated for equality with group 2 inside
// the replacement callback. The url() form is handled by cssURLRegex above.
var cssImportStringRegex = regexp.MustCompile(`(?i)(@import\s+)(['"])([^'"]+)(['"])`)

// separateFontFaces splits cssText into @font-face blocks (written to fontFaceOut)
// and the remaining rules (written to cssOut), rewriting resource URLs in both.
func separateFontFaces(
	cssText string,
	cssDir string,
	resourceBase string,
	cssOut *strings.Builder,
	fontFaceOut *strings.Builder,
	resourceToken string,
) {
	remaining := fontFaceRegex.ReplaceAllStringFunc(cssText, func(match string) string {
		rewritten := rewriteCSSURLs(match, cssDir, resourceBase, resourceToken)
		fontFaceOut.WriteString(rewritten)
		fontFaceOut.WriteByte('\n')
		return ""
	})

	remaining = strings.TrimSpace(remaining)
	if remaining != "" {
		rewritten := rewriteCSSURLs(remaining, cssDir, resourceBase, resourceToken)
		cssOut.WriteString(rewritten)
		cssOut.WriteByte('\n')
	}
}

func rewriteCSSURLs(cssText, cssDir, resourceBase, resourceToken string) string {
	// Rewrite url(...) tokens.
	result := cssURLRegex.ReplaceAllStringFunc(cssText, func(match string) string {
		paren := strings.IndexByte(match, '(')
		if paren < 0 || match[len(match)-1] != ')' {
			return match
		}

		inner := strings.TrimSpace(match[paren+1 : len(match)-1])
		rawURL := strings.Trim(inner, "'\"")
		if isExternalResourceReference(rawURL) {
			// Neutralize remote refs so opening a book can't beacon reading
			// activity to an external origin. about:invalid is the CSS-defined
			// invalid URL that is guaranteed never to load.
			return "url(about:invalid)"
		}
		if !isRewritableResourceReference(rawURL) {
			return match
		}

		rewritten, ok := buildResourceURL(cssDir, resourceBase, rawURL, resourceToken)
		if !ok {
			return match
		}
		return fmt.Sprintf("url('%s')", rewritten)
	})

	// Rewrite bare @import "path" / @import 'path' strings. The url() pass
	// above already handles @import url(...), so only the string form remains.
	// Group layout: [1]=@import+space [2]=open-quote [3]=path [4]=close-quote.
	// We validate that open and close quotes match because RE2 has no
	// backreference support.
	result = cssImportStringRegex.ReplaceAllStringFunc(result, func(match string) string {
		subs := cssImportStringRegex.FindStringSubmatch(match)
		if len(subs) < 5 || subs[2] != subs[4] {
			return match
		}
		rawURL := subs[3]
		if isExternalResourceReference(rawURL) {
			// Drop the remote target of an @import "…" rule while keeping the
			// rule syntactically valid; about:invalid never loads.
			return subs[1] + subs[2] + "about:invalid" + subs[4]
		}
		if !isRewritableResourceReference(rawURL) {
			return match
		}
		rewritten, ok := buildResourceURL(cssDir, resourceBase, rawURL, resourceToken)
		if !ok {
			return match
		}
		return subs[1] + subs[2] + rewritten + subs[4]
	})

	return result
}

func rewriteNodeURLs(n *html.Node, chapterDir, resourceBase, resourceToken string, wmOut *string) {
	rewriteNodeURLsDepth(n, chapterDir, resourceBase, resourceToken, 0, wmOut)
}

// rewriteNodeURLsDepth rewrites resource URLs across the subtree and, when wmOut
// is non-nil and still empty, opportunistically records the first vertical
// writing-mode found in an inline style attribute or a <style> block. Detection
// is gated on *wmOut == "" so it stops after the first hit; the rewrite itself
// always continues. wmOut is nil when head CSS already resolved writing-mode.
func rewriteNodeURLsDepth(n *html.Node, chapterDir, resourceBase, resourceToken string, depth int, wmOut *string) {
	if depth > maxSanitizeDepth {
		return
	}

	if n.Type == html.ElementNode {
		if n.DataAtom == atom.Style {
			rewriteStyleElementText(n, chapterDir, resourceBase, resourceToken, wmOut)
		}

		for i, attr := range n.Attr {
			key := strings.ToLower(attr.Key)

			switch key {
			case "style":
				if wmOut != nil && *wmOut == "" {
					if m := writingModeRe.FindStringSubmatch(attr.Val); m != nil {
						*wmOut = strings.ToLower(m[1])
					}
				}
				n.Attr[i].Val = rewriteCSSURLs(attr.Val, chapterDir, resourceBase, resourceToken)
				continue
			case "srcset":
				n.Attr[i].Val = rewriteSrcsetValue(attr.Val, chapterDir, resourceBase, resourceToken)
				continue
			}

			shouldRewrite := false
			switch key {
			case "src", "poster":
				shouldRewrite = true
			case "href":
				if n.DataAtom != atom.A {
					shouldRewrite = true
				}
			case "xlink:href":
				if n.Data == "image" || n.Data == "use" {
					shouldRewrite = true
				}
			}

			if !shouldRewrite || !isRewritableResourceReference(attr.Val) {
				continue
			}

			if rewritten, ok := buildResourceURL(chapterDir, resourceBase, attr.Val, resourceToken); ok {
				n.Attr[i].Val = rewritten
			}
		}
	}

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		rewriteNodeURLsDepth(child, chapterDir, resourceBase, resourceToken, depth+1, wmOut)
	}
}

func rewriteStyleElementText(n *html.Node, chapterDir, resourceBase, resourceToken string, wmOut *string) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.TextNode {
			continue
		}
		if wmOut != nil && *wmOut == "" {
			if m := writingModeRe.FindStringSubmatch(child.Data); m != nil {
				*wmOut = strings.ToLower(m[1])
			}
		}
		child.Data = rewriteCSSURLs(child.Data, chapterDir, resourceBase, resourceToken)
	}
}

func rewriteSrcsetValue(srcset, chapterDir, resourceBase, resourceToken string) string {
	parts := strings.Split(srcset, ",")
	rewrittenAny := false

	for i, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			parts[i] = strings.TrimSpace(part)
			continue
		}

		rewrittenURL, ok := buildResourceURL(chapterDir, resourceBase, fields[0], resourceToken)
		if ok {
			fields[0] = rewrittenURL
			rewrittenAny = true
		}
		parts[i] = strings.Join(fields, " ")
	}

	if !rewrittenAny {
		return srcset
	}
	return strings.Join(parts, ", ")
}

func buildResourceURL(baseDir, resourceBase, rawRef, resourceToken string) (string, bool) {
	if !isRewritableResourceReference(rawRef) {
		return "", false
	}

	refPath, rawQuery, fragment := splitResourceReference(rawRef)
	if refPath == "" {
		return "", false
	}

	resolved := resolvePath(baseDir, refPath)
	if resolved == "" || resolved == "." {
		return "", false
	}

	var builder strings.Builder
	builder.Grow(len(resourceBase) + len(resolved) + len(rawQuery) + len(fragment) + len(resourceToken) + 16)
	builder.WriteString(resourceBase)
	builder.WriteByte('/')
	builder.WriteString(resolved)

	hasQuery := false
	if rawQuery != "" {
		builder.WriteByte('?')
		builder.WriteString(rawQuery)
		hasQuery = true
	}
	if resourceToken != "" {
		if hasQuery {
			builder.WriteByte('&')
		} else {
			builder.WriteByte('?')
		}
		builder.WriteString("token=")
		builder.WriteString(url.QueryEscape(resourceToken))
	}
	if fragment != "" {
		builder.WriteByte('#')
		builder.WriteString(fragment)
	}

	return builder.String(), true
}

// isExternalResourceReference reports whether rawRef points at a remote origin
// that must not be fetched while rendering a chapter. EPUBs are local content,
// so a stylesheet, @font-face, or @import that pulls from http(s):// or a
// protocol-relative //host URL would let a crafted book beacon reading activity
// (which book, when, which page) to an external server. The reader iframe's CSP
// permits style-src/font-src/img-src from any origin, so this is the layer that
// actually blocks the leak. data: and blob: are inline / same-document and are
// left intact — EPUBs legitimately embed base64 fonts and images via data:.
func isExternalResourceReference(rawRef string) bool {
	trimmed := strings.TrimSpace(rawRef)
	if strings.HasPrefix(trimmed, "//") {
		return true
	}
	if !hasAbsoluteURIScheme(trimmed) {
		return false
	}
	scheme := strings.ToLower(trimmed[:strings.IndexByte(trimmed, ':')])
	switch scheme {
	case "data", "blob":
		return false
	default:
		return true
	}
}

func isRewritableResourceReference(rawRef string) bool {
	rawRef = strings.TrimSpace(rawRef)
	if rawRef == "" {
		return false
	}

	lower := strings.ToLower(rawRef)
	switch {
	case strings.HasPrefix(lower, "#"),
		strings.HasPrefix(lower, "data:"),
		strings.HasPrefix(lower, "blob:"),
		strings.HasPrefix(lower, "/api/"),
		strings.HasPrefix(lower, "//"):
		return false
	}

	return !hasAbsoluteURIScheme(rawRef)
}

func hasAbsoluteURIScheme(rawRef string) bool {
	for idx, r := range rawRef {
		switch r {
		case ':':
			if idx == 0 {
				return false
			}
			return isURIScheme(rawRef[:idx])
		case '/', '?', '#':
			return false
		}
	}
	return false
}

func isURIScheme(value string) bool {
	if value == "" {
		return false
	}

	first := value[0]
	if (first < 'a' || first > 'z') && (first < 'A' || first > 'Z') {
		return false
	}

	for i := 1; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '+' || ch == '-' || ch == '.' {
			continue
		}
		return false
	}

	return true
}

func splitResourceReference(ref string) (refPath, rawQuery, fragment string) {
	if hashIndex := strings.IndexByte(ref, '#'); hashIndex != -1 {
		fragment = ref[hashIndex+1:]
		ref = ref[:hashIndex]
	}
	if queryIndex := strings.IndexByte(ref, '?'); queryIndex != -1 {
		rawQuery = ref[queryIndex+1:]
		ref = ref[:queryIndex]
	}
	return ref, rawQuery, fragment
}

func extractBodyHTML(bodyNode *html.Node, doc *html.Node) (string, error) {
	var buf bytes.Buffer
	if bodyNode == nil {
		if err := html.Render(&buf, doc); err != nil {
			slog.Error("failed to render full document", "err", err)
			return "", fmt.Errorf("render document: %w", err)
		}
		return buf.String(), nil
	}

	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&buf, child); err != nil {
			slog.Error("failed to render body child node", "err", err)
			return "", fmt.Errorf("render body child: %w", err)
		}
	}
	return buf.String(), nil
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}
