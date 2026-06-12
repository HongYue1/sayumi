package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"sayumi/internal/storage"
)

type progressBody struct {
	Chapter int     `json:"chapter"`
	Percent float64 `json:"percent"`
	CFI     string  `json:"cfi,omitempty"`
}

func getUserID(_ *http.Request) string { return "default" }

func getProgressHandler(_ *Dependencies) http.HandlerFunc {
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

		prog, err := pd.DB.GetProgressContext(r.Context(), bookID, getUserID(r))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusOK, progressBody{Chapter: 0, Percent: 0})
				return
			}
			slog.Error("get progress failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to get progress")
			return
		}

		resp := progressBody{Chapter: prog.Chapter, Percent: prog.Percent}
		if prog.CFI.Valid {
			resp.CFI = prog.CFI.String
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func validateProgress(body progressBody, chapterCount int) string {
	if body.Chapter < 0 {
		return "chapter must be >= 0"
	}
	if chapterCount <= 0 || body.Chapter >= chapterCount {
		return "chapter index out of range"
	}
	if body.Percent < 0 || body.Percent > 1.0 {
		return "percent must be 0-1"
	}
	return ""
}

func toProgressRecord(bookID, userID string, body progressBody) storage.ProgressRecord {
	record := storage.ProgressRecord{
		BookID:  bookID,
		UserID:  userID,
		Chapter: body.Chapter,
		Percent: body.Percent,
	}
	if body.CFI != "" {
		record.CFI = sql.NullString{String: body.CFI, Valid: true}
	}
	return record
}

func putProgressHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		book, ok := pd.Books.Get(bookID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		var body progressBody
		if !decodeJSONBody(w, r, &body) {
			return
		}

		if msg := validateProgress(body, book.ChapterCount); msg != "" {
			writeError(w, http.StatusBadRequest, "invalid_body", msg)
			return
		}

		if err := pd.DB.SaveProgressContext(r.Context(), toProgressRecord(bookID, getUserID(r), body)); err != nil {
			slog.Error("save progress failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to save progress")
			return
		}

		writeJSON(w, http.StatusOK, body)
	}
}

func beaconProgressHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		book, ok := pd.Books.Get(bookID)
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var body progressBody
		if !decodeJSONBody(w, r, &body) {
			// beaconProgress is best-effort; the client never reads the response.
			// decodeJSONBody already wrote an error status; just return.
			return
		}
		if msg := validateProgress(body, book.ChapterCount); msg != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if err := pd.DB.SaveProgressContext(r.Context(), toProgressRecord(bookID, getUserID(r), body)); err != nil {
			slog.Error("beacon progress save failed", "book", bookID, "err", err)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
