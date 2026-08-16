# AGENTS.md

Sayumi is a single-binary EPUB reader: a Go HTTP server (`cmd/sayumi`, `internal/`) that
embeds a built Solid 2 SPA (`frontend/`) and serves it via `//go:embed dist`. Local-first
— the only outbound request in the entire binary is the opt-in gofile.io share.

`CLAUDE.md` only points here. If an agent you use doesn't read `AGENTS.md` natively, give
it a one-line pointer file of its own rather than a second copy of these rules.

## Where the reasoning lives

**In the code, next to the thing it explains.** This repo comments the *why*, not the
*what*, and those comments are load-bearing: `storage/db.go` says why the progress upsert
is a prepared statement, `bookcache.go` says why folding stays ASCII-only, `cover.go` says
why concurrent cover decodes are capped. Read the comment before changing the line, and
update it in the same commit.

**Never create an aggregated rationale or code-review document** — no `docs/decisions/`,
`REVIEW.md`, `NOTES.md`, or dated findings log, in any directory. One existed and was
deleted: every claim in it already had a better comment at the call site, so it was a
second place to update, and it drifted. A document that restates code loses to the code,
and it burns the reader's attention before they reach the code.

A review finding goes into a comment at the site it concerns, or into a test if it could
regress, or into the commit message. It belongs in an `AGENTS.md` only when it spans files
and therefore has no single home — as a pointer, not a retelling.

**These files are instructions, not history.** No benchmark tables or before/after numbers
(if a measurement justifies a line of code, it belongs in that line's comment), no session
logs, no dates, no "recently changed". A rejected approach gets one line under *Settled*
below — `X: rejected — why` — not a postmortem. If a section here can be deleted without
losing a rule, delete it.

## Before you finish

`make check` must pass. Ten gates: frontend build, `go mod tidy -diff`, gofumpt +
goimports, prettier, `go vet`, golangci-lint, govulncheck, `go test` (with `-race` when
cgo is available), `bun run check` + `bun run test`, then both builds. Don't hand back work
that hasn't passed it.

- `make fix` formats. `make build` produces `./sayumi`.
- Frontend commands run under **bun**, from `frontend/`.
- The Go build stays `CGO_ENABLED=0`-clean — pure-Go SQLite (`modernc.org/sqlite`).
- depguard runs in strict mode: stdlib, `sayumi`,
  `golang.org/x/{crypto,image,net,sync}`, `modernc.org/sqlite`. A new dependency is a
  decision, not a convenience.

## Cross-file contracts

Nothing enforces these but a reviewer. Grep both sides before changing either.

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

## Settled — don't re-propose

- Startup path index for embedded assets: **rejected** — faster misses, slower real asset
  hits and app-shell fallback.
- LRU on the parsed-spine memo: **rejected** — retention is bounded by library size, and
  eviction would add a re-parse to the chapter-open path.
- Prepared statements for library/book reads: **rejected** — they run once per load, not
  once per UI event.
- Unicode-aware folding in `BookCache`: **rejected** — it must match SQLite `NOCASE`.
- WebP/AVIF cover output: **rejected** — no pure-Go encoder, and the build stays
  `CGO_ENABLED=0`.
- `ParseMultipartForm` for uploads: **rejected** — it buffers the whole EPUB; staging
  streams it.
- One generic middleware chain: **rejected** — instrumentation specializes at handler
  construction instead.

## Conventions

- Conventional commits with a package scope: backend `api|epub|fonts|library|storage`,
  frontend `reader|library|iframe|api|lib`, tooling `ci|build|release|make|lint`.
- Go: gofumpt, imports grouped with `-local sayumi`. Frontend: prettier (spaces, width 80,
  double quotes).
- Solid 2 (signals, split effects), TypeScript, plain CSS.
- Tests live beside the code. Fix a bug, add the test that would have caught it.

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
