package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"

	"sayumi/internal/storage"
)

type settingsJSON struct {
	FontSize            int      `json:"fontSize"`
	FontFamily          string   `json:"fontFamily"`
	LineHeight          *float64 `json:"lineHeight"`
	ParagraphSpacing    *float64 `json:"paragraphSpacing"`
	TextIndent          *float64 `json:"textIndent"`
	ContentWidth        *int     `json:"contentWidth"`
	DisplayMode         string   `json:"displayMode"`
	MarginTop           *int     `json:"marginTop"`
	MarginBottom        *int     `json:"marginBottom"`
	MarginSide          *int     `json:"marginSide"`
	PreserveStyles      bool     `json:"preserveStyles"`
	PreserveFonts       bool     `json:"preserveFonts"`
	Justify             bool     `json:"justify"`
	Hyphenation         bool     `json:"hyphenation"`
	Theme               string   `json:"theme"`
	ChapterTitleAlign   *string  `json:"chapterTitleAlign"`
	ChapterTitleSize    *int     `json:"chapterTitleSize"`
	ChapterTitleSpacing *float64 `json:"chapterTitleSpacing"`
	HeaderSizesEnabled  bool     `json:"headerSizesEnabled"`
	H1Size              *int     `json:"h1Size"`
	H2Size              *int     `json:"h2Size"`
	H3Size              *int     `json:"h3Size"`
	H4Size              *int     `json:"h4Size"`
	H5Size              *int     `json:"h5Size"`
	H6Size              *int     `json:"h6Size"`
	HeaderWeight        *int     `json:"headerWeight"`
	TextWeight          *int     `json:"textWeight"`
	// FontRoles maps a font family id -> the file chosen for each role. Only
	// meaningful for user-supplied (./Fonts/) families; embedded fonts ignore it.
	FontRoles map[string]fontRoleEntry `json:"fontRoles"`
}

type fontRoleEntry struct {
	Regular    string `json:"regular,omitempty"`
	Italic     string `json:"italic,omitempty"`
	Bold       string `json:"bold,omitempty"`
	BoldItalic string `json:"boldItalic,omitempty"`
}

// recordToJSON converts a SettingsRecord (which may have NULL columns for a
// fresh profile) into the JSON shape sent to the client. The literal values
// below are the application defaults returned when no row exists yet.
// NOTE: these defaults must stay in sync with DEFAULT_USER_SETTINGS in the
// client settings store (frontend/src/lib/settings.svelte.ts).
func recordToJSON(s storage.SettingsRecord) settingsJSON {
	j := settingsJSON{
		FontSize:       30,
		FontFamily:     "eb-garamond",
		DisplayMode:    "scroll",
		PreserveStyles: true,
		PreserveFonts:  false,
		Justify:        true,
		Hyphenation:    true,
		Theme:          "catppuccin",
		FontRoles:      map[string]fontRoleEntry{},
	}

	// Set non-zero nullable defaults for fresh profiles.
	marginVal := 48
	j.MarginTop = &marginVal
	j.MarginBottom = &marginVal
	j.MarginSide = &marginVal

	if s.FontSize.Valid {
		j.FontSize = int(s.FontSize.Int64)
	}
	if s.FontFamily.Valid {
		j.FontFamily = s.FontFamily.String
	}
	if s.DisplayMode.Valid {
		j.DisplayMode = s.DisplayMode.String
	}
	if s.Theme.Valid {
		j.Theme = s.Theme.String
	}
	if s.PreserveStyles.Valid {
		j.PreserveStyles = s.PreserveStyles.Bool
	}
	if s.PreserveFonts.Valid {
		j.PreserveFonts = s.PreserveFonts.Bool
	}
	if s.Justify.Valid {
		j.Justify = s.Justify.Bool
	}
	if s.Hyphenation.Valid {
		j.Hyphenation = s.Hyphenation.Bool
	}

	j.LineHeight = nullFloat64ToPtr(s.LineHeight)
	j.ParagraphSpacing = nullFloat64ToPtr(s.ParagraphSpacing)
	j.TextIndent = nullFloat64ToPtr(s.TextIndent)
	j.ContentWidth = nullInt64ToIntPtr(s.ContentWidth)
	// Only override the fresh-profile defaults above when a value is actually
	// stored; a NULL column (no row yet, or a row saved without margins) must
	// keep the 48px default rather than collapsing to null.
	if v := nullInt64ToIntPtr(s.MarginTop); v != nil {
		j.MarginTop = v
	}
	if v := nullInt64ToIntPtr(s.MarginBottom); v != nil {
		j.MarginBottom = v
	}
	if v := nullInt64ToIntPtr(s.MarginSide); v != nil {
		j.MarginSide = v
	}
	j.ChapterTitleAlign = nullStringToPtr(s.ChapterTitleAlign)
	j.ChapterTitleSize = nullInt64ToIntPtr(s.ChapterTitleSize)
	j.ChapterTitleSpacing = nullFloat64ToPtr(s.ChapterTitleSpacing)
	if s.HeaderSizesEnabled.Valid {
		j.HeaderSizesEnabled = s.HeaderSizesEnabled.Bool
	}
	j.H1Size = nullInt64ToIntPtr(s.H1Size)
	j.H2Size = nullInt64ToIntPtr(s.H2Size)
	j.H3Size = nullInt64ToIntPtr(s.H3Size)
	j.H4Size = nullInt64ToIntPtr(s.H4Size)
	j.H5Size = nullInt64ToIntPtr(s.H5Size)
	j.H6Size = nullInt64ToIntPtr(s.H6Size)
	j.HeaderWeight = nullInt64ToIntPtr(s.HeaderWeight)
	j.TextWeight = nullInt64ToIntPtr(s.TextWeight)

	if s.FontRoles.Valid && strings.TrimSpace(s.FontRoles.String) != "" {
		var roles map[string]fontRoleEntry
		if err := json.Unmarshal([]byte(s.FontRoles.String), &roles); err == nil && roles != nil {
			j.FontRoles = roles
		}
	}

	return j
}

func nullFloat64ToPtr(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	return &nf.Float64
}

func nullInt64ToIntPtr(ni sql.NullInt64) *int {
	if !ni.Valid || ni.Int64 < math.MinInt || ni.Int64 > math.MaxInt {
		return nil
	}
	v := int(ni.Int64)
	return &v
}

func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func ptrToNullFloat64(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func ptrToNullInt64(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func ptrToNullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func normalizeSettings(j *settingsJSON) {
	j.FontFamily = strings.TrimSpace(j.FontFamily)
	j.DisplayMode = strings.ToLower(strings.TrimSpace(j.DisplayMode))
	j.Theme = strings.TrimSpace(j.Theme)

	if j.ChapterTitleAlign != nil {
		value := strings.ToLower(strings.TrimSpace(*j.ChapterTitleAlign))
		if value == "" {
			j.ChapterTitleAlign = nil
		} else {
			j.ChapterTitleAlign = &value
		}
	}

	// Drop empty role entries so the stored map stays compact.
	if j.FontRoles != nil {
		for id, e := range j.FontRoles {
			e.Regular = strings.TrimSpace(e.Regular)
			e.Italic = strings.TrimSpace(e.Italic)
			e.Bold = strings.TrimSpace(e.Bold)
			e.BoldItalic = strings.TrimSpace(e.BoldItalic)
			if e.Regular == "" && e.Italic == "" && e.Bold == "" && e.BoldItalic == "" {
				delete(j.FontRoles, id)
			} else {
				j.FontRoles[id] = e
			}
		}
	}
}

func validChapterTitleAlign(value string) bool {
	switch value {
	case "left", "center", "right":
		return true
	default:
		return false
	}
}

func validateSettings(j *settingsJSON) (string, bool) {
	if j.FontSize < 10 || j.FontSize > 50 {
		return "fontSize must be 10-50", false
	}
	if j.FontFamily == "" || len(j.FontFamily) > 64 {
		return "fontFamily must be 1-64 characters", false
	}
	if j.Theme == "" || len(j.Theme) > 32 {
		return "theme must be 1-32 characters", false
	}
	if j.DisplayMode != "scroll" && j.DisplayMode != "paged" && j.DisplayMode != "paged-two" {
		return "displayMode must be scroll, paged, or paged-two", false
	}
	if j.LineHeight != nil && (*j.LineHeight < 0.5 || *j.LineHeight > 4.0) {
		return "lineHeight must be 0.5-4.0", false
	}
	if j.ParagraphSpacing != nil && (*j.ParagraphSpacing < 0 || *j.ParagraphSpacing > 3.0) {
		return "paragraphSpacing must be 0-3.0", false
	}
	if j.TextIndent != nil && (*j.TextIndent < 0 || *j.TextIndent > 5.0) {
		return "textIndent must be 0-5.0", false
	}
	if j.ContentWidth != nil && (*j.ContentWidth < 40 || *j.ContentWidth > 100) {
		return "contentWidth must be 40-100", false
	}
	if j.MarginTop != nil && (*j.MarginTop < 0 || *j.MarginTop > 300) {
		return "marginTop must be 0-300", false
	}
	if j.MarginBottom != nil && (*j.MarginBottom < 0 || *j.MarginBottom > 300) {
		return "marginBottom must be 0-300", false
	}
	if j.MarginSide != nil && (*j.MarginSide < 0 || *j.MarginSide > 300) {
		return "marginSide must be 0-300", false
	}
	if j.ChapterTitleAlign != nil && !validChapterTitleAlign(*j.ChapterTitleAlign) {
		return "chapterTitleAlign must be left, center, or right", false
	}
	if j.ChapterTitleSize != nil && (*j.ChapterTitleSize < 10 || *j.ChapterTitleSize > 100) {
		return "chapterTitleSize must be 10-100", false
	}
	if j.ChapterTitleSpacing != nil && (*j.ChapterTitleSpacing < 0 || *j.ChapterTitleSpacing > 5.0) {
		return "chapterTitleSpacing must be 0-5.0", false
	}
	for _, hs := range []*int{j.H1Size, j.H2Size, j.H3Size, j.H4Size, j.H5Size, j.H6Size} {
		if hs != nil && (*hs < 10 || *hs > 100) {
			return "per-heading sizes must be 10-100", false
		}
	}
	if j.HeaderWeight != nil && (*j.HeaderWeight < 100 || *j.HeaderWeight > 900) {
		return "headerWeight must be 100-900", false
	}
	if j.TextWeight != nil && (*j.TextWeight < 100 || *j.TextWeight > 900) {
		return "textWeight must be 100-900", false
	}
	if len(j.FontRoles) > 100 {
		return "too many font role mappings", false
	}
	for id, e := range j.FontRoles {
		if len(id) > 128 {
			return "font family id too long", false
		}
		for _, file := range []string{e.Regular, e.Italic, e.Bold, e.BoldItalic} {
			if len(file) > 256 || strings.ContainsAny(file, "/\\") || strings.Contains(file, "..") {
				return "invalid font role file name", false
			}
		}
	}
	return "", true
}

func getSettingsHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		record, err := pd.DB.GetSettingsContext(r.Context(), getUserID(r))
		if err != nil {
			if !errors.Is(err, storage.ErrNotFound) {
				slog.Error("load settings failed", "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to load settings")
				return
			}
			// Fresh profile (no settings row yet): start from the stored-record
			// defaults and apply the app's non-Auto defaults for the Auto-capable
			// fields. These are applied ONLY here, never in recordToJSON, so an
			// existing profile that explicitly chose "Auto" (a NULL column) keeps
			// Auto on reload instead of being silently reset to these values.
			fresh := recordToJSON(storage.SettingsRecord{})
			textIndent := 0.0
			fresh.TextIndent = &textIndent
			titleAlign := "center"
			fresh.ChapterTitleAlign = &titleAlign
			titleSize := 48
			fresh.ChapterTitleSize = &titleSize
			titleSpacing := 1.0
			fresh.ChapterTitleSpacing = &titleSpacing
			writeJSON(w, http.StatusOK, fresh)
			return
		}

		writeJSON(w, http.StatusOK, recordToJSON(record))
	}
}

func putSettingsHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		userID := getUserID(r)

		var j settingsJSON
		if !decodeJSONBody(w, r, &j) {
			return
		}

		normalizeSettings(&j)

		if msg, ok := validateSettings(&j); !ok {
			writeError(w, http.StatusBadRequest, "invalid", msg)
			return
		}

		if j.FontRoles == nil {
			j.FontRoles = map[string]fontRoleEntry{}
		}
		fontRolesJSON := ""
		if len(j.FontRoles) > 0 {
			b, err := json.Marshal(j.FontRoles)
			if err != nil {
				slog.Error("marshal font roles failed", "err", err)
				writeError(w, http.StatusInternalServerError, "server_error", "failed to encode settings")
				return
			}
			fontRolesJSON = string(b)
		}

		record := storage.SettingsRecord{
			UserID:              userID,
			FontSize:            sql.NullInt64{Int64: int64(j.FontSize), Valid: true},
			FontFamily:          sql.NullString{String: j.FontFamily, Valid: j.FontFamily != ""},
			DisplayMode:         sql.NullString{String: j.DisplayMode, Valid: true},
			Theme:               sql.NullString{String: j.Theme, Valid: true},
			PreserveStyles:      sql.NullBool{Bool: j.PreserveStyles, Valid: true},
			PreserveFonts:       sql.NullBool{Bool: j.PreserveFonts, Valid: true},
			Justify:             sql.NullBool{Bool: j.Justify, Valid: true},
			Hyphenation:         sql.NullBool{Bool: j.Hyphenation, Valid: true},
			LineHeight:          ptrToNullFloat64(j.LineHeight),
			ParagraphSpacing:    ptrToNullFloat64(j.ParagraphSpacing),
			TextIndent:          ptrToNullFloat64(j.TextIndent),
			ContentWidth:        ptrToNullInt64(j.ContentWidth),
			MarginTop:           ptrToNullInt64(j.MarginTop),
			MarginBottom:        ptrToNullInt64(j.MarginBottom),
			MarginSide:          ptrToNullInt64(j.MarginSide),
			ChapterTitleAlign:   ptrToNullString(j.ChapterTitleAlign),
			ChapterTitleSize:    ptrToNullInt64(j.ChapterTitleSize),
			ChapterTitleSpacing: ptrToNullFloat64(j.ChapterTitleSpacing),
			HeaderSizesEnabled:  sql.NullBool{Bool: j.HeaderSizesEnabled, Valid: true},
			H1Size:              ptrToNullInt64(j.H1Size),
			H2Size:              ptrToNullInt64(j.H2Size),
			H3Size:              ptrToNullInt64(j.H3Size),
			H4Size:              ptrToNullInt64(j.H4Size),
			H5Size:              ptrToNullInt64(j.H5Size),
			H6Size:              ptrToNullInt64(j.H6Size),
			HeaderWeight:        ptrToNullInt64(j.HeaderWeight),
			TextWeight:          ptrToNullInt64(j.TextWeight),
			FontRoles:           sql.NullString{String: fontRolesJSON, Valid: true},
		}

		if err := pd.DB.SaveSettingsContext(r.Context(), record); err != nil {
			slog.Error("save settings failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to save settings")
			return
		}

		writeJSON(w, http.StatusOK, j)
	}
}
