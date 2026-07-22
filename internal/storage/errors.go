package storage

import "errors"

// ErrNotFound is the storage layer's domain sentinel for "no matching row".
//
// Getters and mutating operations return it (instead of leaking
// database/sql.ErrNoRows to callers) so the rest of the codebase has a single,
// stable not-found contract to match with errors.Is.
var ErrNotFound = errors.New("storage: not found")

// ErrFileHashConflict is returned by the in-place-edit update methods when the
// recomputed file_hash already belongs to a different book row. It guards the
// partial unique index idx_books_file_hash_uniq so a (astronomically unlikely)
// collision surfaces as a clean 409 rather than a raw constraint-violation 500.
var ErrFileHashConflict = errors.New("storage: file hash conflicts with another book")
