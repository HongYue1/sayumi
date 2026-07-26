# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

Sayumi — a portable, local-first EPUB reader. Go backend (stdlib `net/http`, per-profile SQLite via CGO-free `modernc.org/sqlite`) with a Svelte 5 + Vite frontend embedded into the binary via `go:embed`. One release = one file.

## Commands

- `make check` — every quality gate (frontend build for the embed, `go mod tidy -diff`, gofumpt/goimports, prettier, `go vet`, golangci-lint, govulncheck, `go test -race`, svelte-check, vitest, both builds). Must be green before any push.
- `make fix` — auto-fix pass: imports, formatting, lint `--fix`, mod tidy. Run before `check` if formatting gates fail.
- Single Go test: `go test ./internal/<pkg> -run TestName` (add `-race` for anything concurrent).
- Frontend (from `frontend/`, bun preferred): `bun run dev` (port 3000, proxies `/api` and `/fonts` to a backend on 8080), `check` (svelte-check), `test` (vitest), `build`, `format` / `format:check`.
- `//go:embed dist` requires `cmd/sayumi/dist` to exist — build the frontend before `go build` on a fresh clone (`make web` or `make check` handles it; `make clean` leaves a `.gitkeep` stub so compiles still succeed).

## Architecture

- `cmd/sayumi/` — main: flags, console UI (`n` toggles LAN, `q` quits), server lifecycle, embedded frontend. Binds 127.0.0.1 unless `-network`.
- `internal/api/` — HTTP handlers with per-profile dependencies, gzip middleware, progress write coalescer, bcrypt auth + login throttle, gofile.io share upload (the app's only outbound request).
- `internal/epub/` — refcounted LRU zip store (`store.go`), OPF/NCX parser, HTML/CSS sanitizer (`sanitize.go`), chapter extraction, full-text search with bounded text caches.
- `internal/storage/` — per-profile SQLite in WAL mode; migrations live in `db.go`.
- `internal/library/`, `internal/fonts/` — folder scanning, covers, bundled/user font serving.
- `frontend/src/` — `routes/` (Library, Read, Login), `lib/` (Svelte 5 `$state` class stores + pure TS modules, all unit-tested), `components/`, `iframe/` (the reader engine; `frame.ts` is bundled as an IIFE via the `virtual:frame-script` Vite plugin and injected into a sandboxed `srcdoc` iframe).
- Router is hash-based (`/#/read/<id>`).

## Invariants — intentional design, do not "fix"

Backend:

- modernc DSN pragmas use `_pragma=key(value)` keys; mattn-style `_journal_mode=` keys are silently ignored (a test pins this). Never propose switching to mattn/cgo — CGO_ENABLED=0 portability is the point.
- `escapeDSNPath` rewrites ONLY ambiguous paths (`?` etc.) so existing libraries keep their historical DSNs byte-for-byte; Windows drive/UNC paths pass through untouched.
- Search offset math relies on `foldRunes` (per-rune `unicode.ToLower`); `strings.ToLower` can expand one rune into two and corrupt offsets.
- `bookReplaceMu` pairs cache snapshots with one on-disk EPUB generation; `OpenIndexed`/`Release` pin a zip reader for a whole search.
- The progress coalescer collapses duplicate positions before WAL writes by design.
- Tests that compare SQLite-reported paths must `filepath.EvalSymlinks(t.TempDir())` first (macOS `/var` → `/private/var`).

Frontend:

- Theme system: tokens `--bg/--fg/--accent` plus `--elevated` (official palette surface) and `--accent-ink` (accent adjusted to ≥4.5:1 for TEXT roles; fills keep raw `--accent`). `light-dark()` is valid for colors only — using it for opacity silently invalidates the declaration. `index.html` paints from the `sayumi:theme-vars` localStorage cache before first paint.
- Reader engine: every parent↔iframe message is `seq`-guarded; `restorePending` gates position reports/boundary/highlights until a chapter's restore runs; vertical writing modes go through axis-aware helpers over `|scrollX|`; paged stride = clientWidth + columnGap; reveal is fonts-gated with fallback timers; boundary chapter-advance has a 400ms cooldown + 650ms post-swap grace.
- Library store: the reading-progress publisher is profile-NAME-bound, not generation-bound (a test pins the hard-refresh ordering requiring this); optimistic mutations roll back per-book onto the current array, never via whole-array snapshots.
- Read.svelte: the `applyTheme` effect gates on `settings.loaded`; window-path Ctrl/Cmd+K belongs to App.svelte (Read handles it only for iframe-forwarded keys); dialogs use `onkeydowncapture` so Escape pre-empts earlier-registered handlers.
- TOC virtualization assumes fixed `ROW_H = 34` matching the CSS `--toc-row-h`.
- Svelte `fly`/`fade` transitions bypass the CSS reduced-motion kill switch — give them 0 duration under reduced motion.

If an invariant seems wrong, make the argument with evidence instead of silently changing it.

## Conventions

- Commit messages are detailed and explain the *why*; match the existing style in `git log`.
- Formatting is enforced: gofumpt + `goimports -local sayumi` for Go, prettier for the frontend. Never hand-format against them.
- Every behavioral fix gets a regression test in the package's existing test style. Pure TS modules (`cfi`, `href`, `progress`, …) are cheap to test — prefer adding there.
- Don't bump dependencies casually; govulncheck gates known vulnerabilities.
- Never weaken `internal/epub/sanitize.go` or the reader iframe sandbox — book content is untrusted input.
- Keep diffs surgical; don't reformat or refactor code unrelated to the task.
