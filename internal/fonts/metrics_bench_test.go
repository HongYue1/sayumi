package fonts

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/andybalholm/brotli"
)

// CP14's harness is frozen before changing production code. Its constructors
// do not call the parser or other test helpers: both executables must receive
// identical bytes even if regression fixtures subsequently change. Synthetic
// glyph bytes exercise container work, not outline reconstruction; embedded
// WOFF2 faces cover real compression and transformed tables separately.
func BenchmarkReadMetricsCP14(b *testing.B) {
	minimal := cp14BenchTables(0)
	large := cp14BenchTables(256 << 10)
	fixtures := [7]struct {
		name string
		data []byte
	}{
		{"SFNTMinimal", cp14BenchSFNT(minimal)},
		{"SFNT256K", cp14BenchSFNT(large)},
		{"WOFF1Raw256K", cp14BenchWOFF1(b, large, false)},
		{"WOFF1Zlib256K", cp14BenchWOFF1(b, large, true)},
		{"WOFF2Synthetic256K", cp14BenchWOFF2(b, large)},
	}
	names := make([]string, 0, len(fontData))
	for name := range fontData {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		b.Fatal("embedded faces are required")
	}
	smallest, largest := names[0], names[0]
	for _, name := range names[1:] {
		if len(fontData[name]) < len(fontData[smallest]) {
			smallest = name
		}
		if len(fontData[name]) > len(fontData[largest]) {
			largest = name
		}
	}
	for i, name := range []string{smallest, largest} {
		fixtures[5+i].name = "WOFF2Embedded/" + name
		fixtures[5+i].data = fontData[name]
	}
	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			b.Logf("input_bytes=%d sha256=%x", len(fixture.data), sha256.Sum256(fixture.data))
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.data)))
			for b.Loop() {
				metrics, err := ReadMetrics(fixture.data)
				if err != nil || metrics.UnitsPerEm == 0 {
					b.Fatalf("ReadMetrics: %+v, %v", metrics, err)
				}
			}
		})
	}
}

type cp14BenchTable struct {
	tag  string
	body []byte
}

func cp14BenchTables(glyphBytes int) []cp14BenchTable {
	head, hhea, os2 := make([]byte, 54), make([]byte, 36), make([]byte, 96)
	binary.BigEndian.PutUint16(head[18:], 1000)
	binary.BigEndian.PutUint16(hhea[4:], 800)
	binary.BigEndian.PutUint16(hhea[6:], 65536-200)
	binary.BigEndian.PutUint16(hhea[8:], 100)
	binary.BigEndian.PutUint16(os2, 4)
	binary.BigEndian.PutUint16(os2[86:], 500)
	binary.BigEndian.PutUint16(os2[88:], 700)
	tables := []cp14BenchTable{{"OS/2", os2}}
	if glyphBytes != 0 {
		glyphs := make([]byte, glyphBytes)
		for i := range glyphs {
			glyphs[i] = byte(i*31 + (i >> 8))
		}
		tables = append(tables, cp14BenchTable{"glyf", glyphs})
	}
	return append(tables, cp14BenchTable{"head", head}, cp14BenchTable{"hhea", hhea})
}

func cp14BenchSFNT(tables []cp14BenchTable) []byte {
	data := make([]byte, 0, 12+16*len(tables))
	data = append(data, make([]byte, 12+16*len(tables))...)
	binary.BigEndian.PutUint32(data, 0x00010000)
	binary.BigEndian.PutUint16(data[4:], uint16(len(tables)))
	for i, table := range tables {
		record := data[12+16*i:]
		copy(record, table.tag)
		binary.BigEndian.PutUint32(record[8:], uint32(len(data)))
		binary.BigEndian.PutUint32(record[12:], uint32(len(table.body)))
		data = append(data, table.body...)
	}
	return data
}

func cp14BenchWOFF1(b *testing.B, tables []cp14BenchTable, compress bool) []byte {
	b.Helper()
	data := make([]byte, 0, 44+20*len(tables))
	data = append(data, make([]byte, 44+20*len(tables))...)
	copy(data, "wOFF")
	binary.BigEndian.PutUint32(data[4:], 0x00010000)
	binary.BigEndian.PutUint16(data[12:], uint16(len(tables)))
	for i, table := range tables {
		stored := table.body
		if compress {
			var out bytes.Buffer
			writer := zlib.NewWriter(&out)
			if _, err := writer.Write(table.body); err != nil {
				b.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				b.Fatal(err)
			}
			if out.Len() < len(stored) {
				stored = out.Bytes()
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

func cp14BenchBase128(dst []byte, value uint32) []byte {
	var encoded [5]byte
	at := len(encoded) - 1
	encoded[at] = byte(value & 127)
	for value >>= 7; value != 0; value >>= 7 {
		at--
		encoded[at] = byte(value&127) | 128
	}
	return append(dst, encoded[at:]...)
}

func cp14BenchWOFF2(b *testing.B, tables []cp14BenchTable) []byte {
	b.Helper()
	data := make([]byte, 0, 48)
	data = append(data, make([]byte, 48)...)
	copy(data, "wOF2")
	binary.BigEndian.PutUint32(data[4:], 0x00010000)
	binary.BigEndian.PutUint16(data[12:], uint16(len(tables)))
	var body bytes.Buffer
	for _, table := range tables {
		flags := byte(63) // explicit four-byte tag
		if table.tag == "glyf" {
			flags |= 3 << 6 // untransformed glyph bytes
		}
		data = append(data, flags)
		data = append(data, table.tag...)
		data = cp14BenchBase128(data, uint32(len(table.body)))
		body.Write(table.body)
	}
	var compressed bytes.Buffer
	writer := brotli.NewWriterLevel(&compressed, 4)
	if _, err := writer.Write(body.Bytes()); err != nil {
		b.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	binary.BigEndian.PutUint32(data[20:], uint32(compressed.Len()))
	data = append(data, compressed.Bytes()...)
	binary.BigEndian.PutUint32(data[8:], uint32(len(data)))
	return data
}
