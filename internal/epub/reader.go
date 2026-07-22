package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

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

// defaultMaxZipEntryBytes is the production ceiling for a single in-memory zip
// entry read. Streaming binary resources (images, fonts, audio) go through
// EPUBStore.OpenResource + io.Copy and are intentionally not subject to this
// text-oriented limit.
const defaultMaxZipEntryBytes int64 = 64 << 20

// maxZipEntryBytes is the live ceiling used by readZipEntry. Production leaves
// it at defaultMaxZipEntryBytes; tests may lower it temporarily so bomb-path
// coverage does not need multi-dozen-MiB fixtures.
var maxZipEntryBytes = defaultMaxZipEntryBytes

func readZipEntry(f *zip.File) ([]byte, error) {
	limit := maxZipEntryBytes
	// Fast reject on the declared size. The zip header is attacker-controlled
	// and may understate the true size, so this is only a cheap early-out for
	// honestly-oversized entries; readLimitedZipBody enforces the real ceiling
	// during decompression regardless of what the header claims.
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("zip entry %s too large: declared %d bytes exceeds %d limit", f.Name, f.UncompressedSize64, limit)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	return readLimitedZipBody(f.Name, rc, limit)
}

// readLimitedZipBody drains rc into memory, rejecting streams longer than limit.
// rc is always closed. Extracted so tests can exercise the LimitReader path with
// a plain reader (mutating zip.File.UncompressedSize64 breaks Open).
func readLimitedZipBody(name string, rc io.ReadCloser, limit int64) (data []byte, err error) {
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close zip entry %s: %w", name, cerr)
		}
	}()

	// Read at most limit+1: the extra byte lets us detect a stream that
	// decompressed past the limit (i.e. the header lied) versus one that landed
	// exactly at it. io.ReadAll over a LimitReader bounds the allocation to the
	// cap, independent of the untrusted UncompressedSize64.
	data, err = io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip entry %s exceeds decompressed size limit of %d bytes", name, limit)
	}
	return data, nil
}
