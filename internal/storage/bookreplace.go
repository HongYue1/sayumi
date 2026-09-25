package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ChapterMove says where one chapter of the previous file generation lives in
// the replacement. Index < 0 means the chapter has no counterpart. KeepAnchor
// is true only when the same spine document was matched: reader anchors (CFI)
// are element paths inside one chapter document, so resolving one against a
// different document lands on an arbitrary element instead of failing over to
// the chapter-relative percent.
type ChapterMove struct {
	Index      int
	KeepAnchor bool
}

// ChapterRemap maps every chapter index of the previous generation (the slice
// index) to its place in the replacement. NewCount is the replacement's
// chapter count and bounds every fallback position.
type ChapterRemap struct {
	Moves    []ChapterMove
	NewCount int
}

// Position is one stored reading position: progress or a bookmark. Percent is
// chapter-relative (0-1), CFI an opaque in-chapter anchor.
type Position struct {
	Chapter int
	Percent float64
	CFI     sql.NullString
}

// Apply moves pos into the replacement generation. A matched chapter keeps its
// percent and anchor. An unmatched one keeps its index (clamped into range) and
// its chapter-relative percent, but drops the anchor so the reader falls back
// to percent. A position past the new end parks at the end of the last chapter:
// the reader resumes where the shorter book ends instead of at an index that
// the progress validator would reject.
func (m ChapterRemap) Apply(pos Position) Position {
	if m.NewCount <= 0 {
		return Position{}
	}
	if pos.Chapter >= 0 && pos.Chapter < len(m.Moves) {
		if move := m.Moves[pos.Chapter]; move.Index >= 0 && move.Index < m.NewCount {
			out := Position{Chapter: move.Index, Percent: pos.Percent}
			if move.KeepAnchor {
				out.CFI = pos.CFI
			}
			return out
		}
	}
	switch {
	case pos.Chapter < 0:
		return Position{}
	case pos.Chapter >= m.NewCount:
		return Position{Chapter: m.NewCount - 1, Percent: 1}
	default:
		return Position{Chapter: pos.Chapter, Percent: pos.Percent}
	}
}

// StampAfter returns now in the progress timestamp layout (UTC, second
// resolution), advanced to one second past prev when the clock has not moved
// beyond it. A remapped position must look strictly newer than every baseline
// a client was already given: the reader's page-hide cache wins at boot unless
// the server's updated_at is strictly later than the one it recorded, and that
// cache still holds a chapter index in the previous numbering.
func StampAfter(prev string, now time.Time) string {
	now = now.UTC().Truncate(time.Second)
	if t, err := time.Parse(time.DateTime, prev); err == nil && !now.After(t) {
		now = t.Add(time.Second)
	}
	return now.Format(time.DateTime)
}

// remapProgressStampSQL is StampAfter(updated_at, now) in SQL; SQLite's
// datetime() emits the same layout as time.DateTime.
const remapProgressStampSQL = `CASE WHEN datetime('now') > updated_at
	THEN datetime('now') ELSE datetime(updated_at, '+1 second') END`

// BookFileReplacement is the parsed description of a new EPUB generation
// installed under an existing book ID. Title and author are deliberately
// absent: they are the user's library identity and survive a replacement.
type BookFileReplacement struct {
	Language    string
	Publisher   string
	Description string
	PubDate     string
	ISBN        string
	Direction   string
	FileHash    string
	FileSize    int64
	SpineJSON   string
	TocJSON     string
	Remap       ChapterRemap
}

// ReplaceBookFileContext points an existing book row at a new EPUB generation
// and moves every stored reading position (progress for all users, and all
// bookmarks) into the new chapter numbering, in one transaction. Positions are
// keyed by chapter index, so committing the new spine without remapping them
// would silently re-aim progress and bookmarks at different chapters whenever
// the replacement inserts, removes, or reorders spine items. A progress row
// that moves also gets a strictly later updated_at (see StampAfter).
//
// A book without a cover is re-armed for the scanner's cover backfill so the
// new file's cover can be picked up; an existing (possibly user-uploaded) cover
// is left alone. updated_at advances like every other file edit, rotating the
// chapter/detail ETags and resource token together with file_hash.
func (db *DB) ReplaceBookFileContext(ctx context.Context, id string, rep BookFileReplacement) error {
	if rep.Remap.NewCount <= 0 {
		return errors.New("replace book file: replacement has no chapters")
	}

	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	if err := db.assertFileHashFree(ctx, id, rep.FileHash); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // A completed transaction is already closed.

	res, err := tx.ExecContext(ctx, `
		UPDATE books SET language = ?, publisher = ?, description = ?, pub_date = ?, isbn = ?,
		       direction = ?, file_hash = ?, file_size = ?, spine_json = ?, toc_json = ?,
		       chapter_count = ?,
		       cover_checked = CASE WHEN has_cover = 1 THEN cover_checked ELSE 0 END,
		       updated_at = `+bookEditVersionSQL+`
		WHERE id = ?
	`,
		rep.Language, rep.Publisher, rep.Description, rep.PubDate, rep.ISBN,
		rep.Direction, rep.FileHash, rep.FileSize, rep.SpineJSON, rep.TocJSON,
		rep.Remap.NewCount, id,
	)
	if err != nil {
		if mapped := mapFileHashConflict(err); errors.Is(mapped, ErrFileHashConflict) {
			return ErrFileHashConflict
		}
		return fmt.Errorf("replace file for %s: %w", id, err)
	}
	if err := rowsAffectedOrNotFound(res, "replace file for "+id); err != nil {
		return err
	}

	if err := remapPositionsTx(ctx, tx, rep.Remap,
		"SELECT user_id, chapter, percent, cfi FROM progress WHERE book_id = ?",
		"UPDATE progress SET chapter = ?, percent = ?, cfi = ?, updated_at = "+remapProgressStampSQL+" WHERE book_id = ? AND user_id = ?",
		id,
	); err != nil {
		return fmt.Errorf("remap progress for %s: %w", id, err)
	}
	if err := remapPositionsTx(ctx, tx, rep.Remap,
		"SELECT id, chapter, percent, cfi FROM bookmarks WHERE book_id = ?",
		"UPDATE bookmarks SET chapter = ?, percent = ?, cfi = ? WHERE book_id = ? AND id = ?",
		id,
	); err != nil {
		return fmt.Errorf("remap bookmarks for %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace file for %s: %w", id, err)
	}
	return nil
}

// remapPositionsTx rewrites the positions selected by selectSQL (key, chapter,
// percent, cfi) through remap. Rows are read in full before any UPDATE: a
// transaction owns one connection, which cannot interleave an open result set
// with another statement.
func remapPositionsTx(ctx context.Context, tx *sql.Tx, remap ChapterRemap, selectSQL, updateSQL, bookID string) error {
	all, err := readKeyedPositionsTx(ctx, tx, selectSQL, bookID)
	if err != nil {
		return err
	}
	for _, k := range all {
		next := remap.Apply(k.pos)
		if next == k.pos {
			continue
		}
		if _, err := tx.ExecContext(ctx, updateSQL, next.Chapter, next.Percent, next.CFI, bookID, k.key); err != nil {
			return fmt.Errorf("update position %s: %w", k.key, err)
		}
	}
	return nil
}

type keyedPosition struct {
	key string
	pos Position
}

func readKeyedPositionsTx(ctx context.Context, tx *sql.Tx, selectSQL, bookID string) (all []keyedPosition, err error) {
	rows, err := tx.QueryContext(ctx, selectSQL, bookID)
	if err != nil {
		return nil, fmt.Errorf("select positions: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close positions: %w", closeErr)
		}
	}()
	for rows.Next() {
		var k keyedPosition
		if err := rows.Scan(&k.key, &k.pos.Chapter, &k.pos.Percent, &k.pos.CFI); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		all = append(all, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read positions: %w", err)
	}
	return all, nil
}
