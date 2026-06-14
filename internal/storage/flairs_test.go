package storage

import (
	"context"
	"testing"
)

func TestDB_FlairExistsContext(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()

	if err := db.InsertFlairContext(ctx, FlairRecord{
		ID: "flair_known", UserID: "default", Label: "Favorite", Color: "#abc",
	}); err != nil {
		t.Fatalf("seed flair: %v", err)
	}

	tests := []struct {
		name   string
		id     string
		userID string
		want   bool
	}{
		{"existing flair for user", "flair_known", "default", true},
		{"unknown id", "flair_missing", "default", false},
		{"existing id but wrong user", "flair_known", "other", false},
		{"empty id", "", "default", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.FlairExistsContext(ctx, tc.id, tc.userID)
			if err != nil {
				t.Fatalf("FlairExistsContext: %v", err)
			}
			if got != tc.want {
				t.Errorf("FlairExistsContext(%q, %q) = %v, want %v", tc.id, tc.userID, got, tc.want)
			}
		})
	}
}
