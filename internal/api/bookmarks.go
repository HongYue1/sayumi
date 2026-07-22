package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"sayumi/internal/storage"
)

const maxBookmarkTextLen = 2000

type bookmarkBody struct {
	Chapter int     `json:"chapter"`
	Percent float64 `json:"percent"`
	CFI     string  `json:"cfi,omitempty"`
	Label   string  `json:"label,omitempty"`
	Comment string  `json:"comment,omitempty"`
}

type bookmarkUpdateBody struct {
	Label   string `json:"label"`
	Comment string `json:"comment"`
}

type bookmarkResponse struct {
	ID        string  `json:"id"`
	Chapter   int     `json:"chapter"`
	Percent   float64 `json:"percent"`
	CFI       string  `json:"cfi,omitempty"`
	Label     string  `json:"label"`
	Comment   string  `json:"comment"`
	CreatedAt string  `json:"createdAt"`
}

func bookmarkToResponse(b storage.BookmarkRecord) bookmarkResponse {
	resp := bookmarkResponse{
		ID:        b.ID,
		Chapter:   b.Chapter,
		Percent:   b.Percent,
		Label:     b.Label,
		Comment:   b.Comment,
		CreatedAt: b.CreatedAt,
	}
	if b.CFI.Valid {
		resp.CFI = b.CFI.String
	}
	return resp
}

func listBookmarksHandler(_ *Dependencies) http.HandlerFunc {
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

		bookmarks, err := pd.DB.ListBookmarksContext(r.Context(), bookID, getUserID(r))
		if err != nil {
			slog.Error("list bookmarks failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to list bookmarks")
			return
		}

		resp := make([]bookmarkResponse, 0, len(bookmarks))
		for _, bookmark := range bookmarks {
			resp = append(resp, bookmarkToResponse(bookmark))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func createBookmarkHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		userID := getUserID(r)

		book, ok := pd.Books.Get(bookID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}

		var body bookmarkBody
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if body.Chapter < 0 {
			writeError(w, http.StatusBadRequest, "invalid_body", "chapter must be >= 0")
			return
		}
		if book.ChapterCount <= 0 || body.Chapter >= book.ChapterCount {
			writeError(w, http.StatusBadRequest, "invalid_body", "chapter index out of range")
			return
		}
		if body.Percent < 0 || body.Percent > 1.0 {
			writeError(w, http.StatusBadRequest, "invalid_body", "percent must be 0-1")
			return
		}
		if len(body.Label) > maxBookmarkTextLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "label too long")
			return
		}
		if len(body.Comment) > maxBookmarkTextLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "comment too long")
			return
		}

		createdAt := time.Now().UTC().Format(time.DateTime)
		bookmarkID := storage.GenerateBookmarkID()
		record := storage.BookmarkRecord{
			ID:        bookmarkID,
			BookID:    bookID,
			UserID:    userID,
			Chapter:   body.Chapter,
			Percent:   body.Percent,
			Label:     body.Label,
			Comment:   body.Comment,
			CreatedAt: createdAt,
		}
		if body.CFI != "" {
			record.CFI = sql.NullString{String: body.CFI, Valid: true}
		}

		if err := pd.DB.InsertBookmarkContext(r.Context(), record); err != nil {
			slog.Error("create bookmark failed", "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to create bookmark")
			return
		}

		writeJSON(w, http.StatusCreated, bookmarkResponse{
			ID:        bookmarkID,
			Chapter:   body.Chapter,
			Percent:   body.Percent,
			CFI:       body.CFI,
			Label:     body.Label,
			Comment:   body.Comment,
			CreatedAt: createdAt,
		})
	}
}

func updateBookmarkHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		bookmarkID := r.PathValue("bid")
		userID := getUserID(r)

		existing, err := pd.DB.GetBookmarkContext(r.Context(), bookmarkID, userID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "bookmark not found")
				return
			}
			slog.Error("load bookmark failed", "bookmark", bookmarkID, "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to load bookmark")
			return
		}
		if existing.BookID != bookID {
			writeError(w, http.StatusNotFound, "not_found", "bookmark not found")
			return
		}

		var body bookmarkUpdateBody
		if !decodeJSONBody(w, r, &body) {
			return
		}
		if len(body.Label) > maxBookmarkTextLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "label too long")
			return
		}
		if len(body.Comment) > maxBookmarkTextLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "comment too long")
			return
		}

		if err := pd.DB.UpdateBookmarkContext(
			r.Context(), bookmarkID, bookID, userID, body.Label, body.Comment,
		); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "bookmark not found")
				return
			}
			slog.Error("update bookmark failed", "bookmark", bookmarkID, "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to update bookmark")
			return
		}

		existing.Label = body.Label
		existing.Comment = body.Comment
		writeJSON(w, http.StatusOK, bookmarkToResponse(existing))
	}
}

func deleteBookmarkHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		bookID := r.PathValue("id")
		bookmarkID := r.PathValue("bid")
		userID := getUserID(r)

		if err := pd.DB.DeleteBookmarkContext(r.Context(), bookmarkID, bookID, userID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "bookmark not found")
				return
			}
			slog.Error("delete bookmark failed", "bookmark", bookmarkID, "book", bookID, "err", err)
			writeError(w, http.StatusInternalServerError, "db_error", "failed to delete bookmark")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
