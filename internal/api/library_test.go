package api

import "testing"

func books() []BookResponse {
	return []BookResponse{
		{ID: "1", Title: "Beta", Author: "Zane", Progress: 0.5, AddedAt: "2026-01-02", LastReadAt: "2026-03-01"},
		{ID: "2", Title: "alpha", Author: "Adams", Progress: 0.9, AddedAt: "2026-01-03", LastReadAt: "2026-02-01"},
		{ID: "3", Title: "Gamma", Author: "Moore", Progress: 0.1, AddedAt: "2026-01-01", LastReadAt: "2026-04-01"},
	}
}

func ids(bs []BookResponse) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterAndSortBooks(t *testing.T) {
	tests := []struct {
		name           string
		q, sort, order string
		want           []string
	}{
		{"default is title asc, case-insensitive", "", "", "", []string{"2", "1", "3"}},
		{"title desc", "", "title", "desc", []string{"3", "1", "2"}},
		{"author asc", "", "author", "asc", []string{"2", "3", "1"}},
		{"added desc (newest first)", "", "added", "desc", []string{"2", "1", "3"}},
		{"read desc (most recent first)", "", "read", "desc", []string{"3", "1", "2"}},
		{"progress desc", "", "progress", "desc", []string{"2", "1", "3"}},
		{"query filters title", "alpha", "", "", []string{"2"}},
		{"query filters author, case-insensitive", "moore", "", "", []string{"3"}},
		{"query with no match", "zzz", "", "", []string{}},
		{"unknown sort falls back to title", "", "bogus", "", []string{"2", "1", "3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ids(filterAndSortBooks(books(), tc.q, tc.sort, tc.order))
			if !eq(got, tc.want) {
				t.Errorf("filterAndSortBooks(q=%q sort=%q order=%q) = %v, want %v",
					tc.q, tc.sort, tc.order, got, tc.want)
			}
		})
	}
}

func TestFilterAndSortBooksDoesNotMutateOnEmptyQuery(t *testing.T) {
	in := books()
	_ = filterAndSortBooks(in, "", "title", "asc")
	if in[0].ID != "1" {
		t.Errorf("input slice order changed unexpectedly: first ID = %q", in[0].ID)
	}
}
