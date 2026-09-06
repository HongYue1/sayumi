package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"sayumi/internal/storage"
)

func settingsTestBase() settingsJSON {
	return settingsJSON{
		FontSize: 26, FontFamily: "literata", Theme: "catppuccin", DisplayMode: "scroll",
		FontRoles: map[string]fontRoleEntry{},
	}
}

func TestSettingsRecordDefaultsAndRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want map[string]fontRoleEntry
	}{
		{name: "empty", raw: ""},
		{name: "whitespace", raw: " \n\t"},
		{name: "null", raw: "null"},
		{name: "empty object", raw: "{}"},
		{name: "malformed", raw: "{"},
		{name: "array", raw: "[]"},
		{name: "wrong role type", raw: `{"user:F":{"regular":false}}`},
		{
			name: "stored roles are not renormalized",
			raw:  `{"user:F":{"regular":" Reg.ttf ","boldItalic":"BI.ttf"}}`,
			want: map[string]fontRoleEntry{"user:F": {Regular: " Reg.ttf ", BoldItalic: "BI.ttf"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			record := storage.SettingsRecord{FontRoles: tc.raw}
			got := recordToJSON(record)
			want := settingsJSON{
				FontSize: 30, FontFamily: "literata", DisplayMode: "scroll", Theme: "catppuccin",
				PreserveStyles: true, Justify: true, Hyphenation: true,
				MarginTop: new(48), MarginBottom: new(48), MarginSide: new(48),
				FontRoles: tc.want,
			}
			if want.FontRoles == nil {
				want.FontRoles = map[string]fontRoleEntry{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("recordToJSON = %+v, want %+v", got, want)
			}
			// Each conversion owns its decoded map; one response must not
			// mutate another response or turn the empty default into null.
			got.FontRoles["mutated"] = fontRoleEntry{Regular: "other.ttf"}
			if again := recordToJSON(record); !reflect.DeepEqual(again, want) {
				t.Errorf("conversion retained a previous response's mutation: %+v", again)
			}
		})
	}
}

func TestSettingsNullableConversions(t *testing.T) {
	t.Parallel()

	if nullFloat64ToPtr(sql.NullFloat64{Float64: 2}) != nil ||
		nullInt64ToIntPtr(sql.NullInt64{Int64: 2}) != nil ||
		nullStringToPtr(sql.NullString{String: "ignored"}) != nil {
		t.Fatal("invalid SQL nullable values must become nil, regardless of their payload")
	}
	if ptrToNullFloat64(nil).Valid || ptrToNullInt64(nil).Valid || ptrToNullString(nil).Valid {
		t.Fatal("nil pointers must become SQL NULL")
	}
	for _, v := range []float64{0, -0.25, 1.75} {
		t.Run("float "+strconv.FormatFloat(v, 'g', -1, 64), func(t *testing.T) {
			stored := sql.NullFloat64{Float64: v, Valid: true}
			p := nullFloat64ToPtr(stored)
			if p == nil || *p != v || ptrToNullFloat64(p) != stored {
				t.Fatalf("nullable float did not round-trip: %v", stored)
			}
		})
	}
	for _, v := range []string{"", "user:Family"} {
		t.Run("string "+v, func(t *testing.T) {
			stored := sql.NullString{String: v, Valid: true}
			p := nullStringToPtr(stored)
			if p == nil || *p != v || ptrToNullString(p) != stored {
				t.Fatalf("nullable string did not round-trip: %v", stored)
			}
		})
	}
	for _, v := range []int64{math.MinInt64, math.MinInt32, -1, 0, 1, math.MaxInt32, math.MaxInt64} {
		t.Run("int "+strconv.FormatInt(v, 10), func(t *testing.T) {
			stored := sql.NullInt64{Int64: v, Valid: true}
			p := nullInt64ToIntPtr(stored)
			// The overflow arm is exercised only on a 32-bit test target;
			// a 64-bit run cannot establish that platform's execution result.
			if strconv.IntSize == 32 && (v < math.MinInt32 || v > math.MaxInt32) {
				if p != nil {
					t.Fatalf("out-of-range SQL integer %d became %d", v, *p)
				}
				return
			}
			if p == nil || int64(*p) != v || ptrToNullInt64(p) != stored {
				t.Fatalf("nullable int did not round-trip: %v", stored)
			}
		})
	}
}

func TestSettingsNormalizeOptionalFields(t *testing.T) {
	t.Parallel()

	align, family := " RIGHT ", " user:Mixed Case "
	s := settingsTestBase()
	s.FontFamily = " user:Mixed Case "
	s.DisplayMode = " PaGeD-TwO "
	s.Theme = " Custom-Theme "
	s.ChapterTitleAlign, s.ChapterTitleFont = &align, &family
	s.FontRoles = map[string]fontRoleEntry{
		"user:Mixed Case": {Regular: " Reg.ttf ", Italic: " I.ttf ", Bold: " B.ttf ", BoldItalic: " BI.ttf "},
		"user:Empty":      {Regular: " ", Italic: "\t", Bold: "\n", BoldItalic: " "},
	}
	normalizeSettings(&s)
	want := settingsTestBase()
	want.FontFamily, want.DisplayMode, want.Theme = "user:Mixed Case", "paged-two", "Custom-Theme"
	want.ChapterTitleAlign, want.ChapterTitleFont = new("right"), new("user:Mixed Case")
	want.FontRoles = map[string]fontRoleEntry{
		"user:Mixed Case": {Regular: "Reg.ttf", Italic: "I.ttf", Bold: "B.ttf", BoldItalic: "BI.ttf"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("normalized settings = %+v, want %+v", s, want)
	}
	if align != " RIGHT " || family != " user:Mixed Case " {
		t.Fatal("normalization mutated the caller's optional string storage")
	}
	normalizeSettings(&s)
	if !reflect.DeepEqual(s, want) {
		t.Fatal("normalization is not idempotent")
	}
	s.ChapterTitleAlign, s.ChapterTitleFont = new(" \t"), new("\n ")
	normalizeSettings(&s)
	if s.ChapterTitleAlign != nil || s.ChapterTitleFont != nil {
		t.Fatal("blank optional strings must normalize to Auto (nil)")
	}
	if msg, ok := validateSettings(&s); !ok {
		t.Fatalf("normalized settings rejected: %s", msg)
	}
}

func TestSettingsNumericBoundaries(t *testing.T) {
	t.Parallel()

	// The JSON field names are independent of Go field selectors, so both a
	// missing validator and a mismapped JSON tag break an out-of-range case.
	tests := []struct {
		field string
		low   string
		high  string
		below string
		above string
	}{
		{field: "fontSize", low: "10", high: "50", below: "9", above: "51"},
		{field: "lineHeight", low: "0.5", high: "4", below: "0.499", above: "4.001"},
		{field: "paragraphSpacing", low: "0", high: "3", below: "-0.001", above: "3.001"},
		{field: "textIndent", low: "0", high: "5", below: "-0.001", above: "5.001"},
		{field: "letterSpacing", low: "-0.5", high: "1", below: "-0.501", above: "1.001"},
		{field: "contentWidth", low: "40", high: "100", below: "39", above: "101"},
		{field: "marginTop", low: "0", high: "300", below: "-1", above: "301"},
		{field: "marginBottom", low: "0", high: "300", below: "-1", above: "301"},
		{field: "marginSide", low: "0", high: "300", below: "-1", above: "301"},
		{field: "chapterTitleSize", low: "10", high: "100", below: "9", above: "101"},
		{field: "chapterTitleSpacing", low: "0", high: "5", below: "-0.001", above: "5.001"},
		{field: "headingLetterSpacing", low: "-0.5", high: "1", below: "-0.501", above: "1.001"},
		{field: "h1Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "h2Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "h3Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "h4Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "h5Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "h6Size", low: "10", high: "100", below: "9", above: "101"},
		{field: "headerWeight", low: "100", high: "900", below: "99", above: "901"},
		{field: "textWeight", low: "100", high: "900", below: "99", above: "901"},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			bounds := []struct {
				name string
				raw  string
				ok   bool
			}{
				{name: "below", raw: tc.below},
				{name: "lower endpoint", raw: tc.low, ok: true},
				{name: "upper endpoint", raw: tc.high, ok: true},
				{name: "above", raw: tc.above},
			}
			if tc.field != "fontSize" {
				bounds = append(bounds, struct {
					name string
					raw  string
					ok   bool
				}{name: "Auto", raw: "null", ok: true})
			}
			for _, bound := range bounds {
				t.Run(bound.name, func(t *testing.T) {
					s := settingsTestBase()
					if err := json.Unmarshal([]byte(fmt.Sprintf(`{%q:%s}`, tc.field, bound.raw)), &s); err != nil {
						t.Fatal(err)
					}
					msg, ok := validateSettings(&s)
					if ok != bound.ok || (ok && msg != "") || (!ok && msg == "") {
						t.Errorf("validate %s=%s: (%q, %v), want ok=%v", tc.field, bound.raw, msg, ok, bound.ok)
					}
				})
			}
		})
	}
}

func TestSettingsEnumsAndThemeBytes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"scroll", "paged", "paged-two", "sideways", ""} {
		t.Run("mode "+mode, func(t *testing.T) {
			s := settingsTestBase()
			s.DisplayMode = mode
			_, ok := validateSettings(&s)
			if want := mode == "scroll" || mode == "paged" || mode == "paged-two"; ok != want {
				t.Errorf("displayMode %q: ok=%v, want %v", mode, ok, want)
			}
		})
	}
	for _, align := range []string{"left", "center", "right", "justify", ""} {
		t.Run("alignment "+align, func(t *testing.T) {
			s := settingsTestBase()
			s.ChapterTitleAlign = &align
			_, ok := validateSettings(&s)
			if want := align == "left" || align == "center" || align == "right"; ok != want {
				t.Errorf("chapterTitleAlign %q: ok=%v, want %v", align, ok, want)
			}
		})
	}
	for _, theme := range []string{"", strings.Repeat("a", 32), strings.Repeat("a", 33), strings.Repeat("界", 10) + "ab", strings.Repeat("界", 11)} {
		t.Run("theme "+strconv.Itoa(len(theme))+" bytes "+theme, func(t *testing.T) {
			s := settingsTestBase()
			s.Theme = theme
			_, ok := validateSettings(&s)
			if want := len(theme) > 0 && len(theme) <= 32; ok != want {
				t.Errorf("theme %q: ok=%v, want %v", theme, ok, want)
			}
		})
	}
}

func TestSettingsFontRoleBoundaries(t *testing.T) {
	t.Parallel()

	for _, id := range []string{strings.Repeat("é", 64), strings.Repeat("é", 64) + "a"} {
		t.Run(strconv.Itoa(len(id))+" byte family id", func(t *testing.T) {
			s := settingsTestBase()
			s.FontRoles[id] = fontRoleEntry{Regular: "Reg.ttf"}
			if msg, ok := validateSettings(&s); ok != (len(id) == 128) {
				t.Errorf("family id bytes=%d: (%q, %v)", len(id), msg, ok)
			}
		})
	}
	for _, count := range []int{100, 101} {
		t.Run(strconv.Itoa(count)+" families", func(t *testing.T) {
			s := settingsTestBase()
			for i := range count {
				s.FontRoles["user:"+strconv.Itoa(i)] = fontRoleEntry{Regular: "Reg.ttf"}
			}
			if _, ok := validateSettings(&s); ok != (count == 100) {
				t.Errorf("%d nonempty families: ok=%v", count, ok)
			}
			// Empty mappings are pruned before validation in both callers.
			for id := range s.FontRoles {
				s.FontRoles[id] = fontRoleEntry{}
			}
			normalizeSettings(&s)
			if msg, ok := validateSettings(&s); !ok || len(s.FontRoles) != 0 {
				t.Errorf("empty mappings were not pruned: %s", msg)
			}
		})
	}
	for _, role := range []string{"regular", "italic", "bold", "boldItalic"} {
		t.Run(role, func(t *testing.T) {
			tests := []struct {
				name string
				file string
				ok   bool
			}{
				{name: "unset", file: "", ok: true},
				{name: "literal percent and space", file: "Reg 100%.ttf", ok: true},
				{name: "256 bytes", file: strings.Repeat("é", 128), ok: true},
				{name: "257 bytes", file: strings.Repeat("é", 128) + "a"},
				{name: "slash", file: "dir/font.ttf"},
				{name: "backslash", file: `dir\font.ttf`},
				{name: "parent", file: "../font.ttf"},
				{name: "embedded double dot", file: "font..ttf"},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					s := settingsTestBase()
					raw := fmt.Sprintf(`{"fontRoles":{"user:F":{%q:%q}}}`, role, tc.file)
					if err := json.Unmarshal([]byte(raw), &s); err != nil {
						t.Fatal(err)
					}
					if msg, ok := validateSettings(&s); ok != tc.ok {
						t.Errorf("%s=%q: (%q, %v), want ok=%v", role, tc.file, msg, ok, tc.ok)
					}
				})
			}
		})
	}
}

func TestSettingsByteLimitMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*settingsJSON)
		want string
	}{
		{
			name: "theme",
			edit: func(s *settingsJSON) { s.Theme = strings.Repeat("界", 11) },
			want: "theme must be 1-32 bytes",
		},
		{
			name: "role family id",
			edit: func(s *settingsJSON) {
				s.FontRoles[strings.Repeat("a", maxFontFamilyIDBytes+1)] = fontRoleEntry{Regular: "Reg.ttf"}
			},
			want: "font family id must be at most 128 bytes",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := settingsTestBase()
			tc.edit(&s)
			for _, preset := range []bool{false, true} {
				t.Run(fmt.Sprintf("preset=%v", preset), func(t *testing.T) {
					// A preset shares this validation pipeline. A rejected payload
					// must stop before either handler needs a database.
					var body any = s
					handler := putSettingsHandler(nil)
					method := http.MethodPut
					if preset {
						body = createPresetBody{Name: "Example", Settings: s}
						handler = createPresetHandler(nil)
						method = http.MethodPost
					}
					blob, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequestWithContext(t.Context(), method, "/api/settings", strings.NewReader(string(blob)))
					w := httptest.NewRecorder()
					handler(w, withProfileDeps(r, &profileDeps{}))
					assertSettingsError(t, w, http.StatusBadRequest, apiError{Code: "invalid", Error: tc.want})
				})
			}
		})
	}
}

// Use the real embedded SQLite adapter in an isolated temporary library, not
// a SQL mock: a mock cannot catch nullable-field or bind/scan-order mistakes.
// Like the adjacent API tests, these need no external service or build tag.
func settingsTestProfile(t *testing.T) *profileDeps {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the reference owned by authMiddleware without starting the
	// unrelated scanner/store/coalescer. The settings handlers only borrow it.
	pd := &profileDeps{DB: db, LibPath: dir, refs: 1}
	pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
	t.Cleanup(func() {
		pd.lifetimeMu.Lock()
		refs := pd.refs
		pd.lifetimeMu.Unlock()
		if refs != 1 {
			t.Errorf("settings handler changed its borrowed profile ref: got %d, want 1", refs)
		} else {
			pd.release()
		}
		if err := db.Close(); err != nil {
			t.Errorf("close settings DB: %v", err)
		}
	})
	return pd
}

func settingsTestRequest(t *testing.T, pd *profileDeps, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "/api/settings", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler := getSettingsHandler(nil)
	if method == http.MethodPut {
		handler = putSettingsHandler(nil)
	}
	handler(w, withProfileDeps(r, pd))
	return w
}

func assertSettingsResponse(t *testing.T, w *httptest.ResponseRecorder, status int, want any) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options=%q", got)
	}
	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Body.String(); got != string(blob)+"\n" {
		t.Errorf("body=%s, want %s followed by one newline", got, blob)
	}
}

func assertSettingsError(t *testing.T, w *httptest.ResponseRecorder, status int, want apiError) {
	t.Helper()
	assertSettingsResponse(t, w, status, want)
}

func TestSettingsHandlerFreshAndStoredAuto(t *testing.T) {
	t.Parallel()
	pd := settingsTestProfile(t)
	// Keep these independently specified defaults in sync with the client
	// DEFAULT_USER_SETTINGS; recordToJSON alone deliberately is not that value.
	want := settingsJSON{
		FontSize: 30, FontFamily: "literata", DisplayMode: "scroll", Theme: "catppuccin",
		PreserveStyles: true, Justify: true, Hyphenation: true,
		MarginTop: new(48), MarginBottom: new(48), MarginSide: new(48),
		TextIndent: new(0.0), ChapterTitleAlign: new("center"),
		ChapterTitleSize: new(48), ChapterTitleSpacing: new(1.0),
		FontRoles: map[string]fontRoleEntry{},
	}
	assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodGet, ""), http.StatusOK, want)
	if _, err := pd.DB.GetSettingsContext(t.Context(), "default"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("GET created a settings row or hid a DB error: %v", err)
	}

	if err := pd.DB.SaveSettingsContext(t.Context(), storage.SettingsRecord{UserID: "default"}); err != nil {
		t.Fatal(err)
	}
	want.TextIndent, want.ChapterTitleAlign = nil, nil
	want.ChapterTitleSize, want.ChapterTitleSpacing = nil, nil
	// Stored NULLs for Auto-capable title/indent fields are different from a
	// missing row. Margins retain their separate legacy 48px fallback.
	assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodGet, ""), http.StatusOK, want)
}

func TestSettingsHandlerSnapshotRoundTrip(t *testing.T) {
	t.Parallel()
	pd := settingsTestProfile(t)
	want := settingsTestBase()
	want.FontSize, want.FontFamily, want.DisplayMode, want.Theme = 37, "user:Family", "paged-two", "Custom-Theme"
	want.LineHeight, want.ParagraphSpacing, want.TextIndent = new(1.75), new(1.25), new(2.5)
	want.LetterSpacing, want.ContentWidth = new(-0.25), new(73)
	want.MarginTop, want.MarginBottom, want.MarginSide = new(0), new(201), new(37)
	want.PreserveFonts, want.HeaderSizesEnabled = true, true
	want.ChapterTitleAlign, want.ChapterTitleFont = new("right"), new("user:Heading")
	want.ChapterTitleSize, want.ChapterTitleSpacing, want.HeadingLetterSpacing = new(63), new(2.25), new(0.5)
	want.H1Size, want.H2Size, want.H3Size = new(71), new(62), new(53)
	want.H4Size, want.H5Size, want.H6Size = new(44), new(35), new(26)
	want.HeaderWeight, want.TextWeight = new(650), new(450)
	want.FontRoles = map[string]fontRoleEntry{
		"user:Family": {Regular: "R.ttf", Italic: "I.ttf", Bold: "B.ttf", BoldItalic: "BI.ttf"},
	}
	input := want
	input.FontFamily, input.DisplayMode, input.Theme = " user:Family ", " PAGED-TWO ", " Custom-Theme "
	input.ChapterTitleAlign, input.ChapterTitleFont = new(" RIGHT "), new(" user:Heading ")
	input.FontRoles = map[string]fontRoleEntry{
		"user:Family": {Regular: " R.ttf ", Italic: " I.ttf ", Bold: " B.ttf ", BoldItalic: " BI.ttf "},
		"user:Empty":  {},
	}
	blob, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodPut, string(blob)), http.StatusOK, want)
	assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodGet, ""), http.StatusOK, want)

	// PUT is a complete snapshot, not a patch. Both omitted and explicit null
	// optionals reset the stored values; absent booleans become false.
	reset := `{"fontSize":26,"fontFamily":"literata","displayMode":"scroll","theme":"catppuccin"}`
	for _, extra := range []string{"", `,"textIndent":null,"chapterTitleAlign":null,"fontRoles":null`} {
		t.Run("reset "+extra, func(t *testing.T) {
			assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodPut, string(blob)), http.StatusOK, want)
			body := strings.TrimSuffix(reset, "}") + extra + "}"
			cleared := settingsTestBase()
			assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodPut, body), http.StatusOK, cleared)
			cleared.MarginTop, cleared.MarginBottom, cleared.MarginSide = new(48), new(48), new(48)
			assertSettingsResponse(t, settingsTestRequest(t, pd, http.MethodGet, ""), http.StatusOK, cleared)
			record, err := pd.DB.GetSettingsContext(t.Context(), "default")
			if err != nil {
				t.Fatal(err)
			}
			if record.TextIndent.Valid || record.ChapterTitleAlign.Valid || record.MarginTop.Valid || record.FontRoles != "" {
				t.Errorf("reset did not persist NULLs/empty role storage: %+v", record)
			}
		})
	}
}

func TestSettingsHandlerRejectsWithoutSaving(t *testing.T) {
	t.Parallel()
	pd := settingsTestProfile(t)
	original := storage.SettingsRecord{
		UserID: "default", FontSize: sql.NullInt64{Int64: 41, Valid: true}, FontRoles: "{}",
	}
	if err := pd.DB.SaveSettingsContext(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	original, err := pd.DB.GetSettingsContext(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	const base = `{"fontSize":26,"fontFamily":"literata","displayMode":"scroll","theme":"catppuccin"}`
	tests := []struct {
		name   string
		body   string
		status int
		want   apiError
	}{
		{name: "empty", body: "", status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "malformed", body: "{", status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "second value", body: base + "{}", status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "null", body: "null", status: http.StatusBadRequest, want: apiError{Code: "invalid", Error: "fontSize must be 10-50"}},
		{name: "array", body: "[]", status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "fractional integer", body: `{"fontSize":26.5}`, status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "NaN", body: `{"lineHeight":NaN}`, status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "infinity", body: `{"lineHeight":1e9999}`, status: http.StatusBadRequest, want: apiError{Code: "invalid_body", Error: "invalid JSON body"}},
		{name: "invalid range", body: `{"fontSize":51}`, status: http.StatusBadRequest, want: apiError{Code: "invalid", Error: "fontSize must be 10-50"}},
		{
			name: "over body limit", body: base + strings.Repeat(" ", maxJSONBodySize-len(base)+1),
			status: http.StatusRequestEntityTooLarge, want: apiError{Code: "too_large", Error: "request body too large"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertSettingsError(t, settingsTestRequest(t, pd, http.MethodPut, tc.body), tc.status, tc.want)
			got, err := pd.DB.GetSettingsContext(t.Context(), "default")
			if err != nil {
				t.Fatal(err)
			}
			if got != original {
				t.Errorf("rejected PUT changed persisted settings: %+v, want %+v", got, original)
			}
		})
	}
}

func TestSettingsHandlerBodyLimitAndCompatibility(t *testing.T) {
	t.Parallel()
	pd := settingsTestProfile(t)
	// Unknown fields remain tolerated and duplicate scalar fields retain the
	// decoder's last-value behavior. Do not turn PUT into a strict-schema API.
	const body = `{"fontSize":12,"fontSize":26,"fontFamily":"literata","displayMode":"scroll","theme":"catppuccin","future":{"x":true},"userID":"other"}`
	for _, unknownLength := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown length=%v", unknownLength), func(t *testing.T) {
			padded := body + strings.Repeat(" ", maxJSONBodySize-len(body))
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/settings", strings.NewReader(padded))
			if unknownLength {
				r.ContentLength = -1
			}
			w := httptest.NewRecorder()
			putSettingsHandler(nil)(w, withProfileDeps(r, pd))
			assertSettingsResponse(t, w, http.StatusOK, settingsTestBase())
		})
	}
	if _, err := pd.DB.GetSettingsContext(t.Context(), "other"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("client-supplied userID selected a storage row: %v", err)
	}
}

func TestSettingsHandlerDatabaseErrors(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"canceled", "closed"} {
		t.Run(failure, func(t *testing.T) {
			pd := settingsTestProfile(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "canceled" {
				cancel()
			} else if err := pd.DB.Close(); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				t.Run(method, func(t *testing.T) {
					handler, message := getSettingsHandler(nil), "failed to load settings"
					if method == http.MethodPut {
						handler, message = putSettingsHandler(nil), "failed to save settings"
					}
					body := `{"fontSize":26,"fontFamily":"literata","displayMode":"scroll","theme":"catppuccin"}`
					r := httptest.NewRequestWithContext(ctx, method, "/api/settings", strings.NewReader(body))
					w := httptest.NewRecorder()
					handler(w, withProfileDeps(r, pd))
					assertSettingsError(t, w, http.StatusInternalServerError, apiError{Code: "db_error", Error: message})
				})
			}
			if failure == "canceled" {
				if _, err := pd.DB.GetSettingsContext(t.Context(), "default"); !errors.Is(err, storage.ErrNotFound) {
					t.Fatalf("canceled PUT wrote a settings row: %v", err)
				}
			}
		})
	}
}

func TestSettingsHandlersNeedProfileAndAuthentication(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			// Missing profile context is an internal wiring failure, not a
			// fresh profile. Even an invalid PUT body must not mask it.
			w := settingsTestRequest(t, nil, method, "{")
			assertSettingsError(t, w, http.StatusInternalServerError, apiError{Code: "server_error", Error: "profile not available"})
			// The actual registered routes enforce authentication before the
			// settings handler. No session/profile DB is needed to deny this.
			handler := NewHandler(&Dependencies{}, http.NotFoundHandler(), http.NotFoundHandler())
			r := httptest.NewRequestWithContext(t.Context(), method, "/api/settings", strings.NewReader("{"))
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertSettingsError(t, w, http.StatusUnauthorized, apiError{Code: "unauthenticated", Error: "not logged in"})
		})
	}
}

func TestSettingsHandlerProfileIsolation(t *testing.T) {
	t.Parallel()
	a, b := settingsTestProfile(t), settingsTestProfile(t)
	profiles := []struct {
		name string
		pd   *profileDeps
		size int
	}{
		{name: "first", pd: a, size: 16},
		{name: "second", pd: b, size: 42},
	}
	// Both databases use the same "default" row key. Only the borrowed
	// request context chooses the profile; handler construction retains none.
	put, get := putSettingsHandler(nil), getSettingsHandler(nil)
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			want := settingsTestBase()
			want.FontSize = p.size
			body, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/settings?profile=other&userID=other", strings.NewReader(string(body)))
			r.Header.Set("X-User-Id", "other")
			w := httptest.NewRecorder()
			put(w, withProfileDeps(r, p.pd))
			assertSettingsResponse(t, w, http.StatusOK, want)
		})
	}
	for _, p := range profiles {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/settings?profile=other", nil)
		w := httptest.NewRecorder()
		get(w, withProfileDeps(r, p.pd))
		want := settingsTestBase()
		want.FontSize = p.size
		want.MarginTop, want.MarginBottom, want.MarginSide = new(48), new(48), new(48)
		assertSettingsResponse(t, w, http.StatusOK, want)
		if _, err := p.pd.DB.GetSettingsContext(t.Context(), "other"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("client selected a different user row in %s: %v", p.name, err)
		}
	}
}
