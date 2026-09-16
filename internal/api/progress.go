package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"sayumi/internal/storage"
)

type progressBody struct {
	Chapter int     `json:"chapter"`
	Percent float64 `json:"percent"`
	CFI     string  `json:"cfi,omitempty"`
	// UpdatedAt is server-owned: the instant the answered position was recorded,
	// on this server's clock. It is ignored on write (storage.SaveProgressContext
	// deliberately stamps its own, so a client cannot dictate a position's age),
	// and an unread book carries none. The reader keeps the newest value it has
	// been told and compares it against the one stored with its page-hide cache
	// to decide which of the two is actually newer -- without it, a tab that had
	// merely been hidden rewound whatever another client had read since
	// (frontend/src/lib/progress.ts).
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// getUserID returns the single-user id. Every profile is single-user today,
// so the request is ignored and the id is constant; the parameter keeps the
// seam where per-request user resolution will plug in for multi-user.
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

		userID := getUserID(r)

		// Read-through: a just-staged position may not be flushed to the DB yet,
		// so prefer the coalescer's pending value to avoid returning a stale
		// position right after the client scrolled.
		if rec, ok := pd.Progress.get(bookID, userID); ok {
			resp := progressBody{Chapter: rec.Chapter, Percent: rec.Percent, UpdatedAt: rec.UpdatedAt}
			if rec.CFI.Valid {
				resp.CFI = rec.CFI.String
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		prog, err := pd.DB.GetProgressContext(r.Context(), bookID, userID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeJSON(w, http.StatusOK, progressBody{Chapter: 0, Percent: 0})
				return
			}
			slog.Error("get progress failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to get progress")
			return
		}

		resp := progressBody{Chapter: prog.Chapter, Percent: prog.Percent, UpdatedAt: prog.UpdatedAt}
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

		// Stage into the per-profile coalescer instead of writing synchronously.
		// The write is flushed on a short timer, collapsing the frequent scroll
		// updates for one book into a single WAL commit.
		record := toProgressRecord(bookID, getUserID(r), body)
		// Stamp here instead of leaving it to stage(), so the response reports the
		// same instant the read path will report for this position while it is
		// still pending. The reader keeps it as the baseline for its page-hide
		// cache; with no stamp in the response that baseline would stay at boot
		// time and make the server look newer than the position it holds.
		record.UpdatedAt = time.Now().UTC().Format(time.DateTime)
		pd.Progress.stage(record)

		body.UpdatedAt = record.UpdatedAt
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

		pd.Progress.stage(toProgressRecord(bookID, getUserID(r), body))

		w.WriteHeader(http.StatusNoContent)
	}
}
