package epub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type SearchResult struct {
	ChapterIndex int    `json:"chapterIndex"`
	CharOffset   int    `json:"charOffset"`
	MatchLen     int    `json:"matchLen"`
	Snippet      string `json:"snippet"`
	SnippetStart int    `json:"snippetStart"`
	SnippetLen   int    `json:"snippetLen"`
}

type SearchResponse struct {
	Results    []SearchResult `json:"results"`
	HasMore    bool           `json:"hasMore"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type searchCursor struct {
	ChapterIndex int `json:"c"`
	CharOffset   int `json:"o"`
}

const snippetContextRunes = 80

func Search(
	ctx context.Context,
	store *EPUBStore,
	filePath string,
	spine []SpineEntry,
	query string,
	cursor string,
	limit int,
) (SearchResponse, error) {
	if limit <= 0 {
		limit = 20
	}

	// foldRunes applies unicode.ToLower per-rune rather than strings.ToLower.
	// strings.ToLower uses full Unicode case mappings that can expand a single
	// rune into multiple runes (e.g. Turkish İ → "i\u0307"), breaking the 1:1
	// rune correspondence between the folded text and the original that the
	// offset arithmetic below relies on. unicode.ToLower always returns exactly
	// one rune, so rune position i in the folded string equals rune position i
	// in the original.
	query = foldRunes(strings.TrimSpace(query))
	if query == "" {
		return SearchResponse{Results: []SearchResult{}}, nil
	}

	startChapter := 0
	startChar := 0
	if cursor != "" {
		if decoded, err := decodeCursor(cursor); err == nil {
			startChapter = max(decoded.ChapterIndex, 0)
			startChar = max(decoded.CharOffset, 0)
		} else {
			slog.Debug("ignoring malformed search cursor", "cursor", cursor, "err", err)
		}
	}

	qLen := utf8.RuneCountInString(query)
	queryByteLen := len(query)
	resultCap := limit + 1
	var results []SearchResult

	for chapterIndex := startChapter; chapterIndex < len(spine); chapterIndex++ {
		if err := ctx.Err(); err != nil {
			return SearchResponse{}, err
		}
		orig, textLower, err := chapterPlainText(store, filePath, spine, chapterIndex)
		if err != nil {
			slog.Warn("skipping chapter during search: failed to extract text",
				"chapter", chapterIndex, "err", err)
			continue
		}

		searchFrom := 0
		if chapterIndex == startChapter {
			searchFrom = startChar
		}

		byteSearchFrom := runeOffsetToByteIndex(textLower, searchFrom)
		runePos := searchFrom
		var origRunes []rune

		for bytePos := byteSearchFrom; bytePos <= len(textLower)-queryByteLen; {
			idx := strings.Index(textLower[bytePos:], query)
			if idx < 0 {
				break
			}

			matchByteStart := bytePos + idx
			runePos += utf8.RuneCountInString(textLower[bytePos:matchByteStart])
			matchStart := runePos

			if origRunes == nil {
				origRunes = []rune(orig)
			}

			snippetFrom := max(matchStart-snippetContextRunes, 0)
			snippetTo := min(matchStart+qLen+snippetContextRunes, len(origRunes))
			snippet := string(origRunes[snippetFrom:snippetTo])

			snippetStart := matchStart - snippetFrom
			// Clamp to zero: near the end of a chapter snippetTo-snippetFrom-snippetStart
			// can be smaller than qLen, producing a negative value without the guard.
			snippetLen := max(0, min(qLen, (snippetTo-snippetFrom)-snippetStart))

			results = append(results, SearchResult{
				ChapterIndex: chapterIndex,
				CharOffset:   matchStart,
				MatchLen:     qLen,
				Snippet:      snippet,
				SnippetStart: snippetStart,
				SnippetLen:   snippetLen,
			})

			if len(results) == resultCap {
				extra := results[limit]
				return SearchResponse{
					Results:    results[:limit],
					HasMore:    true,
					NextCursor: encodeCursor(searchCursor{ChapterIndex: extra.ChapterIndex, CharOffset: extra.CharOffset}),
				}, nil
			}

			bytePos = matchByteStart + queryByteLen
			runePos += qLen
		}
	}

	return SearchResponse{Results: results, HasMore: false}, nil
}

func chapterPlainText(
	store *EPUBStore,
	filePath string,
	spine []SpineEntry,
	chapterIndex int,
) (orig, lower string, err error) {
	if chapterIndex < 0 || chapterIndex >= len(spine) {
		return "", "", fmt.Errorf("chapter index %d out of range", chapterIndex)
	}

	if orig, lower, ok := store.GetText(filePath, chapterIndex); ok {
		return orig, lower, nil
	}

	_, index, err := store.OpenIndexed(filePath)
	if err != nil {
		return "", "", fmt.Errorf("open epub for text extraction: %w", err)
	}
	defer store.Release(filePath)

	hrefPath := spine[chapterIndex].Href
	if idx := strings.Index(hrefPath, "#"); idx != -1 {
		hrefPath = hrefPath[:idx]
	}

	rawHTML, err := readZipFileIndexed(index, hrefPath)
	if err != nil {
		return "", "", fmt.Errorf("read chapter %s for text extraction: %w", hrefPath, err)
	}

	doc, err := html.Parse(bytes.NewReader(rawHTML))
	if err != nil {
		return "", "", fmt.Errorf("parse html for text extraction: %w", err)
	}

	var extractor plainTextExtractor
	extractor.extract(doc)
	orig = extractor.String()
	// Use foldRunes (unicode.ToLower per-rune) instead of strings.ToLower so
	// that the rune count of lower always equals the rune count of orig. Some
	// Unicode code points (e.g. Turkish İ) expand to two runes under
	// strings.ToLower, which would shift rune offsets and corrupt snippets.
	lower = foldRunes(orig)

	store.SetText(filePath, chapterIndex, orig, lower)
	return orig, lower, nil
}

type plainTextExtractor struct {
	builder      strings.Builder
	pendingSpace bool
}

func (e *plainTextExtractor) String() string {
	return strings.TrimSpace(e.builder.String())
}

func (e *plainTextExtractor) writeBoundary() {
	if e.builder.Len() == 0 {
		return
	}
	e.pendingSpace = true
}

func (e *plainTextExtractor) writeText(text string) {
	for _, r := range text {
		if unicode.IsSpace(r) {
			e.pendingSpace = true
			continue
		}
		if e.pendingSpace && e.builder.Len() > 0 {
			e.builder.WriteByte(' ')
		}
		e.pendingSpace = false
		e.builder.WriteRune(r)
	}
}

func (e *plainTextExtractor) extract(node *html.Node) {
	if node == nil {
		return
	}

	if node.Type == html.ElementNode {
		switch node.DataAtom {
		case atom.Head, atom.Script, atom.Style:
			return
		case atom.Br:
			e.writeBoundary()
			return
		}

		boundary := isTextBoundaryElement(node.DataAtom, node.Data)
		if boundary {
			e.writeBoundary()
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			e.extract(child)
		}
		if boundary {
			e.writeBoundary()
		}
		return
	}

	if node.Type == html.TextNode {
		e.writeText(node.Data)
		return
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		e.extract(child)
	}
}

func isTextBoundaryElement(tag atom.Atom, rawTag string) bool {
	if tag == 0 {
		switch strings.ToLower(rawTag) {
		case "svg", "math":
			return true
		default:
			return false
		}
	}

	switch tag {
	case atom.Address, atom.Article, atom.Aside, atom.Blockquote,
		atom.Caption, atom.Div, atom.Dd, atom.Dl, atom.Dt,
		atom.Figcaption, atom.Figure, atom.Footer, atom.Form,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Header, atom.Hr, atom.Li, atom.Main, atom.Nav,
		atom.Ol, atom.P, atom.Pre, atom.Section,
		atom.Table, atom.Tbody, atom.Td, atom.Tfoot, atom.Th,
		atom.Thead, atom.Tr, atom.Ul:
		return true
	default:
		return false
	}
}

// foldRunes returns s with each rune replaced by unicode.ToLower(r). Unlike
// strings.ToLower it never expands a single rune into multiple runes, so
// utf8.RuneCountInString(foldRunes(s)) == utf8.RuneCountInString(s) always.
func foldRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func runeOffsetToByteIndex(s string, runeOffset int) int {
	if runeOffset <= 0 {
		return 0
	}
	runeCount := 0
	for i := range s {
		if runeCount == runeOffset {
			return i
		}
		runeCount++
	}
	return len(s)
}

func encodeCursor(c searchCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (searchCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return searchCursor{}, fmt.Errorf("decode cursor base64: %w", err)
	}
	var cursor searchCursor
	if err := json.Unmarshal(b, &cursor); err != nil {
		return searchCursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	return cursor, nil
}
