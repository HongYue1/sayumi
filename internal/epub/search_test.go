package epub

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func TestFoldRunesKeepsOneToOneLength(t *testing.T) {
	t.Parallel()

	cases := []string{
		"Hello",
		"İstanbul",     // Turkish I with dot; strings.ToLower can expand
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

	if strings.Contains(got, "s()") || strings.Contains(got, ".x") {
		t.Fatalf("script/style leaked: %q", got)
	}
	if strings.Contains(got, "hidden noscript") {
		t.Fatalf("noscript leaked: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Fatalf("missing body text: %q", got)
	}
	// Block boundaries should separate Hello and World.
	if !strings.Contains(got, "Hello World") && !strings.Contains(got, "Hello  World") {
		// writeBoundary collapses to single spaces between blocks.
		if found := strings.Contains(got, "Hello"); !found {
			t.Fatalf("no Hello in %q", got)
		}
	}
	if !strings.Contains(got, "More text") {
		t.Fatalf("inline tags should not split words oddly: %q", got)
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
	ctx := context.Background()

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
	}
}
