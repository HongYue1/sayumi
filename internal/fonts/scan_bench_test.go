package fonts

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cp15BenchmarkScanner(tb testing.TB, families int) *Scanner {
	tb.Helper()
	data := fontData["AtkinsonHyperlegibleNext-VariableFont.woff2"]
	if len(data) == 0 {
		tb.Fatal("embedded benchmark fixture missing")
	}
	tb.Logf(
		"fixture_bytes=%d fixture_sha256=%x families=%d",
		len(data),
		sha256.Sum256(data),
		families,
	)
	root := tb.TempDir()
	stamp := time.Unix(1700000000, 0)
	for i := range families {
		dir := filepath.Join(root, fmt.Sprintf("Family%02d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		name := filepath.Join(dir, "Regular.woff2")
		if err := os.WriteFile(name, data, 0o644); err != nil {
			tb.Fatal(err)
		}
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			tb.Fatal(err)
		}
		meta := []byte(`{"category":"sans-serif","variable":false}`)
		if err := os.WriteFile(filepath.Join(dir, "family.json"), meta, 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	s := NewScanner(root)
	if got := s.Families(); len(got) != families {
		tb.Fatalf("fixture catalog: got %d families, want %d", len(got), families)
	}
	return s
}

func BenchmarkFontScannerCP15(b *testing.B) {
	// Eight real compressed faces exercise discovery, optional JSON, metric
	// decoding, sorting and publication. Setup and the initial scan are untimed.
	for _, rescan := range []bool{false, true} {
		name := "CachedFamilies"
		if rescan {
			name = "Rescan8"
		}
		b.Run(name, func(b *testing.B) {
			s := cp15BenchmarkScanner(b, 8)
			b.ReportAllocs()
			for b.Loop() {
				families := s.Families()
				if rescan {
					families = s.Rescan()
				}
				if len(families) != 8 {
					b.Fatal("lost benchmark families")
				}
			}
		})
	}
}
