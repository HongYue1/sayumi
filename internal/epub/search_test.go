package epub

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func TestFoldRunesKeepsOneToOneLength(t *testing.T) {
	t.Parallel()

	cases := []string{
		"Hello",
		"İstanbul",     // Simple lowercase preserves one rune, unlike full JavaScript casing.
		"Straße\u00df", // sharp s
		"",
		"a\u0301", // combining accent stays one base+mark as separate runes in input
	}
	for _, s := range cases {
		got := foldRunes(s)
		if utf8.RuneCountInString(got) != utf8.RuneCountInString(s) {
			t.Fatalf("foldRunes(%q) rune count %d != %d (%q)",
				s, utf8.RuneCountInString(got), utf8.RuneCountInString(s), got)
		}
	}
	if foldRunes("AbC") != "abc" {
		t.Fatalf("foldRunes AbC = %q", foldRunes("AbC"))
	}
	if got := foldRunes("İKΣςßẞ𐐀e\u0301"); got != "ikσςßß𐐨e\u0301" {
		t.Fatalf("simple lowercase mapping = %q", got)
	}
}

func TestRuneOffsetToByteIndex(t *testing.T) {
	t.Parallel()

	s := "aéc" // a, e-acute, c
	if got := runeOffsetToByteIndex(s, 0); got != 0 {
		t.Fatalf("offset 0 = %d", got)
	}
	if got := runeOffsetToByteIndex(s, 1); got != 1 {
		t.Fatalf("offset 1 = %d, want 1", got)
	}
	if got := runeOffsetToByteIndex(s, 2); got != 1+utf8.RuneLen('é') {
		t.Fatalf("offset 2 = %d", got)
	}
	if got := runeOffsetToByteIndex(s, 99); got != len(s) {
		t.Fatalf("past end = %d, want %d", got, len(s))
	}
	if got := runeOffsetToByteIndex(s, -1); got != 0 {
		t.Fatalf("negative = %d", got)
	}
}

func TestSearchCursorRoundTrip(t *testing.T) {
	t.Parallel()

	c := searchCursor{ChapterIndex: 3, CharOffset: 42}
	enc := encodeCursor(c)
	if enc == "" {
		t.Fatal("empty cursor encoding")
	}
	got, err := decodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != c {
		t.Fatalf("round-trip = %+v, want %+v", got, c)
	}
	if _, err := decodeCursor("not-valid!!!"); err == nil {
		t.Fatal("bad cursor: want error")
	}
}

func TestPlainTextExtractor(t *testing.T) {
	t.Parallel()

	htmlIn := strings.Join([]string{
		"<html><head><title>T</title><style>.x{}</style><script>s()</script></head>",
		"<body>",
		"<p>Hello</p><p>World</p>",
		"<br>",
		"<noscript>hidden noscript</noscript>",
		"<div>More <b>text</b></div>",
		"</body></html>",
	}, "")
	doc, err := html.Parse(strings.NewReader(htmlIn))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var ext plainTextExtractor
	ext.extract(doc)
	got := ext.String()

	if got != "Hello World More text" {
		t.Fatalf("extracted text = %q; want %q", got, "Hello World More text")
	}
}

func TestSearchEmptyQueryAndPagination(t *testing.T) {
	t.Parallel()

	// Two chapters with known plain text.
	zipPath := writeTestEPUB(t, map[string]string{
		"ch0.xhtml": `<html><body><p>alpha beta alpha</p></body></html>`,
		"ch1.xhtml": `<html><body><p>alpha gamma</p></body></html>`,
	})
	store := NewStore(4)
	t.Cleanup(func() { store.Close() })

	spine := []SpineEntry{
		{Href: "ch0.xhtml", ID: "c0", Linear: true},
		{Href: "ch1.xhtml", ID: "c1", Linear: true},
	}
	ctx := t.Context()

	empty, err := Search(ctx, store, zipPath, spine, "   ", "", 10)
	if err != nil {
		t.Fatalf("empty query: %v", err)
	}
	if empty.Results == nil || len(empty.Results) != 0 || empty.HasMore {
		t.Fatalf("empty query resp = %+v", empty)
	}

	// Three "alpha" hits total (2 in ch0, 1 in ch1). Page size 2.
	page1, err := Search(ctx, store, zipPath, spine, "alpha", "", 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Results) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v", page1)
	}
	if page1.Results[0].ChapterIndex != 0 || page1.Results[1].ChapterIndex != 0 {
		t.Fatalf("page1 chapters = %+v", page1.Results)
	}

	page2, err := Search(ctx, store, zipPath, spine, "alpha", page1.NextCursor, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Results) != 1 || page2.HasMore {
		t.Fatalf("page2 = %+v", page2)
	}
	if page2.Results[0].ChapterIndex != 1 {
		t.Fatalf("page2 first = %+v", page2.Results[0])
	}

	// No overlap: page2 first offset must not equal either page1 hit in ch0.
	for _, r := range page1.Results {
		if r.ChapterIndex == page2.Results[0].ChapterIndex && r.CharOffset == page2.Results[0].CharOffset {
			t.Fatalf("duplicate across pages: %+v", r)
		}
	}

	// Malformed cursor is ignored (starts from beginning).
	fromBad, err := Search(ctx, store, zipPath, spine, "alpha", "!!!bad!!!", 10)
	if err != nil {
		t.Fatalf("bad cursor: %v", err)
	}
	if len(fromBad.Results) != 3 {
		t.Fatalf("bad cursor results = %d, want 3", len(fromBad.Results))
	}

	// Snippet contains the match.
	for _, r := range fromBad.Results {
		if r.MatchLen <= 0 || r.Snippet == "" {
			t.Fatalf("bad result shape: %+v", r)
		}
		snippetRunes := []rune(r.Snippet)
		if r.SnippetStart < 0 || r.SnippetStart+r.SnippetLen > len(snippetRunes) {
			t.Fatalf("snippet bounds: %+v len=%d", r, len(snippetRunes))
		}
		if got := string(snippetRunes[r.SnippetStart : r.SnippetStart+r.SnippetLen]); got != "alpha" {
			t.Fatalf("snippet match = %q; want alpha", got)
		}
	}
}

func TestSearchSanitizedText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "removed_svg_subtree",
			body: `lead<svg><foreignObject><div>hidden</div></foreignObject><text>svg</text></svg><p>needle</p>tail`,
			want: "leadsvg needle tail",
		},
		{
			name: "html_template_content_is_inert",
			body: `<span>lead</span><template><p>hidden</p></template><span>needle</span>`,
			want: "leadneedle",
		},
		{
			// The frame's HTML tagName checks are uppercase. Foreign-namespace
			// style/section/template nodes instead have ordinary traversable children.
			name: "svg_namespace_is_not_html",
			body: `<svg><style>styletext</style><section>sectiontext</section><template>templatetext</template><text>needle</text></svg>`,
			want: "styletextsectiontexttemplatetextneedle",
		},
		{
			name: "unwrapped_nodes_do_not_consume_rendered_depth",
			body: strings.Repeat("<object>", maxSanitizeDepth) + "needle" + strings.Repeat("</object>", maxSanitizeDepth),
			want: "needle",
		},
		{
			// document(0), html(1), body(2), 498 divs, retained text leaf(501).
			name: "retained_depth_leaf",
			body: strings.Repeat("<div>", maxSanitizeDepth-2) + "needle" + strings.Repeat("</div>", maxSanitizeDepth-2),
			want: "needle",
		},
		{
			name: "pruned_depth_subtree",
			body: strings.Repeat("<div>", maxSanitizeDepth-1) + "needle" + strings.Repeat("</div>", maxSanitizeDepth-1),
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := "<html><head></head><body>" + tc.body + "</body></html>"
			// Verify the retained/pruned marker through the actual sanitizer and
			// body renderer, rather than treating the raw ZIP text as the oracle.
			doc, err := html.Parse(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			Sanitize(doc)
			_, _, body := findStructural(doc)
			rendered, err := extractBodyHTML(body, doc, len(raw))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(rendered, "needle") != strings.Contains(tc.want, "needle") {
				t.Fatalf("rendered marker disagrees with fixture: %q", rendered)
			}

			zipPath := writeTestEPUB(t, map[string]string{"ch.xhtml": raw})
			spine := []SpineEntry{{Href: "ch.xhtml#fragment", ID: "chapter", Linear: true}}
			spineBefore := append([]SpineEntry(nil), spine...)
			store := NewStore(1)
			t.Cleanup(store.Close)
			var previous SearchResponse
			for pass := range 2 {
				got, err := Search(t.Context(), store, zipPath, spine, "needle", "", 10)
				if err != nil {
					t.Fatal(err)
				}
				orig, lower, ok := store.GetText(zipPath, 0)
				if !ok || orig != tc.want || lower != foldRunes(tc.want) {
					t.Errorf("pass %d cached text = (%q, %q, %v); want %q", pass, orig, lower, ok, tc.want)
				}
				wantResults := []SearchResult{}
				if at := strings.Index(tc.want, "needle"); at >= 0 {
					offset := utf8.RuneCountInString(tc.want[:at])
					wantResults = append(wantResults, SearchResult{
						ChapterIndex: 0, CharOffset: offset, MatchLen: 6,
						Snippet: tc.want, SnippetStart: offset, SnippetLen: 6,
					})
				}
				if want := (SearchResponse{Results: wantResults}); !reflect.DeepEqual(got, want) {
					t.Errorf("pass %d response = %+v; want %+v", pass, got, want)
				}
				if pass > 0 && !reflect.DeepEqual(got, previous) {
					t.Errorf("warm-cache response changed: %+v -> %+v", previous, got)
				}
				previous = got
				assertSearchReleased(t, store)
			}
			if !reflect.DeepEqual(spine, spineBefore) {
				t.Fatalf("search mutated the shared spine: %+v", spine)
			}
		})
	}
}

func TestSearchCancellation(t *testing.T) {
	t.Parallel()

	t.Run("before_archive_access", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		store := NewStore(1)
		t.Cleanup(store.Close)
		missing := filepath.Join(t.TempDir(), "missing.epub")
		for _, spine := range [][]SpineEntry{nil, {{Href: "ch.xhtml"}}} {
			for _, cursor := range []string{"", encodeCursor(searchCursor{ChapterIndex: 99})} {
				if _, err := Search(ctx, store, missing, spine, "needle", cursor, 1); !errors.Is(err, context.Canceled) {
					t.Errorf("canceled search = %v; want context.Canceled", err)
				}
			}
		}
		// Empty queries remain a no-op even with a canceled context and no archive.
		got, err := Search(ctx, store, missing, nil, " \t\u0085 ", "", 1)
		if err != nil || !reflect.DeepEqual(got, SearchResponse{Results: []SearchResult{}}) {
			t.Fatalf("empty canceled query = %+v, %v", got, err)
		}
		assertSearchReleased(t, store)
	})

	for _, tc := range []struct {
		name      string
		query     string
		readError bool
	}{
		{name: "page_lookahead", query: "needle"},
		{name: "no_match", query: "absent"},
		{name: "read_error", query: "needle", readError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			zipPath := writeTestEPUB(t, map[string]string{"ch.xhtml": "<p>needle needle</p>"})
			store := NewStore(1)
			t.Cleanup(store.Close)
			// Configure before publication. OpenIndexed lends a shared read-only
			// reader, so registering a decompressor on a borrowed reader is invalid.
			_, err := store.acquireWithOpener(zipPath, func(filePath string) (*zip.ReadCloser, error) {
				reader, err := zip.OpenReader(filePath)
				if err != nil {
					return nil, err
				}
				reader.RegisterDecompressor(zip.Deflate, func(src io.Reader) io.ReadCloser {
					cancel()
					if tc.readError {
						return io.NopCloser(searchErrorReader{})
					}
					return flate.NewReader(src)
				})
				return reader, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			store.Release(zipPath) // Release the setup borrow, independently of Search.
			_, err = Search(ctx, store, zipPath, []SpineEntry{{Href: "ch.xhtml"}}, tc.query, "", 1)
			if ctx.Err() == nil {
				t.Fatal("fixture did not cancel during the ZIP chapter read")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("search = %v; want context.Canceled", err)
			}
			assertSearchReleased(t, store)
		})
	}
}

type searchErrorReader struct{}

func (searchErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func assertSearchReleased(t *testing.T, store *EPUBStore) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	for filePath, entry := range store.openFiles {
		if entry.refs != 0 {
			t.Errorf("search left %d ZIP borrows for %q", entry.refs, filePath)
		}
	}
}

func TestSearchUnicodeSnippetsAndCursor(t *testing.T) {
	t.Parallel()

	text := strings.Repeat("é", 90) + "😀 İstanbul e\u0301 " + strings.Repeat("界", 90) + " istanbul"
	zipPath := writeTestEPUB(t, map[string]string{"ch.xhtml": "<p>" + text + "</p>"})
	store := NewStore(1)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "ch.xhtml"}}
	runes := []rune(text)
	cursor := ""
	for page, offset := range []int{92, 195} {
		got, err := Search(t.Context(), store, zipPath, spine, " \u0085İSTANBUL\u00a0", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		from, to := max(offset-80, 0), min(offset+8+80, len(runes))
		want := SearchResult{
			ChapterIndex: 0, CharOffset: offset, MatchLen: 8,
			Snippet: string(runes[from:to]), SnippetStart: offset - from, SnippetLen: 8,
		}
		if len(got.Results) != 1 || got.Results[0] != want || got.HasMore != (page == 0) {
			t.Fatalf("page %d = %+v; want %+v", page, got, want)
		}
		if page == 0 {
			decoded, err := decodeCursor(got.NextCursor)
			if err != nil || decoded != (searchCursor{ChapterIndex: 0, CharOffset: 195}) {
				t.Fatalf("lookahead cursor = %+v, %v", decoded, err)
			}
		} else if got.NextCursor != "" {
			t.Fatalf("final page retained cursor %q", got.NextCursor)
		}
		cursor = got.NextCursor
		assertSearchReleased(t, store)
	}
}

func TestSearchLimitsAndNonOverlappingMatches(t *testing.T) {
	t.Parallel()

	zipPath := writeTestEPUB(t, map[string]string{
		"many.xhtml":    "<p>" + strings.Repeat("needle ", 21) + "</p>",
		"overlap.xhtml": "<p>aaaa</p>",
	})
	store := NewStore(2)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "many.xhtml"}}
	for _, limit := range []int{-1, 0, 1, 20, 21, 22} {
		got, err := Search(t.Context(), store, zipPath, spine, "needle", "", limit)
		if err != nil {
			t.Fatal(err)
		}
		wantLimit := limit
		if wantLimit <= 0 {
			wantLimit = 20
		}
		wantCount := min(wantLimit, 21)
		if len(got.Results) != wantCount || got.HasMore != (wantCount < 21) {
			t.Fatalf("limit %d = %+v", limit, got)
		}
		for i, result := range got.Results {
			if result.CharOffset != i*7 || result.MatchLen != 6 || result.SnippetLen != 6 {
				t.Fatalf("limit %d hit %d = %+v", limit, i, result)
			}
		}
		if got.HasMore {
			decoded, err := decodeCursor(got.NextCursor)
			if err != nil || decoded != (searchCursor{CharOffset: wantCount * 7}) {
				t.Fatalf("limit %d cursor = %+v, %v", limit, decoded, err)
			}
		} else if got.NextCursor != "" {
			t.Fatalf("limit %d: unexpected cursor %q", limit, got.NextCursor)
		}
	}
	// A different chapter index avoids reusing the many.xhtml text-cache key.
	overlapSpine := []SpineEntry{{Href: "many.xhtml"}, {Href: "overlap.xhtml"}}
	got, err := Search(t.Context(), store, zipPath, overlapSpine, "aa", encodeCursor(searchCursor{ChapterIndex: 1}), 10)
	if err != nil || len(got.Results) != 2 || got.Results[0].CharOffset != 0 || got.Results[1].CharOffset != 2 {
		t.Fatalf("non-overlapping search = %+v, %v", got, err)
	}
	assertSearchReleased(t, store)
}

func TestSearchMissingChaptersAndEmptyResults(t *testing.T) {
	t.Parallel()

	zipPath := writeTestEPUB(t, map[string]string{"ch.xhtml": "<p>needle</p>"})
	store := NewStore(1)
	t.Cleanup(store.Close)
	spine := []SpineEntry{{Href: "missing.xhtml"}, {Href: "ch.xhtml#anchor"}}
	got, err := Search(t.Context(), store, zipPath, spine, "needle", encodeCursor(searchCursor{ChapterIndex: -1, CharOffset: -2}), 10)
	if err != nil || len(got.Results) != 1 || got.Results[0].ChapterIndex != 1 || got.Results[0].CharOffset != 0 {
		t.Fatalf("skip missing chapter / clamp cursor = %+v, %v", got, err)
	}
	for _, cursor := range []string{"", encodeCursor(searchCursor{ChapterIndex: 9}), encodeCursor(searchCursor{ChapterIndex: 1, CharOffset: 999})} {
		got, err := Search(t.Context(), store, zipPath, spine, "absent", cursor, 10)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(got)
		if err != nil || string(data) != `{"results":[],"hasMore":false}` {
			t.Fatalf("empty result JSON = %s, %v", data, err)
		}
	}
	if _, err := Search(t.Context(), store, filepath.Join(t.TempDir(), "missing.epub"), spine, "needle", "", 10); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing archive error = %v", err)
	}
	for _, chapterIndex := range []int{-1, len(spine)} {
		if _, _, err := chapterPlainText(store, zipPath, nil, spine, chapterIndex); err == nil {
			t.Fatalf("chapter index %d: want bounds error", chapterIndex)
		}
	}
	assertSearchReleased(t, store)
}

func FuzzSearchRuneOffsets(f *testing.F) {
	f.Add("İK😀e\u0301", 2)
	f.Add("", -1)
	f.Add("a\xffb", 99)
	f.Fuzz(func(t *testing.T, text string, offset int) {
		if len(text) > 64<<10 {
			t.Skip()
		}
		folded := foldRunes(text)
		if !utf8.ValidString(folded) || utf8.RuneCountInString(folded) != utf8.RuneCountInString(text) {
			t.Fatal("folding broke the one-code-point-per-rune contract")
		}
		runes := []rune(folded)
		want := len(string(runes[:min(max(offset, 0), len(runes))]))
		if got := runeOffsetToByteIndex(folded, offset); got != want {
			t.Fatalf("byte index %d; want %d", got, want)
		}
		cursor := searchCursor{ChapterIndex: len(text), CharOffset: offset}
		if got, err := decodeCursor(encodeCursor(cursor)); err != nil || got != cursor {
			t.Fatalf("cursor round trip = %+v, %v; want %+v", got, err, cursor)
		}
	})
}
