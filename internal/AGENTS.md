# internal/AGENTS.md

Backend packages. The reasoning for individual decisions is in comments at each site —
read them before editing that code.

## Boundaries

- `api` owns HTTP: handlers, auth, middleware, per-profile dependency wiring.
- `storage` owns SQLite and the in-memory `BookCache`. No HTTP types.
- `library` owns the filesystem: scan, import, dedup, covers.
- `epub` owns parsing, sanitizing, and search over untrusted book files.
- `fonts` owns bundled and user font serving.

Handlers don't reach past `storage`'s API into SQL, and `storage` never imports `api`.

## Invariants

- **Profiles are isolated.** Every authenticated handler takes its `*profileDeps` from the
  request context (`requireProfileDeps`) and releases the ref exactly once
  (`defer pd.release()`). That struct is shared across concurrent requests — never write
  per-request state into it.
- **`bookReplaceMu` pairs a cache snapshot with one on-disk EPUB generation.** Chapter
  render, search, downloads, in-place edits, and delete all take part. Readers hold the
  read side for as long as they use the file or spine; a replacement holds the write side
  only across `TryCloseForReplace` → rename → DB/cache refresh.
- **`BookCache.GetSpine` returns a shared slice** — read-only for every caller: no sort,
  no in-place mutation, no retained append. Same for `fonts.Scanner.Families()`.
- **Library title ordering is a three-place contract** across `storage/books.go`,
  `storage/bookcache.go`, and `api/library.go`. See the root `AGENTS.md`.
- **Search offsets depend on `foldRunes`** (per-rune `unicode.ToLower`).
  `strings.ToLower` can expand one rune into two and corrupt every offset after it.
- **The progress coalescer must drain before its DB closes:** `stop()` in `closeProfile`
  after refs reach zero, before `DB.Close()`. Deleting a book must also drop its pending
  entry, or a staged write retries forever against a cascaded-away row.
- **Traversal of untrusted EPUB content stays depth-bounded and fail-closed**, mutating or
  not. Remote subresources resolve to `about:invalid`; local ones go through the book's
  resources endpoint.
- **`/resources` and `/fonts/user` authorize by bearer token in constant time**, not by
  session — they bypass `applyAuth` on purpose. So do profile clone/delete, which would
  otherwise deadlock against their own profile lock.
- **gofile is the only outbound call.** Per-request opt-in, anonymous, and the
  gofile-supplied server name must be validated before it is used as a host.
- **A cover or metadata edit bumps `books.updated_at`.** The cover ETag folds it and the
  client busts cache with `?v=updatedAt`. All three move together, or an edited cover
  serves stale.

## Testing

`go test ./...`, with `-race` when cgo is available. `internal/library` is the slow package
(~20s). Tests resolve `filepath.EvalSymlinks(t.TempDir())` before comparing paths, because
SQLite reports the resolved path and macOS maps `/var` → `/private/var`.
