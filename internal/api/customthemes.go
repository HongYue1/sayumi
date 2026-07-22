package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"sayumi/internal/storage"
)

const maxThemeNameLen = 60

// hexColorRe matches #rgb or #rrggbb (case-insensitive). Color <input> controls
// emit #rrggbb; the 3-digit shorthand is accepted for hand-entered values.
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type customThemeResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	Bg        string `json:"bg"`
	Fg        string `json:"fg"`
	Accent    string `json:"accent"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type customThemeBody struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Bg     string `json:"bg"`
	Fg     string `json:"fg"`
	Accent string `json:"accent"`
}

// exceedsRuneLimit keeps the common short-ASCII path at one byte-length check,
// then counts only when a multibyte name may fit within the character limit.
// It exits as soon as the limit is exceeded, so an oversized request never
// forces a full scan of the bounded JSON body.
func exceedsRuneLimit(s string, limit int) bool {
	if len(s) <= limit {
		return false
	}
	count := 0
	for range s {
		count++
		if count > limit {
			return true
		}
	}
	return false
}

func customThemeToResponse(t storage.CustomThemeRecord) customThemeResponse {
	return customThemeResponse{
		ID:        t.ID,
		Name:      t.Name,
		Group:     t.Group,
		Bg:        t.Bg,
		Fg:        t.Fg,
		Accent:    t.Accent,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}

// normalizeCustomThemeBody trims and validates the shared create/update
// payload in place, returning a user-facing message when invalid.
func normalizeCustomThemeBody(b *customThemeBody) (string, bool) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || exceedsRuneLimit(b.Name, maxThemeNameLen) {
		return "name must be 1-60 characters", false
	}
	if b.Group != "light" && b.Group != "dark" {
		return "group must be 'light' or 'dark'", false
	}
	b.Bg = strings.TrimSpace(b.Bg)
	b.Fg = strings.TrimSpace(b.Fg)
	b.Accent = strings.TrimSpace(b.Accent)
	if !hexColorRe.MatchString(b.Bg) {
		return "bg must be a hex color like #1c1917", false
	}
	if !hexColorRe.MatchString(b.Fg) {
		return "fg must be a hex color like #1c1917", false
	}
	if b.Accent != "" && !hexColorRe.MatchString(b.Accent) {
		return "accent must be a hex color like #2563eb", false
	}
	return "", true
}

func listCustomThemesHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		themes, err := pd.DB.ListCustomThemesContext(r.Context(), getUserID(r))
		if err != nil {
			slog.Error("list custom themes failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to list custom themes")
			return
		}

		resp := make([]customThemeResponse, 0, len(themes))
		for _, t := range themes {
			resp = append(resp, customThemeToResponse(t))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createCustomThemeHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		var body customThemeBody
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if msg, ok := normalizeCustomThemeBody(&body); !ok {
			writeError(w, http.StatusBadRequest, "invalid_body", msg)
			return
		}

		now := time.Now().UTC().Format(time.DateTime)
		rec := storage.CustomThemeRecord{
			ID:        storage.GenerateCustomThemeID(),
			UserID:    getUserID(r),
			Name:      body.Name,
			Group:     body.Group,
			Bg:        body.Bg,
			Fg:        body.Fg,
			Accent:    body.Accent,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := pd.DB.InsertCustomThemeContext(r.Context(), rec); err != nil {
			slog.Error("create custom theme failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to create custom theme")
			return
		}

		writeJSON(w, http.StatusCreated, customThemeToResponse(rec))
	}
}

func updateCustomThemeHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		var body customThemeBody
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if msg, ok := normalizeCustomThemeBody(&body); !ok {
			writeError(w, http.StatusBadRequest, "invalid_body", msg)
			return
		}

		rec := storage.CustomThemeRecord{
			ID:     r.PathValue("id"),
			UserID: getUserID(r),
			Name:   body.Name,
			Group:  body.Group,
			Bg:     body.Bg,
			Fg:     body.Fg,
			Accent: body.Accent,
		}
		updated, err := pd.DB.UpdateCustomThemeContext(r.Context(), rec)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "custom theme not found")
				return
			}
			slog.Error("update custom theme failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update custom theme")
			return
		}

		writeJSON(w, http.StatusOK, customThemeToResponse(updated))
	}
}

func deleteCustomThemeHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		if err := pd.DB.DeleteCustomThemeContext(r.Context(), id, getUserID(r)); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "custom theme not found")
				return
			}
			slog.Error("delete custom theme failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete custom theme")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
