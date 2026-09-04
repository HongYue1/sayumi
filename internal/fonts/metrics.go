package fonts

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
)

// Metrics are the vertical metrics of one face, each expressed as a fraction of
// the em. Ratios rather than design units means they hold at any font-size and
// can be compared between families directly.
//
// Two families set at the same font-size seldom look the same size, because
// their glyphs fill different fractions of the em: at 28px one may draw an x
// 13px tall and another 15px. XHeight is the ratio that decides that impression
// for running text, so it is the one worth normalizing on. The remaining values
// describe the line box the browser builds around those glyphs, which is why
// the same font-size and line-height can still yield different leading.
type Metrics struct {
	UnitsPerEm int     `json:"unitsPerEm"`
	XHeight    float64 `json:"xHeight"`
	CapHeight  float64 `json:"capHeight"`
	Ascent     float64 `json:"ascent"`
	Descent    float64 `json:"descent"` // distance below the baseline, kept positive
	LineGap    float64 `json:"lineGap"`
}

// Byte offsets of the fields read out of each table.
const (
	headUnitsPerEm = 18

	hheaAscender  = 4
	hheaDescender = 6
	hheaLineGap   = 8

	os2Version       = 0
	os2Selection     = 62
	os2TypoAscender  = 68
	os2TypoDescender = 70
	os2TypoLineGap   = 72
	os2XHeight       = 86
	os2CapHeight     = 88

	// os2XHeightVersion is the first OS/2 version to carry sxHeight and
	// sCapHeight. Older tables simply stop before those fields.
	os2XHeightVersion = 2

	// os2UseTypoMetrics is the fsSelection bit by which a face asks for its
	// sTypo* values to be preferred over hhea for line layout.
	os2UseTypoMetrics = 1 << 7
)

// ReadMetrics reads one face's em-relative metrics from .ttf, .otf, .woff or
// .woff2 bytes.
//
// XHeight and CapHeight come from OS/2, which only carries them from version 2
// onward. A face without them reports zero rather than a guess: a wrong ratio
// would silently mis-size text, while a zero can be detected and left alone.
// Normalizable tells the two apart, and additionally rejects an x-height no
// text face could honestly report.
func ReadMetrics(data []byte) (Metrics, error) {
	tables, err := readTables(data)
	if err != nil {
		return Metrics{}, err
	}

	head, hasHead := tables[tagHead]
	if !hasHead {
		return Metrics{}, fmt.Errorf("%w: no head table", errMalformedFont)
	}
	unitsPerEm, hasUnits := readU16(head, headUnitsPerEm)
	if !hasUnits || unitsPerEm == 0 {
		return Metrics{}, fmt.Errorf("%w: head table has no unitsPerEm", errMalformedFont)
	}
	em := float64(unitsPerEm)

	hhea, hasHhea := tables[tagHhea]
	if !hasHhea {
		return Metrics{}, fmt.Errorf("%w: no hhea table", errMalformedFont)
	}
	ascender, hasAscender := readI16(hhea, hheaAscender)
	descender, hasDescender := readI16(hhea, hheaDescender)
	lineGap, hasLineGap := readI16(hhea, hheaLineGap)
	if !hasAscender || !hasDescender || !hasLineGap {
		return Metrics{}, fmt.Errorf("%w: hhea table is truncated", errMalformedFont)
	}

	metrics := Metrics{
		UnitsPerEm: int(unitsPerEm),
		Ascent:     float64(ascender) / em,
		// hhea stores the descender as a negative offset from the baseline.
		// CSS descent-override wants the distance, so the sign is dropped here
		// rather than at each call site.
		Descent: float64(-descender) / em,
		LineGap: float64(lineGap) / em,
	}

	// A missing OS/2 table reads as an empty slice, which every accessor below
	// reports as absent, so an old face degrades instead of failing.
	os2 := tables[tagOS2]

	version, hasVersion := readU16(os2, os2Version)
	if hasVersion && version >= os2XHeightVersion {
		if xHeight, hasX := readI16(os2, os2XHeight); hasX && xHeight > 0 {
			metrics.XHeight = float64(xHeight) / em
		}
		if capHeight, hasCap := readI16(os2, os2CapHeight); hasCap && capHeight > 0 {
			metrics.CapHeight = float64(capHeight) / em
		}
	}

	if selection, hasSelection := readU16(os2, os2Selection); hasSelection && selection&os2UseTypoMetrics != 0 {
		typoAscender, hasTypoAscender := readI16(os2, os2TypoAscender)
		typoDescender, hasTypoDescender := readI16(os2, os2TypoDescender)
		typoLineGap, hasTypoLineGap := readI16(os2, os2TypoLineGap)
		if hasTypoAscender && hasTypoDescender && hasTypoLineGap {
			metrics.Ascent = float64(typoAscender) / em
			metrics.Descent = float64(-typoDescender) / em
			metrics.LineGap = float64(typoLineGap) / em
		}
	}

	return metrics, nil
}

// plausibleXHeight bounds the x-height a text face can honestly report, as a
// fraction of its em. The embedded faces all land near the middle (pinned by
// TestReadMetricsEmbeddedFaces); the ends are where a misread field shows up,
// not an unusual design.
const (
	minPlausibleXHeight = 0.3
	maxPlausibleXHeight = 0.7
)

// Normalizable reports whether this face carries an x-height the size
// normalization can trust. A face without one has to be left at its natural
// size — and so does a face with one no text face could report: Ovo and
// Rosarivo claim ~0.17 while their 'x' inks ~0.46-0.51 of the em, which sized
// them near 300% until the glyphs no longer fit the line box and lines
// overlapped. A wrong ratio mis-sizes text silently, while a false here
// degrades to natural size.
func (m Metrics) Normalizable() bool {
	return m.UnitsPerEm > 0 &&
		m.XHeight >= minPlausibleXHeight && m.XHeight <= maxPlausibleXHeight
}

// readU16 reads a big-endian uint16 at a byte offset, reporting false when the
// table stops before it. Tables are user-supplied and legitimately come in
// several lengths, so a short table is an expected outcome, not an error.
func readU16(table []byte, at int) (uint16, bool) {
	if at < 0 || at+2 > len(table) {
		return 0, false
	}
	return binary.BigEndian.Uint16(table[at:]), true
}

// readI16 reads a big-endian int16 at a byte offset. Font design units are
// signed: descenders and some offsets sit below the baseline.
func readI16(table []byte, at int) (int16, bool) {
	value, ok := readU16(table, at)
	return int16(value), ok
}

// embeddedFaceMetrics parses every embedded face once, on first use. The family
// the reader normalizes everything else against is one of these, so its metrics
// are needed wherever a font-face rule is built — but not before the first
// request, which is why this is not computed at start-up.
var embeddedFaceMetrics = sync.OnceValue(func() map[string]Metrics {
	parsed := make(map[string]Metrics, len(fontData))
	for name, data := range fontData {
		metrics, err := ReadMetrics(data)
		if err != nil {
			// An embedded face that will not parse is a build-time mistake, not a
			// runtime condition. Log it and leave that face unnormalized rather
			// than failing every font request.
			slog.Warn("read embedded font metrics", "font", name, "err", err)
			continue
		}
		parsed[name] = metrics
	}
	return parsed
})

// EmbeddedFaceMetrics returns the metrics of every embedded face, keyed the
// same way. The returned map is the shared parsed set and must be treated as
// read-only.
//
// It exists so the client can normalize the embedded families against measured
// numbers rather than a hand-copied table, which would go stale the moment a
// face is updated — and the family every other one is measured against is
// itself embedded, so those are exactly the numbers that must not drift.
func EmbeddedFaceMetrics() map[string]Metrics {
	return embeddedFaceMetrics()
}
