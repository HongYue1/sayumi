package storage

import (
	"context"
	"strings"
	"testing"
)

// TestListBookSummariesUsesTitleSortIndex pins the query plan of the library
// list query. idx_books_title_sort only pays for itself if SQLite actually walks
// it instead of sorting into a temp B-tree, and none of that is observable in
// the query's results: drop COLLATE NOCASE from either side, reorder the index
// to (id, title), or prepend an ORDER BY column, and the list keeps returning
// correct rows while the sort silently goes back to costing a full temp B-tree
// per library load -- with the index still charged on every insert.
//
// Two details keep this test honest. It EXPLAINs listBookSummariesQuery itself
// rather than a copy, which cannot drift from production. And it seeds rows and
// uses the full 17-column projection: a narrower SELECT reports a covering-index
// plan that the real query can never reach.
func TestListBookSummariesUsesTitleSortIndex(t *testing.T) {
	db := newTestDB(t)
	seedBooks(t, db, 200)

	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+listBookSummariesQuery)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			t.Errorf("close plan rows: %v", cerr)
		}
	}()

	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}

	got := plan.String()
	if !strings.Contains(got, "idx_books_title_sort") {
		t.Errorf("library list query does not use idx_books_title_sort; plan:\n%s", got)
	}
	if strings.Contains(strings.ToUpper(got), "TEMP B-TREE") {
		t.Errorf("library list query still sorts into a temp B-tree; plan:\n%s", got)
	}
}
