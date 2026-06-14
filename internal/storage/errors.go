package storage

import "errors"

// ErrNotFound is the storage layer's domain sentinel for "no matching row".
//
// Getters and mutating operations return it (instead of leaking
// database/sql.ErrNoRows to callers) so the rest of the codebase has a single,
// stable not-found contract to match with errors.Is.
var ErrNotFound = errors.New("storage: not found")
