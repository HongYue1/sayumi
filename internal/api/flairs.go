package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"sayumi/internal/storage"
)

const maxFlairLabelLen = 40

// Hex color like #abc or #aabbcc.
var hexColorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type flairResponse struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Color string `json:"color"`
}

type createFlairBody struct {
	Label string `json:"label"`
	Color string `json:"color"`
}

type setBookFlairBody struct {
	// FlairID is the flair to assign; null or empty clears the assignment.
	FlairID *string `json:"flairId"`
}

func flairToResponse(f storage.FlairRecord) flairResponse {
	return flairResponse{ID: f.ID, Label: f.Label, Color: f.Color}
}

func listFlairsHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		flairs, err := pd.DB.ListFlairsContext(r.Context(), getUserID(r))
		if err != nil {
			slog.Error("list flairs failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to list flairs")
			return
		}

		resp := make([]flairResponse, 0, len(flairs))
		for _, f := range flairs {
			resp = append(resp, flairToResponse(f))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createFlairHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		var body createFlairBody
		if !decodeJSONBody(w, r, &body) {
			return
		}
		body.Label = strings.TrimSpace(body.Label)
		body.Color = strings.TrimSpace(body.Color)
		if body.Label == "" || len(body.Label) > maxFlairLabelLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "label must be 1-40 characters")
			return
		}
		if !hexColorPattern.MatchString(body.Color) {
			writeError(w, http.StatusBadRequest, "invalid_body", "color must be a hex value like #3b82f6")
			return
		}

		rec := storage.FlairRecord{
			ID:     storage.GenerateFlairID(),
			UserID: getUserID(r),
			Label:  body.Label,
			Color:  body.Color,
		}
		if err := pd.DB.InsertFlairContext(r.Context(), rec); err != nil {
			slog.Error("create flair failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to create flair")
			return
		}
		writeJSON(w, http.StatusCreated, flairToResponse(rec))
	}
}

func deleteFlairHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		if err := pd.DB.DeleteFlairContext(r.Context(), id, getUserID(r)); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "flair not found")
				return
			}
			slog.Error("delete flair failed", "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete flair")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func setBookFlairHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		if _, ok := pd.Books.Get(bookID); !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		var body setBookFlairBody
		if !decodeJSONBody(w, r, &body) {
			return
		}

		flairID := ""
		if body.FlairID != nil {
			flairID = strings.TrimSpace(*body.FlairID)
		}
		if len(flairID) > 128 {
			writeError(w, http.StatusBadRequest, "invalid_body", "flairId too long")
			return
		}

		userID := getUserID(r)
		if flairID != "" {
			exists, err := pd.DB.FlairExistsContext(r.Context(), flairID, userID)
			if err != nil {
				slog.Error("check flair failed", "flair", flairID, "err", err)
				writeError(w, http.StatusInternalServerError, "db_error", "failed to set flair")
				return
			}
			if !exists {
				writeError(w, http.StatusNotFound, "not_found", "flair not found")
				return
			}
		}

		if err := pd.DB.SetBookFlairContext(r.Context(), bookID, userID, flairID); err != nil {
			slog.Error("set book flair failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to set flair")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
