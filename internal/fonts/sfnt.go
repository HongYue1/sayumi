package fonts

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/andybalholm/brotli"
)

// This file opens the four font containers the scanner accepts and hands back
// the few tables that carry vertical metrics. It deliberately stops there: no
// glyph outlines, no character map, no shaping data.
//
// That limit is what keeps .woff2 cheap. A .woff2 is not simply a
// brotli-compressed font: the whole table set is one brotli stream, and
// glyf/loca are additionally stored in a transformed form that has to be
// reversed before outlines can be read. head, hhea and OS/2 are never
// transformed, so the transformed tables are measured to find where the wanted
// ones begin and are then skipped, and the reversal is never implemented.

// Tags of the tables this package reads.
const (
	tagHead = "head"
	tagHhea = "hhea"
	tagOS2  = "OS/2"
)

// Container signatures, taken from the first four bytes of the file.
const (
	sigWOFF2 = "wOF2"
	sigWOFF1 = "wOFF"
	sigOTTO  = "OTTO" // sfnt with CFF outlines
	sigTrue  = "true" // sfnt, older Apple spelling
	sigTTCF  = "ttcf" // font collection: several faces in one file

	// sfntVersion1 is the numeric signature of a plain TrueType sfnt, the one
	// container whose first four bytes are not printable.
	sfntVersion1 = 0x00010000
)

var (
	// errUnsupportedFont is a container this package does not read. The caller
	// is expected to carry on without metrics for that face.
	errUnsupportedFont = errors.New("fonts: unsupported font container")

	// errMalformedFont is a container whose own offsets and lengths disagree
	// with its size. Font files here are user-supplied, so every bound coming
	// out of the file is checked against the bytes actually present.
	errMalformedFont = errors.New("fonts: malformed font")
)

// maxFontTables caps how many bytes of table data one face may expand to. Real
// text faces land far below this; the cap exists so a hostile or truncated file
// cannot ask for an unbounded allocation from a length field.
const maxFontTables = 24 << 20

// readTables opens a font container and returns its tables keyed by tag. The
// returned slices alias data, or one decompressed buffer, so callers must treat
// them as read-only.
func readTables(data []byte) (map[string][]byte, error) {
	if len(data) < sfntHeaderSize {
		return nil, fmt.Errorf("%w: %d bytes is shorter than any font header", errMalformedFont, len(data))
	}

	switch string(data[:4]) {
	case sigWOFF2:
		return woff2Tables(data)
	case sigWOFF1:
		return woff1Tables(data)
	case sigOTTO, sigTrue:
		return sfntTables(data)
	case sigTTCF:
		return nil, fmt.Errorf("%w: a collection holds several faces, so there is no single set of metrics", errUnsupportedFont)
	}
	if binary.BigEndian.Uint32(data[:4]) == sfntVersion1 {
		return sfntTables(data)
	}
	return nil, fmt.Errorf("%w: unrecognized signature %q", errUnsupportedFont, data[:4])
}

const (
	sfntHeaderSize = 12
	sfntEntrySize  = 16
)

// sfntTables reads an uncompressed .ttf/.otf table directory.
func sfntTables(data []byte) (map[string][]byte, error) {
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	if sfntHeaderSize+numTables*sfntEntrySize > len(data) {
		return nil, fmt.Errorf("%w: %d table records do not fit in %d bytes", errMalformedFont, numTables, len(data))
	}

	tables := make(map[string][]byte, numTables)
	for i := range numTables {
		record := data[sfntHeaderSize+i*sfntEntrySize:]
		tag := string(record[:4])
		offset := int64(binary.BigEndian.Uint32(record[8:12]))
		length := int64(binary.BigEndian.Uint32(record[12:16]))
		if offset+length > int64(len(data)) {
			return nil, fmt.Errorf("%w: table %q runs past the end of the file", errMalformedFont, tag)
		}
		tables[tag] = data[offset : offset+length]
	}
	return tables, nil
}

const (
	woff1HeaderSize = 44
	woff1EntrySize  = 20
)

// woff1Tables reads a .woff, which compresses each table separately with zlib.
func woff1Tables(data []byte) (map[string][]byte, error) {
	if len(data) < woff1HeaderSize {
		return nil, fmt.Errorf("%w: woff header is %d bytes", errMalformedFont, len(data))
	}
	numTables := int(binary.BigEndian.Uint16(data[12:14]))
	if woff1HeaderSize+numTables*woff1EntrySize > len(data) {
		return nil, fmt.Errorf("%w: %d woff table records do not fit in %d bytes", errMalformedFont, numTables, len(data))
	}

	tables := make(map[string][]byte, numTables)
	for i := range numTables {
		record := data[woff1HeaderSize+i*woff1EntrySize:]
		tag := string(record[:4])
		offset := int64(binary.BigEndian.Uint32(record[4:8]))
		storedLength := int64(binary.BigEndian.Uint32(record[8:12]))
		origLength := int64(binary.BigEndian.Uint32(record[12:16]))
		if offset+storedLength > int64(len(data)) {
			return nil, fmt.Errorf("%w: woff table %q runs past the end of the file", errMalformedFont, tag)
		}
		stored := data[offset : offset+storedLength]

		// A table that would not shrink is stored verbatim, which the format
		// signals by making the stored length equal to the original length.
		if storedLength >= origLength {
			tables[tag] = stored
			continue
		}
		if origLength > maxFontTables {
			return nil, fmt.Errorf("%w: woff table %q claims %d bytes", errMalformedFont, tag, origLength)
		}
		expanded, err := inflate(stored, origLength)
		if err != nil {
			return nil, fmt.Errorf("fonts: woff table %q: %w", tag, err)
		}
		tables[tag] = expanded
	}
	return tables, nil
}

// inflate zlib-expands compressed into exactly size bytes. Reading a fixed
// length bounds the allocation and rejects a table that expands to less than
// its directory entry promised.
func inflate(compressed []byte, size int64) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("zlib header: %w", err)
	}
	defer func() { _ = reader.Close() }()

	expanded := make([]byte, size)
	if _, err := io.ReadFull(reader, expanded); err != nil {
		return nil, fmt.Errorf("zlib body: %w", err)
	}
	return expanded, nil
}

const (
	woff2HeaderSize = 48

	// woff2ArbitraryTag is the table-index value meaning the four-byte tag is
	// written out inline instead of naming one of the known tables.
	woff2ArbitraryTag = 63

	// woff2TagIndexMask and woff2TransformShift split a table record's flags
	// byte into its known-table index (low six bits) and transform version
	// (high two bits).
	woff2TagIndexMask   = 0x3f
	woff2TransformShift = 6
)

// woff2Tables reads a .woff2, whose tables share a single brotli stream.
func woff2Tables(data []byte) (map[string][]byte, error) {
	if len(data) < woff2HeaderSize {
		return nil, fmt.Errorf("%w: woff2 header is %d bytes", errMalformedFont, len(data))
	}
	numTables := int(binary.BigEndian.Uint16(data[12:14]))
	if numTables == 0 {
		return nil, fmt.Errorf("%w: woff2 declares no tables", errMalformedFont)
	}
	compressedSize := int64(binary.BigEndian.Uint32(data[20:24]))

	// The directory is uncompressed but carries only lengths: the table bodies
	// follow one another inside the brotli stream, in directory order. So the
	// walk below turns each record into a span of the decompressed buffer.
	type span struct {
		tag    string
		at     int64
		length int64
	}
	spans := make([]span, 0, numTables)

	pos := woff2HeaderSize
	var total int64
	for range numTables {
		if pos >= len(data) {
			return nil, fmt.Errorf("%w: woff2 directory runs past the end of the file", errMalformedFont)
		}
		flags := data[pos]
		pos++

		var tag string
		if index := flags & woff2TagIndexMask; index == woff2ArbitraryTag {
			if pos+4 > len(data) {
				return nil, fmt.Errorf("%w: woff2 record ends mid-tag", errMalformedFont)
			}
			tag = string(data[pos : pos+4])
			pos += 4
		} else {
			tag = woff2KnownTags[index]
		}

		origLength, read, err := uintBase128(data[pos:])
		if err != nil {
			return nil, fmt.Errorf("fonts: woff2 table %q length: %w", tag, err)
		}
		pos += read

		// A transformed table occupies its transformed length in the stream, not
		// its original length. Getting this wrong shifts every later table.
		length := origLength
		if woff2Transformed(tag, flags>>woff2TransformShift) {
			transformLength, readTransform, err := uintBase128(data[pos:])
			if err != nil {
				return nil, fmt.Errorf("fonts: woff2 table %q transformed length: %w", tag, err)
			}
			pos += readTransform
			length = transformLength
		}

		spans = append(spans, span{tag: tag, at: total, length: length})
		total += length
		if total > maxFontTables {
			return nil, fmt.Errorf("%w: woff2 tables claim more than %d bytes", errMalformedFont, maxFontTables)
		}
	}

	if int64(pos)+compressedSize > int64(len(data)) {
		return nil, fmt.Errorf("%w: woff2 brotli stream runs past the end of the file", errMalformedFont)
	}
	stream := data[pos : int64(pos)+compressedSize]

	// Reading exactly the promised total bounds the allocation by the
	// directory's own arithmetic and rejects a stream that expands to less.
	expanded := make([]byte, total)
	if _, err := io.ReadFull(brotli.NewReader(bytes.NewReader(stream)), expanded); err != nil {
		return nil, fmt.Errorf("fonts: woff2 brotli stream: %w", err)
	}

	tables := make(map[string][]byte, numTables)
	for _, s := range spans {
		tables[s.tag] = expanded[s.at : s.at+s.length]
	}
	return tables, nil
}

// woff2Transformed reports whether a table is stored in transformed form, which
// is what decides if its record carries a second length.
//
// glyf and loca are transformed by default and opt out with version 3; every
// other table is the other way round, untransformed at version 0.
func woff2Transformed(tag string, transformVersion byte) bool {
	if tag == "glyf" || tag == "loca" {
		return transformVersion != 3
	}
	return transformVersion != 0
}

// uintBase128 reads a WOFF2 variable-length integer: seven bits per byte, most
// significant byte first, with the high bit set on every byte but the last.
// Returns the value and how many bytes it consumed.
func uintBase128(b []byte) (int64, int, error) {
	var value uint32
	// Five bytes is the most that can carry 32 bits, so a sixth means the
	// encoding is wrong rather than merely large.
	for i := range 5 {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("%w: truncated base-128 integer", errMalformedFont)
		}
		digit := b[i]

		// A leading 0x80 encodes a redundant high zero. The format forbids it so
		// that each value has exactly one encoding.
		if i == 0 && digit == 0x80 {
			return 0, 0, fmt.Errorf("%w: base-128 integer has a leading zero", errMalformedFont)
		}
		if value > (1<<25)-1 {
			return 0, 0, fmt.Errorf("%w: base-128 integer is wider than 32 bits", errMalformedFont)
		}
		value = value<<7 | uint32(digit&0x7f)
		if digit&0x80 == 0 {
			return int64(value), i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("%w: base-128 integer is longer than five bytes", errMalformedFont)
}

// woff2KnownTags is the fixed table-tag list a WOFF2 record indexes into
// instead of spelling the tag out. Only indexes 1, 2 and 6 (head, hhea, OS/2)
// are read here; the rest have to be present and in order so that each table's
// position in the brotli stream comes out right.
var woff2KnownTags = [63]string{
	"cmap", "head", "hhea", "hmtx", "maxp", "name", "OS/2", "post",
	"cvt ", "fpgm", "glyf", "loca", "prep", "CFF ", "VORG", "EBDT",
	"EBLC", "gasp", "hdmx", "kern", "LTSH", "PCLT", "VDMX", "vhea",
	"vmtx", "BASE", "GDEF", "GPOS", "GSUB", "EBSC", "JSTF", "MATH",
	"CBDT", "CBLC", "COLR", "CPAL", "SVG ", "sbix", "acnt", "avar",
	"bdat", "bloc", "bsln", "cvar", "fdsc", "feat", "fmtx", "fvar",
	"gvar", "hsty", "just", "lcar", "mort", "morx", "opbd", "prop",
	"trak", "Zapf", "Silf", "Glat", "Gloc", "Feat", "Sill",
}
