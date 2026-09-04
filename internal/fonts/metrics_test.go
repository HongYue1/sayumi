package fonts

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The embedded faces are the fixtures for this package: they are real .woff2
// files, so parsing them exercises the brotli path end to end without shipping
// test data. One of them is also the family every other family is normalized
// against, so a silent failure here would mis-size the whole reader.
func TestReadMetricsEmbeddedFaces(t *testing.T) {
	t.Parallel()

	if len(fontData) == 0 {
		t.Fatal("no embedded fonts to measure")
	}

	for name, data := range fontData {
		metrics, err := ReadMetrics(data)
		if err != nil {
			t.Errorf("ReadMetrics(%s): %v", name, err)
			continue
		}
		// Logged rather than only asserted: when a band below fails, the whole
		// set is what tells a misread field from a genuinely unusual face.
		t.Logf("%s: unitsPerEm %d, x-height %.4f, cap-height %.4f, ascent %.4f, descent %.4f, line-gap %.4f",
			name, metrics.UnitsPerEm, metrics.XHeight, metrics.CapHeight, metrics.Ascent, metrics.Descent, metrics.LineGap)

		if !metrics.Normalizable() {
			t.Errorf("%s: not normalizable: %+v", name, metrics)
			continue
		}

		// Bands wide enough for any text face but narrow enough to catch a
		// misread field, which shows up as a wildly wrong ratio rather than a
		// slightly wrong one.
		if metrics.UnitsPerEm < 16 || metrics.UnitsPerEm > 16384 {
			t.Errorf("%s: unitsPerEm = %d", name, metrics.UnitsPerEm)
		}
		if metrics.XHeight < 0.3 || metrics.XHeight > 0.7 {
			t.Errorf("%s: xHeight = %.4f, outside 0.3-0.7 of the em", name, metrics.XHeight)
		}
		if metrics.CapHeight <= metrics.XHeight || metrics.CapHeight > 1 {
			t.Errorf("%s: capHeight = %.4f, want above xHeight %.4f and within the em", name, metrics.CapHeight, metrics.XHeight)
		}
		if metrics.Ascent <= 0 || metrics.Descent <= 0 {
			t.Errorf("%s: ascent = %.4f, descent = %.4f, want both positive", name, metrics.Ascent, metrics.Descent)
		}
		if metrics.LineGap < 0 {
			t.Errorf("%s: lineGap = %.4f", name, metrics.LineGap)
		}
	}
}

// The embedded-metrics map is how a font-face rule reaches the reference
// family's numbers, so it has to answer for a served filename and refuse
// anything else.
func TestEmbeddedMetricsLookup(t *testing.T) {
	t.Parallel()

	var reference string
	for name := range fontData {
		if strings.HasPrefix(name, "Literata-VariableFont") {
			reference = name
			break
		}
	}
	if reference == "" {
		t.Skip("the reference face is not embedded in this build")
	}

	metrics, found := EmbeddedFaceMetrics()[reference]
	if !found {
		t.Fatalf("EmbeddedFaceMetrics()[%q] not found", reference)
	}
	if !metrics.Normalizable() {
		t.Fatalf("reference face %q is not normalizable: %+v", reference, metrics)
	}

	if _, found := EmbeddedFaceMetrics()["NoSuchFace.woff2"]; found {
		t.Error("EmbeddedFaceMetrics found a face that is not embedded")
	}
}

// Font files arrive from the user's own disk, so every rejection path has to be
// an error rather than a panic or a zero-valued set of metrics.
func TestReadMetricsRejectsBadInput(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                 nil,
		"shorter than a header": []byte("wOF"),
		"unknown signature":     []byte("XXXXXXXXXXXX"),
		"font collection":       []byte("ttcfXXXXXXXX"),
		"truncated woff2":       append([]byte("wOF2"), make([]byte, 16)...),
		"woff2 with no tables":  append([]byte("wOF2"), make([]byte, 60)...),
		"truncated woff":        append([]byte("wOFF"), make([]byte, 16)...),
		"sfnt with a bad table count": func() []byte {
			b := make([]byte, 12)
			b[1] = 0x01 // sfnt version 1.0
			b[4], b[5] = 0xff, 0xff
			return b
		}(),
	}

	for name, data := range cases {
		if _, err := ReadMetrics(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestUintBase128(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []byte
		want    int64
		wantLen int
		wantErr bool
	}{
		{name: "zero", in: []byte{0x00}, want: 0, wantLen: 1},
		{name: "one byte", in: []byte{0x3f}, want: 63, wantLen: 1},
		{name: "two bytes", in: []byte{0x81, 0x00}, want: 128, wantLen: 2},
		{name: "three bytes", in: []byte{0x81, 0x80, 0x00}, want: 1 << 14, wantLen: 3},
		{name: "trailing bytes ignored", in: []byte{0x01, 0xff, 0xff}, want: 1, wantLen: 1},
		{name: "leading zero rejected", in: []byte{0x80, 0x01}, wantErr: true},
		{name: "truncated", in: []byte{0x81}, wantErr: true},
		{name: "empty", in: nil, wantErr: true},
		{name: "wider than 32 bits", in: []byte{0x90, 0x80, 0x80, 0x80, 0x00}, wantErr: true},
		{name: "longer than five bytes", in: []byte{0x81, 0x81, 0x81, 0x81, 0x81, 0x00}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, gotLen, err := uintBase128(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("uintBase128(% x) = (%d, %d, nil), want an error", tc.in, got, gotLen)
				}
				return
			}
			if err != nil {
				t.Fatalf("uintBase128(% x): %v", tc.in, err)
			}
			if got != tc.want || gotLen != tc.wantLen {
				t.Errorf("uintBase128(% x) = (%d, %d), want (%d, %d)", tc.in, got, gotLen, tc.want, tc.wantLen)
			}
		})
	}
}

func TestWoff2Transformed(t *testing.T) {
	t.Parallel()

	// glyf and loca are transformed by default and opt out at version 3; every
	// other table is untransformed at version 0. Reading this backwards would
	// shift every table after the first transformed one.
	tests := []struct {
		tag     string
		version byte
		want    bool
	}{
		{tag: "glyf", version: 0, want: true},
		{tag: "glyf", version: 3, want: false},
		{tag: "loca", version: 0, want: true},
		{tag: "loca", version: 3, want: false},
		{tag: "head", version: 0, want: false},
		{tag: "hhea", version: 0, want: false},
		{tag: "OS/2", version: 0, want: false},
		{tag: "hmtx", version: 1, want: true},
	}

	for _, tc := range tests {
		if got := woff2Transformed(tc.tag, tc.version); got != tc.want {
			t.Errorf("woff2Transformed(%q, %d) = %t, want %t", tc.tag, tc.version, got, tc.want)
		}
	}
}

func TestWoff2KnownTagsIndexes(t *testing.T) {
	t.Parallel()

	// The whole list has to stay in order for table offsets to come out right,
	// but these are the entries this package actually looks up.
	for index, want := range map[int]string{1: tagHead, 2: tagHhea, 6: tagOS2, 10: "glyf", 11: "loca"} {
		if got := woff2KnownTags[index]; got != want {
			t.Errorf("woff2KnownTags[%d] = %q, want %q", index, got, want)
		}
	}

	for index, tag := range woff2KnownTags {
		if len(tag) != 4 {
			t.Errorf("woff2KnownTags[%d] = %q, want a four-byte tag", index, tag)
		}
	}
}

// An x-height outside any text face's range is a lying OS/2 table, not an
// unusual design: Ovo claims 356/2048 and Rosarivo 170/1000 while their 'x'
// inks more than twice that. Trusting either sized them near 300% until lines
// overlapped, so the gate below the band rejects as firmly as a missing value.
func TestMetricsNormalizable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		metrics Metrics
		wantOK  bool
	}{
		{name: "literata reference", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.507}, wantOK: true},
		{name: "band low end", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.3}, wantOK: true},
		{name: "band high end", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.7}, wantOK: true},
		{name: "missing x-height", metrics: Metrics{UnitsPerEm: 1000}, wantOK: false},
		{name: "zeroed units", metrics: Metrics{XHeight: 0.5}, wantOK: false},
		{name: "ovo reports 356 of 2048", metrics: Metrics{UnitsPerEm: 2048, XHeight: 356.0 / 2048}, wantOK: false},
		{name: "rosarivo reports 170 of 1000", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.17}, wantOK: false},
		{name: "rosarivo italic reports 217 of 1000", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.217}, wantOK: false},
		{name: "just below the band", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.299}, wantOK: false},
		{name: "just above the band", metrics: Metrics{UnitsPerEm: 1000, XHeight: 0.701}, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.metrics.Normalizable(); got != tc.wantOK {
				t.Errorf("Normalizable(%+v) = %t, want %t", tc.metrics, got, tc.wantOK)
			}
		})
	}
}

// craftSFNT builds the smallest container ReadMetrics accepts: a TrueType
// header plus exactly the head, hhea and OS/2 tables it reads. Enough to pin
// how a lying or version-1 OS/2 table flows through the real parser without
// shipping a font as test data.
func craftSFNT(head, hhea, os2 []byte) []byte {
	tables := []struct {
		tag  string
		body []byte
	}{
		{"OS/2", os2},
		{"head", head},
		{"hhea", hhea},
	}
	headerLen := 12 + len(tables)*16
	header := make([]byte, headerLen)
	binary.BigEndian.PutUint32(header[0:], 0x00010000)
	binary.BigEndian.PutUint16(header[4:], uint16(len(tables)))
	at := headerLen
	for i, table := range tables {
		rec := header[12+i*16:]
		copy(rec[:4], table.tag)
		binary.BigEndian.PutUint32(rec[8:], uint32(at))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(table.body)))
		at += len(table.body)
	}
	data := make([]byte, 0, at)
	data = append(data, header...)
	for _, table := range tables {
		data = append(data, table.body...)
	}
	return data
}

func craftHead(unitsPerEm uint16) []byte {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], unitsPerEm)
	return head
}

func craftHhea(ascender, descender, lineGap int16) []byte {
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[4:], uint16(ascender))
	binary.BigEndian.PutUint16(hhea[6:], uint16(descender))
	binary.BigEndian.PutUint16(hhea[8:], uint16(lineGap))
	return hhea
}

func craftOS2(version uint16, typoAscender, typoDescender, typoLineGap, xHeight, capHeight int16) []byte {
	os2 := make([]byte, 96)
	binary.BigEndian.PutUint16(os2[0:], version)
	binary.BigEndian.PutUint16(os2[68:], uint16(typoAscender))
	binary.BigEndian.PutUint16(os2[70:], uint16(typoDescender))
	binary.BigEndian.PutUint16(os2[72:], uint16(typoLineGap))
	binary.BigEndian.PutUint16(os2[86:], uint16(xHeight))
	binary.BigEndian.PutUint16(os2[88:], uint16(capHeight))
	return os2
}

// The parser must accept these files (they are well-formed) yet refuse to
// normalize from them: the Ovo-shaped table reports a usable-looking x-height
// that is a lie, and the version-1 table stops before the x-height entirely.
func TestReadMetricsDistrustsBadXHeight(t *testing.T) {
	t.Parallel()

	// Ovo's shape: 2048-unit em, OS/2 version 3 claiming sxHeight 356.
	lying := craftSFNT(craftHead(2048), craftHhea(1770, -534, 0), craftOS2(3, 1770, -534, 0, 356, 210))
	metrics, err := ReadMetrics(lying)
	if err != nil {
		t.Fatalf("ReadMetrics(lying OS/2): %v", err)
	}
	if metrics.Normalizable() {
		t.Errorf("lying x-height %+v is Normalizable, want false", metrics)
	}

	// An OS/2 version 1 table carries no sxHeight at all.
	old := craftSFNT(craftHead(1000), craftHhea(800, -200, 0), craftOS2(1, 800, -200, 0, 0, 0)[:78])
	metrics, err = ReadMetrics(old)
	if err != nil {
		t.Fatalf("ReadMetrics(version 1 OS/2): %v", err)
	}
	if metrics.Normalizable() {
		t.Errorf("missing x-height %+v is Normalizable, want false", metrics)
	}

	// Control: the same scaffolding with an honest x-height stays usable.
	honest := craftSFNT(craftHead(1000), craftHhea(900, -300, 0), craftOS2(4, 900, -300, 0, 500, 700))
	metrics, err = ReadMetrics(honest)
	if err != nil {
		t.Fatalf("ReadMetrics(honest OS/2): %v", err)
	}
	if !metrics.Normalizable() {
		t.Errorf("honest x-height %+v is not Normalizable, want true", metrics)
	}
}

// The scan is how metrics reach the client, so it has to measure a real face on
// disk, let family.json override what it found, and survive a file that is not
// a font at all.
func TestScanFamilyMetrics(t *testing.T) {
	t.Parallel()

	var face []byte
	for name, data := range fontData {
		if strings.HasPrefix(name, "Literata-VariableFont") {
			face = data
			break
		}
	}
	if face == nil {
		t.Skip("the reference face is not embedded in this build")
	}

	root := t.TempDir()
	write := func(dir, name string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, name), content, 0o600); err != nil {
			t.Fatalf("write %s/%s: %v", dir, name, err)
		}
	}

	write("Measured", "Measured-Regular.woff2", face)

	// The same face, with its metrics pinned by hand.
	write("Overridden", "Overridden-Regular.woff2", face)
	write("Overridden", "family.json", []byte(`{"label":"Overridden","metrics":{"unitsPerEm":1000,"xHeight":0.42,"capHeight":0.68,"ascent":1,"descent":0.25,"lineGap":0}}`))

	write("Broken", "Broken-Regular.woff2", []byte("not a font"))

	byLabel := make(map[string]Family)
	for _, fam := range NewScanner(root).Families() {
		byLabel[fam.Label] = fam
	}
	if len(byLabel) != 3 {
		t.Fatalf("families = %d, want 3: %+v", len(byLabel), byLabel)
	}

	measured := byLabel["Measured"].Metrics
	switch {
	case measured == nil || !measured.Normalizable():
		t.Errorf("Measured.Metrics = %+v, want the face's own metrics", measured)
	case measured.XHeight < 0.5 || measured.XHeight > 0.52:
		t.Errorf("Measured x-height = %.4f, want the reference face's 0.507", measured.XHeight)
	}

	overridden := byLabel["Overridden"].Metrics
	switch {
	case overridden == nil:
		t.Error("Overridden.Metrics = nil, want the family.json values")
	case overridden.XHeight != 0.42:
		t.Errorf("Overridden x-height = %.4f, want the pinned 0.42", overridden.XHeight)
	}

	if broken := byLabel["Broken"].Metrics; broken != nil {
		t.Errorf("Broken.Metrics = %+v, want nil for a file that is not a font", broken)
	}
}
