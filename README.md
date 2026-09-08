# Sayumi

[![CI](https://github.com/HongYue1/sayumi/actions/workflows/ci.yml/badge.svg)](https://github.com/HongYue1/sayumi/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/HongYue1/sayumi)](https://github.com/HongYue1/sayumi/releases/latest)

Sayumi is a portable, local-first EPUB reader. It ships as a single Go binary with an embedded Solid 2 web app that opens in your browser. There are no accounts and no required cloud services: your library, reading progress, and settings live in plain folders next to the binary.

## Screenshots

Reading view:

![Sayumi reader](docs/screenshots/Reader.png)

Library view:

![Sayumi library](docs/screenshots/Library.png)

## Features

### Reading

- Custom EPUB renderer; each chapter runs inside a sandboxed iframe, isolated from the app shell.
- Three layout modes: continuous scroll, single page, and two-page spread.
- Table of contents, full-text search with match highlighting, and bookmarks with notes.
- Reading position stored as an EPUB CFI, with a save-on-exit beacon so your place survives closed tabs.
- Right-to-left and vertical writing modes, following each book's own metadata.
- Reader chrome that hides itself while you read and returns on demand.

### Typography and themes

- 29 bundled reading fonts, plus drop-in support for your own families — including variable fonts — from a `Fonts` folder, with per-role file mapping (regular, bold, italic, bold italic).
- Full text controls: font size, line height, paragraph spacing, indent, weight, justification, and hyphenation.
- Independent letter-spacing for body text and headings, and dedicated chapter-title controls (alignment, size, weight, per-heading sizing).
- An optional "use the book's own fonts" mode that preserves the publisher's styling; code blocks always fall back to monospace.
- A built-in type specimen page for tuning settings against realistic sample text.
- 25 light and dark themes drawn from canonical palettes — Solarized, Nord, Dracula, Gruvbox, Catppuccin, Tokyo Night, Rosé Pine, Everforest, Flexoki, Kanagawa, and more — plus custom theme creation.

### Library

- Drag-and-drop import, or drop `.epub` files into the `Library` folder and rescan.
- Cover art extraction, with the option to replace any cover from your own image.
- Editable metadata, library-wide search, sorting, and filtering.
- Flairs: custom status tags you can assign to books.
- One-click download of the original `.epub`, and an optional anonymous share link via gofile.io — the only outbound request the app ever makes, and only when you ask for it.

### Profiles and interface

- Multiple profiles, each with its own library, progress, settings, and theme; optional per-profile PIN; remember-me sessions that survive restarts.
- Command palette (<kbd>Ctrl</kbd>/<kbd>Cmd</kbd> + <kbd>K</kbd>) for fast navigation and actions.
- Keyboard shortcuts throughout — press <kbd>?</kbd> for the overview.
- Offline detection with graceful degradation.

## Getting started

1. Download the executable for your platform from the [latest release](https://github.com/HongYue1/sayumi/releases/latest).
2. Run it. Your browser opens the app at `http://127.0.0.1:8080`.
3. Add books by uploading them in the app, or by dropping `.epub` files into the `Library` folder created next to the binary.

Additional reading fonts go in the `Fonts` folder next to the binary; releases include a `Fonts/README.txt` describing the expected layout.

## Usage

```sh
sayumi [flags]
```

| Flag          | Default     | Description                                           |
| ------------- | ----------- | ----------------------------------------------------- |
| `-port`       | `8080`      | Port to listen on.                                    |
| `-library`    | `./Library` | Path to the library root directory.                   |
| `-fonts`      | `./Fonts`   | Path to the user fonts directory.                     |
| `-network`    | `false`     | Allow LAN access by binding to `0.0.0.0`.             |
| `-debug`      | `false`     | Enable verbose debug logging.                         |
| `-version`    |             | Print version and exit.                               |
| `-pprof`      | `false`     | Expose `net/http/pprof` on localhost for diagnostics. |
| `-pprof-port` | `6060`      | Port for the localhost-only pprof server.             |
| `-cpuprofile` |             | Write a CPU profile to the given file.                |
| `-trace`      |             | Write an execution trace to the given file.           |

The library path can also be set with the `SAYUMI_LIBRARY` environment variable. While the server is running, type `n` to toggle LAN access and `q` to quit.

## Development

Building from source requires the Go version declared in `go.mod`, the exact stable Bun release in `frontend/package.json`'s `packageManager` field, and Bash (Git Bash on Windows). The Make targets wrap the scripts; `./check.sh` can also be run directly. npm alone is not sufficient because the frontend build uses `Bun.build`.

### Toolchain policy

- `go.mod` is the Go version authority: its `go` directive is the language/module minimum and the exact toolchain selected by ordinary CI and releases. The maintenance workflow proposes validated patches within that minor; minor upgrades are deliberate. A duplicate `toolchain` directive or another version manager is unnecessary here.
- `frontend/package.json` is the Bun version authority. Every workflow reads it with `bun-version-file`; do not duplicate the pin or use moving `latest`/`canary` labels or ranges. Stable Bun provides the iframe bundler needed by this project without making release output depend on the day CI runs.
- Install/select those versions explicitly before working. `packageManager` is selection metadata, not a local installer: `make check` rejects a different Bun revision before building, including a canary with the same numeric `--version`. Nothing in the check installs or switches Bun. Use `GOTOOLCHAIN=local` to prevent Go's automatic toolchain downloads and switching.
- Evaluate newer stable releases and prereleases in isolation on a branch, with a concrete capability or correctness reason. Adoption requires the frozen install, full `make check` with race support, and affected production builds; keep unsupported platform validation explicit. A prerelease would also need a retrievable fixed revision and an intentional update to the stable-only gate, never a moving channel label. Roll back an unsuccessful adoption by reverting its scoped commit and explicitly reselecting the previous toolchain.

```sh
# Inspect the pins/selected executables; these commands do not install tools.
export GOTOOLCHAIN=local
go version
bun -p 'require("./frontend/package.json").packageManager'
bun --revision
```

### Dependency and tool setup

After selecting the toolchains above, run these from the repository root:

```sh
make deps          # install the locked frontend build/test dependencies
make tools         # install the four pinned Go quality tools
make release-tools # additionally install go-winres for Windows release resources
```

These are thin wrappers over `bash ./provision.sh frontend`, `quality-tools`, and `release-tools`, also used by every workflow. Go installs use the selected local compiler and honor `GOBIN` (or Go's default bin directory); put that directory on `PATH`. The executable pins live only in `provision.sh`. Version-suffixed installs keep each tool's module graph independent of the application and the other tools, rather than letting a shared `tool` directive graph change their transitive versions.

Frontend setup requires a nonempty committed `frontend/bun.lock`, checks the Bun revision, and uses `--frozen-lockfile --ignore-scripts`. The missing-lock guard is intentional: Bun's frozen flag alone can resolve fresh dependencies when no lockfile exists. `frontend/bunfig.toml` also disables lifecycle hooks for direct installs and deliberate dependency updates; build/test commands still run normally. Native build tools use their platform packages without install hooks. Review any future hook requirement explicitly rather than blanket-trusting packages.

Commit `go.mod`/`go.sum` and `frontend/package.json`/`frontend/bun.lock` together when their dependencies change. Go verifies downloaded modules through its checksum mechanism; `go mod verify` checks the local module cache and `go mod tidy -diff` checks manifest consistency. Keep SQLite and its required libc paired as explained in `go.mod`.

Dependabot checks actions and Go modules weekly. Its Bun lockfile-v2 support is [blocked upstream](https://github.com/dependabot/dependabot-core/issues/16026); keep the configured Bun job, but do not treat a lack of PRs as proof that the frontend is current or downgrade the lockfile to satisfy the bot. Until a bot update validates successfully, review frontend updates manually on a branch with the pinned Bun: update only the chosen package (`bun update <package>` within its declared range, or deliberately edit that range and run `bun install`), inspect the manifest/lock diff, then run `make deps` and the full race-enabled `GOTOOLCHAIN=local CGO_ENABLED=1 make check`. Keep the Solid prereleases coordinated. Go executable pins are not covered by the gomod updater: review their upstream releases, edit `provision.sh` once, reinstall, and run the same checks. Revert an unsuccessful scoped update rather than deleting lockfiles or relaxing gates.

### Commands

```sh
make build        # local optimized build (auto GOAMD64=v3 when supported)
make run          # build, then run
make check        # all quality gates: format, vet, lint, vulncheck, tests, tsc
make fix          # refresh frontend, then apply Go/frontend fixes and mod tidy
make fmt          # Go-only imports + formatting; requires both formatters
make release      # cross-compiled, portable archives in dist-release/
```

For frontend work, run a dev server that proxies the API to a binary listening on port 8080:

```sh
make deps
cd frontend && bun run dev
```

The quality gates use gofumpt and goimports for formatting, golangci-lint and `go vet` for static analysis, govulncheck for known vulnerabilities, `go test` for the backend, and oxfmt, oxlint, `tsc`, plus vitest for the frontend.

All four Go quality tools must be on `PATH`; a missing tool fails the check rather than skipping a gate. Checks refresh the generated frontend before Go analysis and tests, without modifying source files. `./check.sh --fast` skips only the final Go build, not frontend compilation or quality gates. Race tests run when `go env CGO_ENABLED` is `1`; a failed race run is never retried without `-race`. A cgo-disabled local run explicitly reports the missing race check, while CI requires race support and the production build remains CGO-free.

### Formatting and diagnostics

`make fmt` delegates to `bash ./fix.sh --go-format`: it requires both goimports and gofumpt before changing anything, runs them in that order, and stops if either fails. It needs neither Bun nor a frontend build. `make fix` additionally requires Go, the pinned Bun, and golangci-lint; it checks the complete tool set and Bun revision before the first mutation, refreshes the embedded frontend before Go analysis, then runs lint fixes before final formatting. Neither entry point installs tools, skips missing tools, or substitutes gofmt/npm. Review the changes and run `make check` afterward.

Keep one formatting owner per language: standalone goimports/gofumpt (not golangci-lint's independently bundled formatter versions), and oxfmt with `.oxfmtrc.json` for TSX/CSS and frontend configuration. The Go formatter's import-comment preservation has an executable regression. Oxc updates are checked against the project's existing formatting and lint policy; do not add a second Biome/Prettier pass that can rewrite the first pass's output.

Oxlint runs type-aware rules through `oxlint-tsgolint`; warnings and unused disable directives fail. Keep suppressions narrow and explained, including the Safari/VoiceOver `role="list"` exception for unstyled lists. The existing `prefer-tag-over-role` exception remains intentional: native-tag substitution flags custom listboxes, dialogs and live-status widgets without proving an accessibility defect; a blanket conversion would change their interaction/styling contracts. Do not retain per-site disable directives for globally disabled rules. The separate `tsc` check covers application sources **and** top-level `*.config.ts`, rejects switch fallthrough and unmarked overrides, and emits no JavaScript. Keep the stable compiler rather than substituting oxlint's experimental type-check mode or a native-preview package for this gate.

Before adopting the Solid ESLint plugin, validate its reactivity diagnostics against this application's Solid 2 patterns. The 0.17 candidate flags intentionally class-held signal tuples in `FontRegistry`/`CustomThemes`; accepting the entire preset would require an application audit, not blanket suppressions or a framework rewrite. Its server-function rules do not apply to this Go-backed SPA. The tooling policy tests use disposable projects outside the source tree, retain the real include/alias rules, and run installed tools without symlinks or downloads.

## Architecture

The backend is plain Go on the standard-library HTTP router, storing data in per-profile SQLite databases through the CGO-free `modernc.org/sqlite` driver — the binary builds and runs without a C toolchain. The frontend is a Solid 2 single-page app built by Vite and embedded with `go:embed`, which is why a release is one file with nothing to install. EPUB files are parsed and sanitized on the server; each chapter renders inside a sandboxed iframe on the client.

```
cmd/sayumi/     package main: HTTP server and the embedded frontend (go:embed dist)
internal/       api, epub parsing, library scanning, storage (SQLite), bundled fonts
frontend/       Solid 2 + Vite app; builds into cmd/sayumi/dist
fonts-bundle/   drop-in reading fonts shipped in releases as ./Fonts/
docs/           screenshots and other documentation assets
```
