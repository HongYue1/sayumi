package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"math"
)

// maxZipEntryBytes is the ceiling for a single in-memory ZIP entry read.
// Streaming binary resources (images, fonts, audio) use EPUBStore.OpenResource
// and are intentionally not subject to this text-oriented limit.
const maxZipEntryBytes int64 = 64 << 20

func readZipFileIndexed(index map[string]*zip.File, name string) ([]byte, error) {
	f, err := lookupInIndex(index, name)
	if err != nil {
		return nil, err
	}
	return readZipEntry(f)
}

func readZipEntry(f *zip.File) ([]byte, error) {
	// The header is untrusted: reject oversized declarations before opening,
	// but also enforce the ceiling while reading, independent of that header.
	if f.UncompressedSize64 > uint64(maxZipEntryBytes) {
		return nil, fmt.Errorf(
			"zip entry %s too large: declared %d bytes exceeds %d limit",
			f.Name,
			f.UncompressedSize64,
			maxZipEntryBytes,
		)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	return readLimitedZipBody(f.Name, rc, maxZipEntryBytes)
}

// readLimitedZipBody reads at most limit+1 bytes and always closes rc.
// It returns no data on failure, preferring read/limit errors over close errors.
// The explicit limit also allows small fixtures without mutable package state.
func readLimitedZipBody(name string, rc io.ReadCloser, limit int64) (data []byte, err error) {
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			data = nil
			err = fmt.Errorf("close zip entry %s: %w", name, cerr)
		}
	}()

	if limit < 0 || limit == math.MaxInt64 {
		return nil, fmt.Errorf("invalid zip entry size limit for %s: %d", name, limit)
	}

	// One extra byte distinguishes an oversized stream from an exact fit.
	// LimitReader bounds bytes consumed, not io.ReadAll's backing capacity.
	data, err = io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip entry %s exceeds decompressed size limit of %d bytes", name, limit)
	}
	return data, nil
}
