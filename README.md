# Sayumi

A portable, local-first EPUB reader: a single Go binary with an embedded
Svelte 5 frontend that opens in your browser. No accounts, no cloud — your
library and settings live in folders next to the binary.

## Features

- **Profiles** with optional PINs — each gets its own library, progress, and settings
- **Custom renderer** — sandboxed iframe, three reading modes (scroll, single page, two-page)
- **Typography** — font size, line height, spacing, indent, chapter-title controls; 24 themes
- **Reading fonts** — 2 embedded (EB Garamond, Atkinson Hyperlegible) + drop-in `./Fonts/` families with per-role file mapping
- **In-book search**, **bookmarks**, **flairs** (status tags), library search/sort/filter
- **Command palette** (Ctrl/Cmd+K), keyboard shortcuts (`?`), drag-and-drop upload
- Offline banner, toasts, auto-hiding reader chrome

## Quick start

```sh
./build.sh        # build optimized ./sayumi (frontend + binary)
./sayumi          # run; opens your browser
```

Books: drop `.epub` files into the `Library/` folder beside the binary, or
upload them in the app. Extra reading fonts live in `Fonts/`.

## Development

```sh
make build        # local optimized build (auto GOAMD64=v3 when supported)
make check        # all gates: gofmt, vet, golangci-lint, govulncheck, tests, svelte-check
make fix          # auto-fix pass (imports, formatting, lint --fix, mod tidy)
make release      # cross-compiled, portable archives → dist-release/
```

Frontend dev server (proxies the API to a running binary on :8080):

```sh
cd frontend && bun install && bun run dev
```

## Layout

```
cmd/sayumi/        # package main: server, embedded frontend (go:embed dist)
internal/          # api, epub, library, storage, fonts
frontend/          # Svelte 5 + Vite app (builds into cmd/sayumi/dist)
fonts-bundle/      # drop-in reading fonts shipped in releases as ./Fonts/
```

## Requirements

- Go 1.26+
- [bun](https://bun.sh) (or npm) for the frontend
