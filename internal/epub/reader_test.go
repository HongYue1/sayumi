package epub

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadZipFileIndexedLookup(t *testing.T) {
	t.Parallel()

	path := writeTestEPUB(t, map[string]string{
		"OEBPS/Chapter.xhtml": "<p>hi</p>",
		"OEBPS/plain.txt":     "hello",
	})
	index := openTestIndex(t, path)

	// Exact hit.
	got, err := readZipFileIndexed(index, "OEBPS/plain.txt")
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("exact body = %q", got)
	}

	// Leading slash stripped.
	got, err = readZipFileIndexed(index, "/OEBPS/plain.txt")
	if err != nil || string(got) != "hello" {
		t.Fatalf("slash-prefixed: %q err=%v", got, err)
	}

	// Case-insensitive fallback (index stores a lower key for mixed-case names).
	got, err = readZipFileIndexed(index, "oebps/chapter.xhtml")
	if err != nil {
		t.Fatalf("case fold: %v", err)
	}
	if !strings.Contains(string(got), "hi") {
		t.Fatalf("case fold body = %q", got)
	}

	if _, err := readZipFileIndexed(index, "missing.txt"); err == nil {
		t.Fatal("missing entry: want error")
	}
}

func TestReadZipEntryDeclaredSizeTooLarge(t *testing.T) {
	t.Parallel()

	path := writeTestEPUB(t, map[string]string{"tiny.txt": "abc"})
	index := openTestIndex(t, path)
	f := index["tiny.txt"]
	if f == nil {
		t.Fatal("missing tiny.txt in index")
	}

	// Reject the untrusted declared size before trying to open/decompress it.
	// An unsupported method would produce a different error if Open ran first.
	f.UncompressedSize64 = uint64(maxZipEntryBytes) + 1
	f.Method = 99
	if _, err := readZipEntry(f); err == nil {
		t.Fatal("declared oversize: want error")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want declared-size message", err)
	}
}

func TestReadLimitedZipBodyPastLimit(t *testing.T) {
	t.Parallel()

	const limit int64 = 64
	// Stream longer than limit with no zip header involved.
	body := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), int(limit)+10)))
	if got, err := readLimitedZipBody("big.txt", body, limit); err == nil {
		t.Fatal("past limit: want error")
	} else if got != nil {
		t.Fatalf("oversized body returned data: %q", got)
	} else if !strings.Contains(err.Error(), "exceeds decompressed size limit") {
		t.Fatalf("err = %v, want decompressed-limit message", err)
	}

	// Exactly at the limit still succeeds.
	exact := bytes.Repeat([]byte("y"), int(limit))
	got, err := readLimitedZipBody("exact.txt", io.NopCloser(bytes.NewReader(exact)), limit)
	if err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if !bytes.Equal(got, exact) {
		t.Fatalf("body = %q, want %q", got, exact)
	}
}

func openTestIndex(t *testing.T, path string) map[string]*zip.File {
	t.Helper()
	rc, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	t.Cleanup(func() {
		if err := rc.Close(); err != nil {
			t.Errorf("close zip: %v", err)
		}
	})
	return buildIndex(&rc.Reader)
}
