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

	// Mutate the attacker-controlled declared size past the live ceiling.
	// Increasing the declared size does not break Open (unlike understating it).
	f.UncompressedSize64 = uint64(maxZipEntryBytes) + 1
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
	if _, err := readLimitedZipBody("big.txt", body, limit); err == nil {
		t.Fatal("past limit: want error")
	} else if !strings.Contains(err.Error(), "exceeds decompressed size limit") {
		t.Fatalf("err = %v, want decompressed-limit message", err)
	}

	// Exactly at the limit still succeeds.
	exact := bytes.Repeat([]byte("y"), int(limit))
	got, err := readLimitedZipBody("exact.txt", io.NopCloser(bytes.NewReader(exact)), limit)
	if err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if int64(len(got)) != limit {
		t.Fatalf("len = %d, want %d", len(got), limit)
	}
}

func TestReadZipEntryHonestOversizeWithLoweredLimit(t *testing.T) {
	// Not parallel: temporarily lowers the package-wide ceiling.
	old := maxZipEntryBytes
	maxZipEntryBytes = 64
	t.Cleanup(func() { maxZipEntryBytes = old })

	// Honest header: declared size == real size > lowered limit → early reject.
	payload := bytes.Repeat([]byte("x"), 200)
	path := writeTestEPUB(t, map[string]string{"big.txt": string(payload)})
	index := openTestIndex(t, path)
	if _, err := readZipFileIndexed(index, "big.txt"); err == nil {
		t.Fatal("honest oversize: want error")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want declared-size message", err)
	}

	// Exact-at-limit entry still readable through the full path.
	exact := string(bytes.Repeat([]byte("y"), int(maxZipEntryBytes)))
	path2 := writeTestEPUB(t, map[string]string{"exact.txt": exact})
	got, err := readZipFileIndexed(openTestIndex(t, path2), "exact.txt")
	if err != nil {
		t.Fatalf("exact limit full path: %v", err)
	}
	if int64(len(got)) != maxZipEntryBytes {
		t.Fatalf("len = %d, want %d", len(got), maxZipEntryBytes)
	}
}

func openTestIndex(t *testing.T, path string) map[string]*zip.File {
	t.Helper()
	rc, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return buildIndex(&rc.Reader)
}
