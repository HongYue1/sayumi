package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverResponseETag(t *testing.T) {
	t.Parallel()

	if got := coverResponseETag("", "2020-01-01"); got != "" {
		t.Fatalf("empty hash = %q", got)
	}
	got := coverResponseETag("abc", "t1")
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("not quoted: %q", got)
	}
	if !strings.Contains(got, "abc") || !strings.Contains(got, "t1") || !strings.Contains(got, "cover") {
		t.Fatalf("missing parts: %q", got)
	}
	if coverResponseETag("abc", "t1") == coverResponseETag("abc", "t2") {
		t.Fatal("updatedAt must change etag")
	}
	if coverResponseETag("abc", "t1") == coverResponseETag("xyz", "t1") {
		t.Fatal("hash must change etag")
	}
}

func TestBookDetailETag(t *testing.T) {
	t.Parallel()

	if got := bookDetailETag("", "u", "r"); got != "" {
		t.Fatalf("empty hash = %q", got)
	}
	a := bookDetailETag("h1", "u1", "r1")
	b := bookDetailETag("h1", "u1", "r1")
	if a != b || a == "" {
		t.Fatalf("stable etag failed: %q %q", a, b)
	}
	if !strings.Contains(a, bookDetailVersion) {
		t.Fatalf("missing version: %q", a)
	}
	if bookDetailETag("h1", "u1", "r1") == bookDetailETag("h1", "u2", "r1") {
		t.Fatal("bookUpdatedAt must change etag")
	}
	if bookDetailETag("h1", "u1", "r1") == bookDetailETag("h1", "u1", "r2") {
		t.Fatal("lastReadAt must change etag")
	}
	if bookDetailETag("h1", "u1", "") == bookDetailETag("h1", "u1", "r1") {
		t.Fatal("empty vs set lastReadAt must differ")
	}
}

func TestRemoveManagedLibraryFile(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	// Absolute path inside library: removed.
	inside := filepath.Join(lib, "book.epub")
	if err := os.WriteFile(inside, []byte("epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, inside, "book")
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("inside abs file should be removed: %v", err)
	}

	// Absolute path outside library: refused.
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "escape.epub")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, outside, "book")
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("escape path must survive: %v", err)
	}

	// Relative path under library (cover-style): removed via os.Root.
	relDir := filepath.Join(lib, ".sayumi", "covers")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(".sayumi", "covers", "c1.jpg")
	absRel := filepath.Join(lib, rel)
	if err := os.WriteFile(absRel, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeManagedLibraryFile(lib, rel, "cover")
	if _, err := os.Stat(absRel); !os.IsNotExist(err) {
		t.Fatalf("relative cover should be removed: %v", err)
	}

	// Empty path: no-op.
	removeManagedLibraryFile(lib, "", "book")

	// Missing file: no panic / no error surface.
	removeManagedLibraryFile(lib, filepath.Join(lib, "gone.epub"), "book")
	removeManagedLibraryFile(lib, filepath.Join(".sayumi", "covers", "missing.jpg"), "cover")
}
