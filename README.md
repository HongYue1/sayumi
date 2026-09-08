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

Building from source requires the Go version declared in `go.mod`, the exact stable Bun release in `frontend/package.json`'s `packageManager` field, and Bash (Git Bash on Windows). The Make targets invoke Bash explicitly, including when archive extraction does not preserve executable bits; `bash ./check.sh` also works directly. The frontend is Bun-only: npm/Node cannot provide the iframe's `Bun.build` runtime. Vite dev, build and preview all validate the selected Bun revision through the shared provisioning check; they never install tools. Vitest remains Node-hosted.

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

### CI and maintenance

CI runs on pull requests (including forks and Dependabot), pushes to `main`/`master`, merge-queue checks and manual dispatch. Linux runs the full race-enabled gate; Windows/macOS run native CGO-free Go tests. No path filters or draft-PR skips hide required checks. Only a newer run for the same PR cancels earlier work; push, merge-group and manual runs remain independent.

CI tokens are read-only and checkout does not persist credentials. GitHub scopes PR-written caches to the PR merge ref, not the base branch. Go cache keys include `go.sum` and the shared tool pins in `provision.sh`; caches are an optimization, not validation evidence. Do not cache credentials or pass PR caches/artifacts to a privileged job. Actions use full commit SHAs with version comments; verify updates against the upstream tag, not merely a matching SHA-shaped string.

The Go patch workflow runs Mondays at 06:17 UTC or manually **from the default branch**. A read-only, cache-free job validates the frozen event commit with `make check`. A fresh publishing job runs no builds or dependency installers: it reconstructs only a strictly newer patch in the existing Go minor and matches the validated module blob. Dependency, `toolchain`, `go.sum`, or unrelated changes require manual review. If the default branch moves before publication, rerun from its new tip; the bot branch is updated with an explicit force-with-lease, never an unconditional force push.

Keep repository defaults read-only, require review and status checks, and require approval for outside-contributor workflow runs. The Go updater additionally needs **Actions → General → Workflow permissions → Allow GitHub Actions to create and approve pull requests**; it only creates/updates PRs, never approves or merges them. PRs created with `GITHUB_TOKEN` do not trigger ordinary PR CI. The updater's full check is not a replacement for required PR status checks: if those remain pending, a maintainer can dispatch **CI** on `deps/go-toolchain-patch` and review the result, without bypassing branch protection. No local validation command below dispatches a workflow or publishes anything.

For workflow edits, explicitly install the pinned validator, then run:

```sh
bash ./provision.sh workflow-tools # actionlint only; honors GOBIN and GOTOOLCHAIN=local
make workflow-check
GOTOOLCHAIN=local CGO_ENABLED=1 make check
```

`workflow-check` requires actionlint, Git, Bash and the pinned Bun; it never installs tools. It checks CI/maintenance YAML and expression types, then executes offline shell and temporary-Git regressions. Its actionlint invocation deliberately uses built-in checks only (`-shellcheck= -pyflakes=`), rather than silently varying with optional host tools. The same behavioral regressions run in ordinary Go tests (also requiring Git), so local `make check` covers module handoff, missing tools, Git/API failures and denied PR creation. Hosted scheduling, cache access controls and repository settings still need verification in GitHub; offline fixtures do not emulate the service.

### Commands

```sh
make build        # CGO-free binary, fresh frontend, configured Go CPU baseline
make run          # build, then replace the build process with the native binary
make web          # fresh frontend only
make check        # all quality gates: format, vet, lint, vulncheck, tests, tsc
make fix          # refresh frontend, then apply Go/frontend fixes and mod tidy
make fmt          # Go-only imports + formatting; requires both formatters
make release      # cross-compiled, portable archives in dist-release/
```

`make build` and `make run` always rebuild the frontend before Go, even if `cmd/sayumi/dist` already exists. `bash ./build.sh --skip-web` is an explicit escape hatch for backend-only work: it warns about stale UI and requires a nonempty regular `dist/index.html`, but does not require Bun. Build or compiler failures stop the command; `--run` does not launch an old executable and rejects a cross-target before building. Normal explicit cross-builds still use Go's target executable suffix. The build enforces `GOTOOLCHAIN=local`, retains version/date stamping, and never enables cgo for production.

Local CPU tuning follows Go's environment (`GOAMD64=v1` by default), not an incomplete host-feature probe. Set `GOAMD64=v3 make build` only for machines known to meet [all of Go's v3 requirements](https://go.dev/wiki/MinimumRequirements#amd64); AVX2/BMI2/FMA alone are not sufficient. An explicit cross-build is never tuned from the build host. Use `make release` for the distributable archives.

For frontend work, use two terminals after `make deps`:

```sh
# Terminal 1, repository root: Go API and embedded fonts on port 8080.
make run

# Terminal 2, repository root: frontend HMR on http://localhost:3000.
cd frontend && bun run dev
```

Open the Vite URL, not the Go-served production UI. Vite proxies both `/api` and `/fonts` to `127.0.0.1:8080`; rebuild/restart Go for backend changes. Stop each process with Ctrl-C. `cd frontend && bun run preview` previews already-built assets (no HMR or rebuild); keep the Go backend running for API/font requests.

Vite/Rolldown owns the Solid shell and raw iframe CSS. Bun bundles only `frame.ts` and its runtime imports into one self-contained classic-script IIFE; extra chunks/assets or external imports fail the build because the script is inlined into `srcdoc`. The plugin uses [Bun's metafile](https://bun.sh/docs/bundler#metafile) to register every real transitive input with Vite, so dev HMR and production watch rebuilds need no hand-maintained graph list. Shared modules still update their independent shell consumers, while CSS and HTML-wrapper edits follow Vite's normal graph. Frame compilation errors retain their compiler messages instead of a generic aggregate failure. These contracts have executable build/watch/HMR fixtures; they do not replace browser layout/CSP checks.

The quality gates use gofumpt and goimports for formatting, golangci-lint and `go vet` for static analysis, govulncheck for known vulnerabilities, `go test` for the backend, and oxfmt, oxlint, `tsc`, plus vitest for the frontend.

All four Go quality tools must be on `PATH`; a missing tool fails the check rather than skipping a gate. Checks refresh the generated frontend before Go analysis and tests, without modifying source files. `./check.sh --fast` skips only the final Go build, not frontend compilation or quality gates. Race tests run when `go env CGO_ENABLED` is `1`; a failed race run is never retried without `-race`. A cgo-disabled local run explicitly reports the missing race check, while CI requires race support and the production build remains CGO-free.

### Formatting and diagnostics

`make fmt` delegates to `bash ./fix.sh --go-format`: it requires both goimports and gofumpt before changing anything, runs them in that order, and stops if either fails. It needs neither Bun nor a frontend build. `make fix` additionally requires Go, the pinned Bun, and golangci-lint; it checks the complete tool set and Bun revision before the first mutation, refreshes the embedded frontend before Go analysis, then runs lint fixes before final formatting. Neither entry point installs tools, skips missing tools, or substitutes gofmt/npm. Review the changes and run `make check` afterward.

Keep one formatting owner per language: standalone goimports/gofumpt (not golangci-lint's independently bundled formatter versions), and oxfmt with `.oxfmtrc.json` for TSX/CSS and frontend configuration. The Go formatter's import-comment preservation has an executable regression. Oxc updates are checked against the project's existing formatting and lint policy; do not add a second Biome/Prettier pass that can rewrite the first pass's output.

Oxlint runs type-aware rules through `oxlint-tsgolint`; warnings and unused disable directives fail. Keep suppressions narrow and explained, including the Safari/VoiceOver `role="list"` exception for unstyled lists. The existing `prefer-tag-over-role` exception remains intentional: native-tag substitution flags custom listboxes, dialogs and live-status widgets without proving an accessibility defect; a blanket conversion would change their interaction/styling contracts. Do not retain per-site disable directives for globally disabled rules. The separate `tsc` check covers application sources **and** top-level `*.config.ts`, rejects switch fallthrough and unmarked overrides, and emits no JavaScript. Keep the stable compiler rather than substituting oxlint's experimental type-check mode or a native-preview package for this gate.

Before adopting the Solid ESLint plugin, validate its reactivity diagnostics against this application's Solid 2 patterns. The 0.17 candidate flags intentionally class-held signal tuples in `FontRegistry`/`CustomThemes`; accepting the entire preset would require an application audit, not blanket suppressions or a framework rewrite. Its server-function rules do not apply to this Go-backed SPA. The tooling policy tests use disposable projects outside the source tree, retain the real include/alias rules, and run installed tools without symlinks or downloads.

### Tests, fuzzing and benchmarks

Run Vitest through the Bun package scripts, not `bun test`: the runner remains Node-hosted with the Solid browser/development build and isolated happy-dom environments. Vitest 5 requires Node `^22.12.0 || ^24.0.0 || >=26.0.0`. It rejects unawaited async assertions; local and CI runs both reject `.only`. Use filename or `-t` filters instead. Suite fixtures still own mock clearing, so the upgrade does not silently erase setup/beforeAll history. The runner now installs DOM storage correctly without the old donor-window workaround. The executable policy fixtures cover selection, storage, legitimate fetch mocks, restored guards, and failing assertion/rejection/network paths, including suite teardown.

```sh
# From the repository root, after make deps and selecting the local compiler:
export GOTOOLCHAIN=local
CGO_ENABLED=1 make check
CGO_ENABLED=1 go test -race -count=1 -cover ./internal/epub
# Select one fuzz target in one package; normal go test runs its seeds only.
go test -run '^$' -fuzz '^FuzzSanitizeStableTree$' -fuzztime=10s -parallel=2 ./internal/epub
make bench PKG=./internal/epub BENCH=BenchmarkSanitizeParse/Plain COUNT=5 BENCHTIME=200ms

# Focused frontend tests, optional leak diagnostics, and serial benchmarks:
(cd frontend && bun run test src/iframe/frame.test.ts)
(cd frontend && bun run test:diagnose src/test/library-harness.test.ts)
(cd frontend && bun run bench)
```

Keep Go concurrency tests synchronized with channels or `testing/synctest`, not wall-clock sleeps; repeat/shuffle a focused race run when investigating order dependence. Coverage percentages locate unexercised code, not correctness. Keep any useful fuzz failure as a minimized regression; do not discard failures just to get a green run.

`test:diagnose` adds async-resource stack traces and runs files serially. Leak reports are **advisory**, not a gate: even a zero exit can report leaks, including deliberately pending promise fixtures and DOM abort timers. Inspect the stacks and resource ownership; do not disable isolation, ignore unhandled errors, raise timeouts indiscriminately, or add retries to hide failures. Fake-timer fixtures should restore real timers with the existing leak-checking helper.

Benchmarks stay separate from the unit gate. Vitest 5 registers `bench` through each test's context; explicitly await each registration's `.run()` (registration alone only warns and measures nothing). Fixture assertions run outside timed callbacks. CSS rule counts and a genuinely late search hit are checked before measuring. Run comparisons serially with unchanged fixtures, environment and cache state; the corrected late-hit fixture is not comparable to its old first-paragraph workload. Index-building timings also include the module-export getter overhead that Vitest reports; keep that warning visible and do not interpret those timings as production throughput.

Happy-dom tests do not validate browser layout, CSP or sandbox enforcement. There is no real-browser E2E suite or frontend coverage provider configured. Adding either requires dedicated browser scenarios or a pinned runner-matching coverage provider and explicit provisioning, not an on-demand download during checks. Go's existing race/fuzz tools and the current DOM suites remain the baseline; no test-runtime or application-framework replacement is implied.

## Architecture

The backend is plain Go on the standard-library HTTP router, storing data in per-profile SQLite databases through the CGO-free `modernc.org/sqlite` driver — the binary builds and runs without a C toolchain. The frontend is a Solid 2 single-page app built by Vite and embedded with `go:embed`, which is why a release is one file with nothing to install. EPUB files are parsed and sanitized on the server; each chapter renders inside a sandboxed iframe on the client.

```
cmd/sayumi/     package main: HTTP server and the embedded frontend (go:embed dist)
internal/       api, epub parsing, library scanning, storage (SQLite), bundled fonts
frontend/       Solid 2 + Vite app; builds into cmd/sayumi/dist
fonts-bundle/   drop-in reading fonts shipped in releases as ./Fonts/
docs/           screenshots and other documentation assets
```
