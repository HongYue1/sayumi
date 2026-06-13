package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")
	lower := strings.ToLower(name)

	for _, f := range zr.File {
		fName := strings.TrimPrefix(f.Name, "/")
		if fName == name || strings.ToLower(fName) == lower {
			return readZipEntry(f)
		}
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", name)
}

func readZipFileIndexed(index map[string]*zip.File, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")

	if f, ok := index[name]; ok {
		return readZipEntry(f)
	}
	if f, ok := index[strings.ToLower(name)]; ok {
		return readZipEntry(f)
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", name)
}

// maxZipEntryBytes caps how many decompressed bytes a single zip entry may
// occupy when read fully into memory. Every in-memory read (container.xml,
// OPF, NCX/nav TOC, chapter XHTML, CSS) flows through readZipEntry, so this is
// the chokepoint that bounds decompression-bomb exposure. Streaming binary
// resources (images, fonts, audio) are served via EPUBStore.OpenResource and
// io.Copy, which never buffer the whole entry, so they are intentionally not
// subject to this text-oriented limit. 64 MiB is far larger than any sane
// single text resource yet small enough to keep a malicious entry from
// exhausting memory.
const maxZipEntryBytes = 64 << 20

func readZipEntry(f *zip.File) (data []byte, err error) {
	// Fast reject on the declared size. The zip header is attacker-controlled
	// and may understate the true size, so this is only a cheap early-out for
	// honestly-oversized entries; the LimitReader below enforces the real
	// ceiling during decompression regardless of what the header claims.
	if f.UncompressedSize64 > maxZipEntryBytes {
		return nil, fmt.Errorf("zip entry %s too large: declared %d bytes exceeds %d limit", f.Name, f.UncompressedSize64, maxZipEntryBytes)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close zip entry %s: %w", f.Name, cerr)
		}
	}()

	// Read at most maxZipEntryBytes+1: the extra byte lets us detect a stream
	// that decompressed past the limit (i.e. the header lied) versus one that
	// landed exactly at it. io.ReadAll over a LimitReader bounds the allocation
	// to the cap, independent of the untrusted UncompressedSize64.
	data, err = io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
	}
	if int64(len(data)) > maxZipEntryBytes {
		return nil, fmt.Errorf("zip entry %s exceeds decompressed size limit of %d bytes", f.Name, maxZipEntryBytes)
	}
	return data, nil
}
