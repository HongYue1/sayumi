package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"sayumi/internal/storage"
)

const maxPresetNameLen = 60

type presetResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

type createPresetBody struct {
	Name     string       `json:"name"`
	Settings settingsJSON `json:"settings"`
}

func normalizePresetName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	return name, name != "" && !exceedsRuneLimit(name, maxPresetNameLen)
}

func presetToResponse(p storage.PresetRecord) presetResponse {
	return presetResponse{
		ID:        p.ID,
		Name:      p.Name,
		Settings:  json.RawMessage(p.SettingsJSON),
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func listPresetsHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		presets, err := pd.DB.ListPresetsContext(r.Context(), getUserID(r))
		if err != nil {
			slog.Error("list presets failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to list presets")
			return
		}

		resp := make([]presetResponse, 0, len(presets))
		for _, p := range presets {
			resp = append(resp, presetToResponse(p))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createPresetHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		var body createPresetBody
		if !decodeJSONBody(w, r, &body) {
			return
		}

		var validName bool
		body.Name, validName = normalizePresetName(body.Name)
		if !validName {
			writeError(w, http.StatusBadRequest, "invalid_body", "name must be 1-60 characters")
			return
		}

		// Validate the embedded snapshot through the same pipeline as
		// PUT /api/settings so a preset can never store an invalid payload.
		settings := body.Settings
		normalizeSettings(&settings)
		if msg, ok := validateSettings(&settings); !ok {
			writeError(w, http.StatusBadRequest, "invalid", msg)
			return
		}
		if settings.FontRoles == nil {
			settings.FontRoles = map[string]fontRoleEntry{}
		}

		settingsBytes, err := json.Marshal(settings)
		if err != nil {
			slog.Error("marshal preset settings failed", "err", err)
			writeError(w, http.StatusInternalServerError, "server_error", "failed to encode preset")
			return
		}

		now := time.Now().UTC().Format(time.DateTime)
		rec := storage.PresetRecord{
			ID:           storage.GeneratePresetID(),
			UserID:       getUserID(r),
			Name:         body.Name,
			SettingsJSON: string(settingsBytes),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := pd.DB.InsertPresetContext(r.Context(), rec); err != nil {
			slog.Error("create preset failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to create preset")
			return
		}

		writeJSON(w, http.StatusCreated, presetToResponse(rec))
	}
}

func deletePresetHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		if err := pd.DB.DeletePresetContext(r.Context(), id, getUserID(r)); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "preset not found")
				return
			}
			slog.Error("delete preset failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete preset")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
