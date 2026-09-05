package fonts

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/andybalholm/brotli"
)

// These fixtures describe only the tables the metrics reader consumes, not
// renderable outlines. Keep wire construction separate from the decoder so a
// decoder bug cannot silently redefine what the test considers well-formed.
type fontTestTable struct {
	tag         string
	body        []byte
	version     byte
	transformed bool
	original    uint32
}

func fontTestMetricsTables() []fontTestTable {
	return []fontTestTable{
		{tag: "OS/2", body: craftOS2(4, 800, -200, 100, 500, 700)},
		{tag: "head", body: craftHead(1000)},
		{tag: "hhea", body: craftHhea(800, -200, 100)},
	}
}

func fontTestZlib(tb testing.TB, body []byte) []byte {
	tb.Helper()
	var out bytes.Buffer
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(body); err != nil {
		tb.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		tb.Fatal(err)
	}
	return out.Bytes()
}

func fontTestWOFF1(tb testing.TB, tables []fontTestTable, compress bool) []byte {
	tb.Helper()
	data := make([]byte, 0, 44+20*len(tables))
	data = append(data, make([]byte, 44+20*len(tables))...)
	copy(data, "wOFF")
	binary.BigEndian.PutUint32(data[4:], 0x00010000)
	binary.BigEndian.PutUint16(data[12:], uint16(len(tables)))
	for i, table := range tables {
		stored := table.body
		if compress {
			if compressed := fontTestZlib(tb, stored); len(compressed) < len(stored) {
				stored = compressed
			}
		}
		record := data[44+20*i:]
		copy(record, table.tag)
		binary.BigEndian.PutUint32(record[4:], uint32(len(data)))
		binary.BigEndian.PutUint32(record[8:], uint32(len(stored)))
		binary.BigEndian.PutUint32(record[12:], uint32(len(table.body)))
		data = append(data, stored...)
	}
	binary.BigEndian.PutUint32(data[8:], uint32(len(data)))
	return data
}

func fontTestBase128(dst []byte, value uint32) []byte {
	var encoded [5]byte
	at := len(encoded) - 1
	encoded[at] = byte(value & 127)
	for value >>= 7; value != 0; value >>= 7 {
		at--
		encoded[at] = byte(value&127) | 128
	}
	return append(dst, encoded[at:]...)
}

func fontTestWOFF2(tb testing.TB, tables []fontTestTable, extra []byte) []byte {
	tb.Helper()
	data := make([]byte, 0, 48)
	data = append(data, make([]byte, 48)...)
	copy(data, "wOF2")
	binary.BigEndian.PutUint32(data[4:], 0x00010000)
	binary.BigEndian.PutUint16(data[12:], uint16(len(tables)))
	var body bytes.Buffer
	for _, table := range tables {
		data = append(data, 63|table.version<<6)
		data = append(data, table.tag...)
		length := uint32(len(table.body))
		if table.original != 0 {
			length = table.original
		}
		data = fontTestBase128(data, length)
		if table.transformed {
			data = fontTestBase128(data, uint32(len(table.body)))
		}
		body.Write(table.body)
	}
	body.Write(extra)
	var compressed bytes.Buffer
	writer := brotli.NewWriterLevel(&compressed, 4)
	if _, err := writer.Write(body.Bytes()); err != nil {
		tb.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		tb.Fatal(err)
	}
	binary.BigEndian.PutUint32(data[20:], uint32(compressed.Len()))
	data = append(data, compressed.Bytes()...)
	binary.BigEndian.PutUint32(data[8:], uint32(len(data)))
	return data
}

func TestFontContainerMetrics(t *testing.T) {
	t.Parallel()
	want := Metrics{UnitsPerEm: 1000, XHeight: 0.5, CapHeight: 0.7, Ascent: 0.8, Descent: 0.2, LineGap: 0.1}
	tables := fontTestMetricsTables()
	for name, data := range map[string][]byte{
		"sfnt":       craftSFNT(tables[1].body, tables[2].body, tables[0].body),
		"woff raw":   fontTestWOFF1(t, tables, false),
		"woff zlib":  fontTestWOFF1(t, tables, true),
		"woff2":      fontTestWOFF2(t, tables, nil),
		"woff2 glyf": fontTestWOFF2(t, append([]fontTestTable{{tag: "glyf", body: []byte{1, 2, 3}, transformed: true, original: 100}, {tag: "loca", transformed: true, original: 8}}, tables...), nil),
		"woff2 hmtx": fontTestWOFF2(t, append([]fontTestTable{{tag: "hmtx", version: 1, body: []byte{1, 2}, transformed: true, original: 100}}, tables...), nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ReadMetrics(data)
			if err != nil || got != want {
				t.Fatalf("ReadMetrics = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// ReadFull can return success after filling the caller's buffer even when the
// same decoder read found a bad trailer. A directory length is not an EOF.
func TestFontCompressedDataExact(t *testing.T) {
	t.Parallel()
	tables := fontTestMetricsTables()
	badChecksum := fontTestWOFF1(t, tables, true)
	first := badChecksum[44:]
	at := binary.BigEndian.Uint32(first[4:])
	stored := binary.BigEndian.Uint32(first[8:])
	badChecksum[at+stored-1] ^= 0xff
	tooLong := fontTestWOFF1(t, tables, true)
	binary.BigEndian.PutUint32(tooLong[44+12:], uint32(len(tables[0].body)-1))
	tooShort := fontTestWOFF1(t, tables, true)
	binary.BigEndian.PutUint32(tooShort[44+12:], uint32(len(tables[0].body)+1))
	truncated := fontTestWOFF2(t, tables, nil)
	truncated = truncated[:len(truncated)-1]
	binary.BigEndian.PutUint32(truncated[8:], uint32(len(truncated)))
	binary.BigEndian.PutUint32(truncated[20:], binary.BigEndian.Uint32(truncated[20:])-1)
	for name, data := range map[string][]byte{
		"woff checksum": badChecksum,
		"woff excess":   tooLong,
		"woff short":    tooShort,
		"woff2 excess":  fontTestWOFF2(t, tables, []byte{0x42}),
		"woff2 short":   truncated,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got, err := ReadMetrics(data); err == nil {
				t.Fatalf("accepted damaged compressed data: %+v", got)
			}
		})
	}
}

func TestWOFF1StoredLength(t *testing.T) {
	t.Parallel()
	data := fontTestWOFF1(t, fontTestMetricsTables(), false)
	// storedLength > origLength is not the format's verbatim-storage marker.
	binary.BigEndian.PutUint32(data[44+12:], 95)
	if _, err := ReadMetrics(data); !errors.Is(err, errMalformedFont) {
		t.Fatalf("stored length greater than original: %v", err)
	}
}

// The limit is per face, not per table. Two compressed entries just over half
// the limit previously bypassed the budget while each looked safe on its own.
func TestWOFF1ExpansionBudget(t *testing.T) {
	body := make([]byte, maxFontTables/2+1)
	for _, extra := range []int{0, 1} {
		t.Run(string(rune('0'+extra)), func(t *testing.T) {
			tables := []fontTestTable{{tag: "one ", body: body[:maxFontTables/2]}, {tag: "two ", body: body[:maxFontTables/2+extra]}}
			data := fontTestWOFF1(t, tables, true)
			_, err := readTables(data)
			if extra == 0 && err != nil {
				t.Fatalf("exact budget rejected: %v", err)
			}
			if extra != 0 && !errors.Is(err, errMalformedFont) {
				t.Fatalf("over-budget face not rejected: %v", err)
			}
		})
	}
}

func TestFontDuplicateTables(t *testing.T) {
	t.Parallel()
	tables := fontTestMetricsTables()
	duplicate := append(fontTestMetricsTables(), fontTestTable{tag: "head", body: craftHead(2048)})
	sfnt := craftSFNT(tables[1].body, tables[2].body, tables[0].body)
	copy(sfnt[12:], "head")
	for name, data := range map[string][]byte{
		"sfnt":  sfnt,
		"woff":  fontTestWOFF1(t, duplicate, false),
		"woff2": fontTestWOFF2(t, duplicate, nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ReadMetrics(data); !errors.Is(err, errMalformedFont) {
				t.Fatalf("ambiguous duplicate tables: %v", err)
			}
		})
	}
}

func TestWOFF2UnsupportedTransforms(t *testing.T) {
	t.Parallel()
	for _, table := range []fontTestTable{
		{tag: "head", version: 1, transformed: true, body: craftHead(1000)},
		{tag: "hhea", version: 2, transformed: true, body: craftHhea(800, -200, 0)},
		{tag: "OS/2", version: 3, transformed: true, body: craftOS2(4, 800, -200, 0, 500, 700)},
		{tag: "glyf", version: 1, transformed: true, body: []byte{1}},
		{tag: "hmtx", version: 2, transformed: true, body: []byte{1}},
		{tag: "name", version: 1, transformed: true, body: []byte{1}},
	} {
		t.Run(table.tag, func(t *testing.T) {
			t.Parallel()
			tables := fontTestMetricsTables()
			found := false
			for i := range tables {
				if tables[i].tag == table.tag {
					tables[i] = table
					found = true
				}
			}
			if !found {
				tables = append(tables, table)
			}
			if _, err := ReadMetrics(fontTestWOFF2(t, tables, nil)); !errors.Is(err, errUnsupportedFont) {
				t.Fatalf("reserved transform interpreted as ordinary table: %v", err)
			}
		})
	}
}

func TestWOFF2CollectionAndLoca(t *testing.T) {
	t.Parallel()
	collection := fontTestWOFF2(t, fontTestMetricsTables(), nil)
	copy(collection[4:], "ttcf")
	if _, err := ReadMetrics(collection); !errors.Is(err, errUnsupportedFont) {
		t.Errorf("collection flavor accepted as one face: %v", err)
	}
	tables := append([]fontTestTable{{tag: "loca", body: []byte{1}, transformed: true, original: 8}}, fontTestMetricsTables()...)
	if _, err := ReadMetrics(fontTestWOFF2(t, tables, nil)); !errors.Is(err, errMalformedFont) {
		t.Errorf("transformed loca with nonzero length: %v", err)
	}
}

func FuzzReadMetricsContainers(f *testing.F) {
	tables := fontTestMetricsTables()
	f.Add(craftSFNT(tables[1].body, tables[2].body, tables[0].body))
	f.Add(fontTestWOFF1(f, tables, true))
	f.Add(fontTestWOFF2(f, tables, nil))
	f.Add(fontTestWOFF2(f, tables, []byte{1}))
	f.Add([]byte("wOF2"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip() // Bound corpus I/O; production's expansion budget is separate.
		}
		before := bytes.Clone(data)
		first, firstErr := ReadMetrics(data)
		second, secondErr := ReadMetrics(data)
		if !bytes.Equal(data, before) {
			t.Fatal("metrics parsing modified the borrowed input")
		}
		if (firstErr == nil) != (secondErr == nil) || first != second {
			t.Fatalf("nondeterministic parse: %+v/%v versus %+v/%v", first, firstErr, second, secondErr)
		}
		if firstErr == nil && first.UnitsPerEm <= 0 {
			t.Fatalf("successful metrics have no em: %+v", first)
		}
	})
}
