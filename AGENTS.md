# AGENTS.md

Sayumi is a single-binary EPUB reader: a Go HTTP server (`cmd/sayumi`, `internal/`) that
embeds a built Solid 2 SPA (`frontend/`) and serves it via `//go:embed dist`. Local-first
— the only outbound request in the entire binary is the opt-in gofile.io share.

`CLAUDE.md` only points here. If an agent you use doesn't read `AGENTS.md` natively, give
it a one-line pointer file of its own rather than a second copy of these rules.

## Working rules

- Before making changes, create a fresh, descriptive branch for the task from the
  agreed starting point. For a continuation, use the branch already created for
  that work. Never edit or commit directly on `main` or `master`; do not reuse an
  unrelated branch, reset somebody else's work, merge, amend, or push without
  explicit authorization.
- Use Git status, log, and the intended diff for routine state checks. Preserve
  unrelated work; do not create redundant file-checksum inventories or byte audits.
- Check the available skills under `.skills/` and read the relevant `SKILL.md`
  files and linked references before applying their guidance. Do not bulk-read
  unrelated skills; project-specific contracts take precedence over generic advice.
- Verify uncertain APIs, flags, configuration formats, and version compatibility
  using repository documentation, local tool/API schemas, version-matched official
  online documentation, CLI `--help`, or a focused experiment. Do not guess. If
  verification is unavailable, state the uncertainty and its consequences.

## Development checks

Application and build/configuration changes must pass `make check`. Its ten gates
cover frontend build, `go mod tidy -diff`, gofumpt + goimports, oxfmt, `go vet`,
golangci-lint, govulncheck, `go test` (with `-race` when cgo is available),
`bun run check` + `bun run test`, then both builds. Do not claim a required check
passed if its tool or gate was skipped.

Documentation-only changes do not require rerunning unchanged application checks.

- `make fix` formats. `make build` produces `./sayumi`.
- Frontend commands run under **bun**, from `frontend/`.
- The Go build stays `CGO_ENABLED=0`-clean — pure-Go SQLite (`modernc.org/sqlite`).
- depguard runs in strict mode: stdlib, `sayumi`,
  `golang.org/x/{crypto,image,net,sync}`, `modernc.org/sqlite`. A new dependency is a
  decision, not a convenience.

## Cross-file contracts

These contracts span files. Inspect both sides before changing either.

- `internal/api/flairs.go` ↔ `frontend/src/lib/flairs.ts` (the Go side carries a
  `KEEP IN SYNC` marker).
- `frontend/src/lib/themes.ts` ↔ `frontend/src/iframe/frame.css` — each theme's chrome
  background must equal that theme's reader `--bg-primary`.
- **Library title ordering is one contract in three places:**
  `ORDER BY title COLLATE NOCASE ASC, id ASC` (`storage/books.go`), `BookCache`'s ASCII
  fold plus `id` tie-break (`storage/bookcache.go`), and `filterAndSortBooks`' lowercased
  keys (`api/library.go`). The cache is seeded from the query's row order and extended by
  binary search, and the API sort is *stable* — so a divergence yields silently wrong
  order, not an error. The API layer folds Unicode where the other two fold ASCII;
  deliberate, don't unify it.
- **`ORDER BY` ↔ `idx_books_title_sort`** (`storage/db.go`) must match column for column,
  collation included, or the planner drops the index and sorts into a temp B-tree while
  still paying its write cost. `TestListBookSummariesUsesTitleSortIndex` is the only thing
  that fails.

## Conventions

- Conventional commits with a package scope: backend `api|epub|fonts|library|storage`,
  frontend `reader|library|iframe|api|lib`, tooling `ci|build|release|make|lint`.
- Go: gofumpt, imports grouped with `-local sayumi`. Frontend: oxfmt (spaces, width 80,
  double quotes).
- Solid 2 (signals, split effects), TypeScript, plain CSS.
- Tests live beside the code. Fix a bug, add the test that would have caught it.
- Comments explain the *why*. Read existing rationale before changing the code,
  and update the explanation with the implementation rather than duplicating it.

## Layout

| Path                  | What it is                                                  |
| --------------------- | ----------------------------------------------------------- |
| `cmd/sayumi`          | main, console UI, embedded SPA, pretty logger, debug/pprof   |
| `internal/api`        | HTTP handlers, auth, middleware, per-profile deps            |
| `internal/storage`    | SQLite (modernc), migrations, book cache                     |
| `internal/library`    | filesystem scan, import, covers                              |
| `internal/epub`       | EPUB parsing, sanitizing, search                             |
| `internal/fonts`      | bundled and user font serving                                |
| `frontend/src`        | SPA: `routes/`, `lib/`, `components/`                        |
| `frontend/src/iframe` | reader engine, runs inside the `srcdoc` iframe               |
