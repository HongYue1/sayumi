package epub

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// BenchmarkSearch separates warm text-cache scans from extraction. Cold cases
// include text-key eviction but retain the ZIP reader and warm OS cache; they
// are not cold-disk measurements. No throughput is reported for searches that
// can stop early. Keep this harness identical for paired before/after runs.
func BenchmarkSearch(b *testing.B) {
	plain := strings.Repeat(`<p class="line">alpha needle omega.</p>`, 256)
	unicodeText := strings.Repeat(`<p>İstanbul KELVIN 😀 needle é。</p>`, 256)
	svg := strings.Repeat(`<svg><text>alpha needle omega</text><path id="p" d="M0 0" /></svg>`, 128)
	structural := strings.Repeat(`<template>ghost needle</template><object><p>alpha needle omega</p></object>`, 128)
	depth := strings.Repeat("<div>", 498) + "needle" + strings.Repeat("</div>", 498)
	cases := []struct {
		name     string
		chapters []string
		query    string
		limit    int
		cold     bool
		wantHits int
		wantMore bool
	}{
		{name: "WarmPage", chapters: []string{plain}, query: "needle", limit: 20, wantHits: 20, wantMore: true},
		{name: "WarmUnicodePage", chapters: []string{unicodeText}, query: "istanbul", limit: 20, wantHits: 20, wantMore: true},
		{
			name:     "WarmNextChapter",
			chapters: []string{"<p>needle</p>", "<p>" + strings.Repeat("quiet ", 16384) + "needle</p>"},
			query:    "needle", limit: 1, wantHits: 1, wantMore: true,
		},
		{name: "WarmNoMatch", chapters: []string{plain}, query: "absent", limit: 20},
		{name: "ColdPlain", chapters: []string{plain}, query: "needle", limit: 20, cold: true, wantHits: 20, wantMore: true},
		{name: "ColdSVG", chapters: []string{svg}, query: "needle", limit: 20, cold: true, wantHits: 20, wantMore: true},
		{name: "ColdStructural", chapters: []string{structural}, query: "needle", limit: 20, cold: true, wantHits: 20, wantMore: true},
		// The original extractor omits the retained depth-501 text leaf. This
		// fixture intentionally measures different correct/incorrect outputs;
		// compare extraction cost, not an equivalent-work speedup claim.
		{name: "ColdDepthBoundary", chapters: []string{depth}, query: "needle", limit: 20, cold: true, wantHits: -1},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			zipPath, spine := writeSearchBenchmarkEPUB(b, tc.chapters)
			store := NewStore(4)
			b.Cleanup(store.Close)
			ctx := b.Context()
			// The same warm-up also establishes a per-variant response oracle.
			// ColdStructural offsets intentionally change when inert template
			// content stops being indexed; warm/cold results must still agree.
			want, err := Search(ctx, store, zipPath, spine, tc.query, "", tc.limit)
			if err != nil {
				b.Fatal(err)
			}
			if tc.wantHits >= 0 && (len(want.Results) != tc.wantHits || want.HasMore != tc.wantMore) {
				b.Fatalf("unexpected fixture response: %+v", want)
			}
			if tc.wantHits < 0 && (len(want.Results) > 1 || want.HasMore) {
				b.Fatalf("unexpected depth fixture response: %+v", want)
			}
			textRunes := 0
			for i := range spine {
				orig, _, ok := store.GetText(zipPath, i)
				if !ok {
					b.Fatalf("warm-up did not cache chapter %d", i)
				}
				textRunes += utf8.RuneCountInString(orig)
			}
			b.Logf("fixture: chapters=%d text-runes=%d hits=%d more=%v", len(spine), textRunes, len(want.Results), want.HasMore)

			var got SearchResponse
			b.ReportAllocs()
			for b.Loop() {
				if tc.cold {
					for i := range spine {
						store.texts.Delete(chapterTextKey{filePath: zipPath, chapterIndex: i})
					}
				}
				got, err = Search(ctx, store, zipPath, spine, tc.query, "", tc.limit)
				if err != nil {
					b.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, want) {
				b.Fatalf("response changed during benchmark: %+v; want %+v", got, want)
			}
		})
	}
}

// Use a self-contained fixture writer so older frozen benchmark harnesses and
// *testing.T-only helpers do not need to change for this benchmark.
func writeSearchBenchmarkEPUB(b *testing.B, bodies []string) (string, []SpineEntry) {
	b.Helper()
	zipPath := filepath.Join(b.TempDir(), "search.epub")
	file, err := os.Create(zipPath)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			b.Error(err)
		}
	}()
	writer := zip.NewWriter(file)
	spine := make([]SpineEntry, len(bodies))
	for i, body := range bodies {
		name := fmt.Sprintf("ch%d.xhtml", i)
		entry, err := writer.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := fmt.Fprintf(entry, "<html><head></head><body>%s</body></html>", body); err != nil {
			b.Fatal(err)
		}
		spine[i] = SpineEntry{Href: name, ID: fmt.Sprintf("c%d", i), Linear: true}
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	return zipPath, spine
}
