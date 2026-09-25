package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	// Generation identifies the book FILE the position was measured against.
	// A position is a chapter index into one spine, so a position computed
	// before a replace (POST /books/{id}/replace) does not mean the same place
	// in the new numbering -- and a reader tab left open across a replace would
	// otherwise persist the old index over the remapped row, undoing the remap.
	// The server reports the current generation on every read, and a write that
	// carries a different one is refused instead of applied. An absent value is
	// accepted: a client from before this field, or a beacon rebuilt from a
	// cache that predates it, still saves as it always did.
	Generation string `json:"generation,omitempty"`
}

// progressGeneration identifies one generation of a book's file. It is derived
// from the file hash rather than the books row's updated_at, which also moves
// for a title or cover edit that leaves every chapter index valid.
//
// Deliberately not the hash itself: the bare hash is the resource bearer token
// (resourceTokenForBook), while this value rides in ordinary progress bodies
// that the reader also keeps in localStorage. A prefix of the digest is enough
// to tell two generations apart and grants nothing.
func progressGeneration(fileHash string) string {
	if fileHash == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sayumi:progress-generation:" + fileHash))
	return hex.EncodeToString(sum[:8])
}

// staleGeneration reports whether a client's write was measured against a file
// generation that is no longer installed. A client that sends nothing, and a
// book with no hash to compare, both pass: this refuses positions known to be
// stale, it does not require clients to prove freshness.
func staleGeneration(clientGeneration, fileHash string) bool {
	current := progressGeneration(fileHash)
	return clientGeneration != "" && current != "" && clientGeneration != current
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
		book, ok := pd.Books.Get(bookID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		userID := getUserID(r)
		// Every read hands out the generation its position belongs to, so the
		// client can stamp its writes with it.
		generation := progressGeneration(book.FileHash)

		// Read-through: a just-staged position may not be flushed to the DB yet,
		// so prefer the coalescer's pending value to avoid returning a stale
		// position right after the client scrolled.
		if rec, ok := pd.Progress.get(bookID, userID); ok {
			resp := progressBody{
				Chapter: rec.Chapter, Percent: rec.Percent,
				UpdatedAt: rec.UpdatedAt, Generation: generation,
			}
			if rec.CFI.Valid {
				resp.CFI = rec.CFI.String
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		prog, err := pd.DB.GetProgressContext(r.Context(), bookID, userID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeJSON(w, http.StatusOK, progressBody{Chapter: 0, Percent: 0, Generation: generation})
				return
			}
			slog.Error("get progress failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to get progress")
			return
		}

		resp := progressBody{
			Chapter: prog.Chapter, Percent: prog.Percent,
			UpdatedAt: prog.UpdatedAt, Generation: generation,
		}
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

		// The book file was replaced under this client: its chapter index means
		// a different place now, and the stored position has already been
		// remapped (storage.ReplaceBookFileContext). Refusing is the whole point
		// -- writing would undo that remap with a position nobody is reading.
		if staleGeneration(body.Generation, book.FileHash) {
			writeError(w, http.StatusConflict, "stale_generation",
				"this book's file was replaced; reopen it to keep saving progress")
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
		body.Generation = progressGeneration(book.FileHash)
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
		// Same refusal as the PUT path; a beacon has no reader to tell.
		if staleGeneration(body.Generation, book.FileHash) {
			slog.Info("dropped beaconed progress from a replaced book file", "book", bookID)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		pd.Progress.stage(toProgressRecord(bookID, getUserID(r), body))

		w.WriteHeader(http.StatusNoContent)
	}
}
