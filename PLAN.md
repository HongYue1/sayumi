# Sayumi — EPUB Reader: Implementation Plan

A portable, local-first EPUB reader. **Go** backend (single binary), **Svelte 5** frontend
served from the same binary, **browser** as the GUI. Performance and a minimal aesthetic are
first-class goals.

> **Strategy:** the legacy Go backend (`sayumi_LEGACY/server`) is production-grade and is reused
> directly — **copied out** into a fresh project root (with `sayumi_LEGACY/` kept untouched as a
> reference snapshot), then modified in place. Only the **frontend is rewritten** (React/TSX →
> Svelte 5). Sections below mark what is **[ported]**, **[ported + changed]**, or **[new]**.
>
> **Backend quality note:** direct code review confirmed careful Unicode-safe search (1:1 rune
> offset mapping), well-tuned SQLite pragmas, DB-level concurrent-import dedupe (partial unique
> index + `INSERT OR IGNORE`), and robust EPUB2-NCX / EPUB3-nav parsing. Reuse as-is; do not rewrite.

---

## 1. Context & Goals

- **Portable**: one `./Reader` executable plus sibling folders. Copy the folder to another machine
  and it just works. No system install, no external services.
- **Local-first**: runs a small HTTP server bound to `localhost` by default; auto-opens the browser.
- **LAN-capable**: can toggle to bind `0.0.0.0` so other devices on the LAN can connect. Over the
  LAN, each user library is protected by a **per-user PIN** (see Auth).
- **Multi-user**: each user has a fully isolated library tree.
- **Lightweight + fast**: pure-Go deps only (no CGO), embedded UI, custom EPUB renderer instead of a
  heavy client-side library. Cold start in tens of ms, instant chapter navigation.
- **Minimal UI**: clean, distraction-free reading surface; controls hide while reading.

### Locked decisions
| Area | Choice |
|------|--------|
| Auth | **No local encryption**; **per-user PIN** gates each library (LAN protection). Host owns local-machine safety. |
| Profiles | Create / select / **clone** (deep-copy library+settings into a sandbox) / delete. |
| Rendering | Custom renderer: Go serves sanitized chapter HTML/CSS → same-origin iframe + CSS multi-column pagination. |
| Position | **Legacy element-path CFI** (`cfi:1/2/3`) with **percent fallback**. |
| Reading modes | **All three**: scroll, single-page, two-page. |
| Storage | Embedded pure-Go SQLite (`modernc.org/sqlite`), WAL mode. **No FTS5** — legacy on-demand search kept (see §4). |
| Backend reuse | **Copy `sayumi_LEGACY/server` → new root**, modify in place; keep `sayumi_LEGACY/` as reference. |
| Distribution | Single binary, Svelte build embedded via `go:embed`, auto-opens browser. |
| Fonts | Embed **2 defaults**; load more from sibling **`./Fonts/`** folder at runtime. |
| Themes | Full **24-theme** catalog ported from legacy. |
| Library import | **Both**: folder auto-scan (SHA256 dedupe) **and** browser upload. |

---

## 2. On-disk layout (portable)

```
./Reader                       # the single binary (Reader.exe on Windows)
./Fonts/                        # user-added reading fonts (scanned at startup)   [new]
  └─ MinionPro/                #   one subfolder per family; files auto-detected
       ├─ MinionPro-Regular.woff2
       ├─ MinionPro-Bold.woff2
       ├─ MinionPro-Italic.woff2
       └─ family.json          #   OPTIONAL explicit mapping (name/weights/styles)
./Library/
  ├─ profiles.db               # shared: profile list + PINs (hashed)            [ported]
  └─ <profileName>/
       ├─ .sayumi/sayumi.db    # per-user: books, progress, bookmarks, settings, flairs, ignored
       └─ books/
            └─ <file>.epub      # original files, untouched (dropped in or uploaded)
```

- The binary resolves `Library/` and `Fonts/` **relative to its own executable path**
  (`os.Executable()`), not the CWD, so double-clicking works. Overridable via `--library` flag or
  `SAYUMI_LIBRARY` env var (legacy behavior, kept).
- Missing folders are created on first run; DBs are migrated automatically.
- The EPUB file is the source of truth for content; SQLite holds only metadata/state.

---

## 3. Tech stack & key libraries

**Backend (Go 1.26+):** *(legacy go.mod is Go 1.26; keep)*
- `net/http` 1.22+ `ServeMux` (method + wildcard patterns). **No third-party router.**  [ported]
- `archive/zip` + `encoding/xml` + `golang.org/x/net/html` — manual EPUB parse + HTML sanitize/rewrite. [ported]
- `modernc.org/sqlite` — pure-Go SQLite, WAL, FK on, 32MB cache, mmap. [ported]
- `golang.org/x/crypto` — hashing the per-user PIN. [ported + changed: was full bcrypt password]
- `golang.org/x/image` — cover decoding/thumbnailing. [ported]
- stdlib `embed` — bundle the built Svelte app + 2 default fonts into the binary.

**Frontend (Svelte 5):** [rewrite from React]
- Svelte 5 **runes** throughout: `$state`, `$derived`, `$effect`, `$props`, `$bindable`, snippets.
- Vite bundler; tiny hand-rolled hash router (port legacy `lib/router.ts` idea, keep deps minimal).
- No CSS framework — hand-written CSS + CSS custom properties for theming. Minimal aesthetic.
- TypeScript.

**Build/release infra:** [ported — full]
- `Makefile` (frontend via bun, server build with `-s -w`, windows target).
- `check.sh` (goimports, gofumpt, go vet, go mod tidy, golangci-lint).
- GitHub Actions cross-compile **matrix**: linux/macos/windows × amd64/arm64.
- Windows `.exe` icon + metadata via **go-winres** (`winres/`, `.syso`).

---

## 4. Backend architecture

Restructured from `sayumi_LEGACY/server` into the idiomatic Go **`cmd/` + `internal/`** layout
(executable under `cmd/`, private packages under compiler-enforced `internal/`). Logic is
byte-identical to legacy; only file locations and import paths (`sayumi/server/…` →
`sayumi/internal/…`) changed. Done in Phase 0 — builds + vets clean.

```
cmd/sayumi/               # package main: resolve Library/ & Fonts/, open DBs, serve, [N] LAN toggle
  main.go prettylog.go    #   run loop, serverManager, middleware, static/SPA serving, terminal UI
  //go:embed dist         #   embedded frontend build (Svelte output lands here)            [ported]
  rsrc_windows_*.syso     #   Windows resource objects (linked on GOOS=windows)
  winres/                 #   go-winres source (icon.png, winres.json)
internal/
  api/                    # handlers + router
    router.go             #   ServeMux wiring
    middleware/gzip.go    #   gzip compression (smart content-type, no-transform aware)   [ported]
    auth.go               #   PIN login, session create/destroy        [ported + changed]
    profilemgr.go         #   create / clone / delete profiles          [ported]
    context.go middleware.go
    books.go upload.go resources.go chapters.go
    progress.go bookmarks.go search.go setting.go
    fonts.go              #   list + serve embedded & ./Fonts/ families  [new]
  epub/                   # parser.go reader.go chapter.go sanitize.go search.go store.go  [ported]
  library/                # scanner.go (auto-import, SHA256 dedupe) cover.go               [ported]
  storage/                # db.go books.go bookcache.go profiles.go progress.go
                          #   bookmarks.go settings.go (+ flairs.go [new])                 [ported]
  fonts/                  # embed.go (2 default fonts) + runtime ./Fonts/ scanner          [ported + changed]
```

> Build target is `go build ./cmd/sayumi`. The frontend is embedded via `//go:embed dist` next to
> `main.go`; the Svelte/Vite build output will be written to `cmd/sayumi/dist`.

### EPUB parsing (`epub/parser.go`) [ported]
Reads `META-INF/container.xml` → OPF; extracts metadata (title, author, ISBN, publisher,
description, cover), builds **spine** (reading order) and **TOC** from EPUB3 `nav.xhtml` or EPUB2
`toc.ncx`; resolves relative paths; **detects RTL** direction. `spine_json` + `toc_json` cached in DB.

### Chapter pipeline (`epub/chapter.go`, `sanitize.go`, `api/chapters.go`, `resources.go`) [ported]
- **Sanitize**: strip `script/noscript/base`, non-stylesheet `<link>`, iframes/forms/embeds; block
  dangerous URI prefixes (`javascript:`, `data:text/html`, `vbscript:`…); strip `on*` handlers;
  depth limit 500.
- **Resource URLs** rewritten to `/api/books/{id}/resources/{path}?token={fileHash}` — the hash
  token lets the iframe load images/CSS/fonts without a session cookie, while blocking direct access.
- **ETag** per chapter = `fileHash + chapterIdx` → 304 Not Modified.

### In-book search (`epub/search.go`, `api/search.go`) [ported]
Full-text across all chapters; per-chapter plain-text extraction **cached**; per-rune
`unicode.ToLower` to keep 1:1 offset mapping; returns snippet (~80 runes context) + char offset;
**cursor pagination** (base64 JSON), 10–200 results/page.
- **FTS5 considered and rejected.** The linear-scan-with-cache approach has zero index
  maintenance, instant first search, no extra storage, and yields **exact character offsets** the
  highlight/jump UI needs (FTS5 would require extra offset-mapping work). Not worth the indexing
  cost for single-book search.

### Library search & sort (`storage/books.go`, `bookcache.go`) [ported + extend]
In-memory **book cache** sorted by title (ASCII case-fold to match SQLite NOCASE), binary-search
insert. Library list search by title/author over a tiny dataset (no FTS needed). **Add sort
options** in the new UI: title / author / recently added / recently read / progress.

### Storage schema (`storage/db.go`) [ported + add flairs]
SQLite, WAL, FK on. Tables:
- `books` — metadata, `cover_path`, `has_cover`, `spine_json`, `toc_json`, `file_hash`, `direction`.
- `progress` — per user: chapter, percent, **CFI**.
- `bookmarks` — per user: chapter, percent, CFI, **label**, **comment** (≤2000 chars), `created_at`.
- `settings` — per user: all reader settings (see §6).
- `flairs` + `book_flairs` — **[new, server-side]** status tags moved out of localStorage into DB,
  per user, so they sync across browsers.
- `ignored_files` — scanner skip-list.

### Library import [ported — both paths]
- **Auto-scan** (`library/scanner.go`): walk `Library/<user>/books/` on startup/profile-open,
  SHA256-hash each `.epub`, dedupe by content hash, skip dotfiles + `ignored_files`, honor ctx cancel.
- **Upload** (`api/upload.go`): multipart, 100 MB limit, temp file → verify `.epub` → hash → import;
  unique-name on collision.

### Fonts (`fonts/`, `api/fonts.go`) [ported + changed]
- **Embed 2 defaults** in the binary (one serif, e.g. *Literata*; one accessible sans, *Atkinson
  Hyperlegible*) so it always works offline.
- **Scan `./Fonts/`** at startup: one subfolder per family, files auto-detected by name
  (`*Regular*`/`*Bold*`/`*Italic*`, variable fonts), formats `woff2|woff|ttf|otf`. Optional
  `family.json` overrides display name / weight / style mapping.
- Merge embedded + discovered into one list for the settings picker; serve via cached `/api/fonts/...`.
- **Rescan** endpoint to pick up newly dropped folders without restarting.

---

## 5. Auth & profiles (`internal/api/auth.go`, `profilemgr.go`, `internal/storage/profiles.go`)

**As-built (ported, verified by reading the code):**
- `profiles.db` (shared, WAL): table `profiles(name PK, password_hash, created_at)` — currently
  **bcrypt** hashes. Per-profile data lives in `<libraryRoot>/<name>/.sayumi/sayumi.db`.
- **Sessions**: in-memory `sessionStore`, random 32-byte hex token in an HttpOnly `SameSite=Lax`
  cookie (`sayumi_session`); `Secure` only when `r.TLS != nil`; 24 h expiry (30 d remember-me);
  swept every 5 min by `StartBackgroundTasks`. Non-remember = browser session cookie (no Max-Age).
- `authMiddleware` (`applyAuth`): validates session → confirms profile still exists → opens it
  (ref-counted `ProfileDeps`) → injects into request context.
- **Create** — `validateProfileName` (regex: alphanumeric edges, `[A-Za-z0-9 _-]`, 1–32, no
  `/\:*?"<>|` or `..`); `os.Mkdir` (not MkdirAll) so a stray dir yields a conflict + DB rollback.
- **Clone** — `CreateProfileContext` then `ProfileMgr.CloneProfile` (deep-copy via `os.Root`,
  symlink-escape safe); rolls back the row + dir on failure.
- **Delete** — verifies the **current session's** profile credential, drops all its sessions,
  `lockProfiles` (waits for refs→0), deletes row + `os.RemoveAll` dir, clears cookie.
- ⚠️ **Constraint to preserve:** `clone` and `delete` deliberately **bypass `applyAuth`** and do
  inline auth — `applyAuth` holds a profile ref, but `lockProfiles` waits for refs to reach zero,
  so wrapping them would deadlock. Do not "tidy" these onto `applyAuth`.
- Resource routes intentionally skip auth (file-hash token in URL; iframe can't send cookies).

**Target change — PIN, not password (Phase 2):**
- Rename the request field `password` → `pin` in login/create/clone/delete; keep hashing (a hashed
  PIN, not plaintext — bcrypt is fine, or switch the column comment to `pin_hash`). Relax the
  4-char min to a PIN rule; **allow PIN-less (open) profiles** for a fully trusted machine (empty
  PIN → skip the compare). Libraries are **not** encrypted on disk — the PIN only gates UI/LAN
  access; local-disk safety is the host's responsibility.
- Touch points are small and localized: the four handler request structs + the length validators
  in `auth.go`; the column name/comment in `storage/profiles.go`. Session/cookie/lock logic is
  unchanged.

---

## 6. Frontend architecture (Svelte 5) [rewrite]

**Scaffold done** (Phase 1a): `frontend/` standing up with **bun** + **Vite 8** +
**@sveltejs/vite-plugin-svelte 7** + **Svelte 5.56** + **TypeScript 6** + **svelte-check 4**.
- `~` → `src` alias; dev server on `:3000` proxies `/api` → `127.0.0.1:8080`.
- `vite build` outputs to **`cmd/sayumi/dist`** (the Go embed dir). Verified: typecheck + build
  clean, and the Go binary serves the new `index.html` + hashed assets with SPA fallback and a live
  `/api/health` check.
- Legacy is **Solid**, not React. **Components** are a true rewrite, but the framework-agnostic
  TS/CSS (~4,600 lines) is high quality and **mostly ports verbatim** — see the reuse manifest
  below. The legacy Vite `frameScriptPlugin` (compiles `iframe/frame.ts` → minified string for
  injection) gets ported when the reader lands.

### Legacy frontend reuse manifest (reviewed 2026-06-10)
Of the non-Solid code, the reader engine + data layer + themes reuse as-is; only Solid-coupled or
backend-decision-affected modules need edits. **Crucially, `frame.ts` already implements all three
reading modes (scroll / paged / paged-two), CFI anchoring, swipe/wheel/keyboard nav, resize
re-anchor, and in-iframe search highlighting** — the hardest part is done.

| Module | LOC | Action |
|---|---|---|
| `iframe/frame.ts` | 2133 | **Verbatim** — pure-DOM engine; postMessage protocol (in: load/apply-settings/scroll-to/next-page/prev-page/go-to-page/scroll-to-fragment/get-position/highlight-search/clear-highlights/destroy/set-font-faces; out: ready/loaded/load-error/position/page-changed/at-boundary/key/click/link-clicked). |
| `iframe/frame.css` | 643 | **Verbatim** (paired with frame.ts). |
| `iframe/buildFrameHtml.ts` | 37 | **Verbatim** — needs the `frameScriptPlugin` + `?raw` import ported to our vite.config. |
| `api/client.ts` | 578 | **Verbatim** — typed API, retry+backoff, abort, beacon. Add flairs/fonts fns; `password`→`pin` in Phase 2. |
| `lib/themes.ts` | 213 | **Verbatim** (24 themes data). |
| `lib/cfi.ts` | 68 | **Verbatim** (logic intentionally mirrored inside frame.ts — keep in sync via cross-ref comment). |
| `lib/errors.ts` | 10 | **Verbatim.** |
| `lib/router.ts` | 34 | **Edit** — swap Solid `createSignal`/`onCleanup` for a `$state` rune; keep route matching. |
| `lib/flairs.ts` | 99 | **Edit** — keep defs/palette/color helpers; replace 4 `localStorage` fns with `/api/flairs`. |
| `lib/fonts.ts` | 73 | **Edit** — keep `ReaderFont` shape + helpers; populate list from `/api/fonts` at runtime. |
| `lib/readerFontFaces.ts` | 92 | **Edit** — keep `@font-face` builder; drive from `/api/fonts` (2 embedded + `./Fonts/`). |
| `lib/profile.ts` | 14 | **Replace** — becomes `$state` in `session.svelte.ts`. |
| `global.css` | 638 | **Harvest** design tokens / theme application; component rules rewritten with new markup. |

**ChapterFrame.svelte** (new) is the parent side of the protocol: build srcdoc via
`buildFrameHtml`, pump `window.message` (validate origin), send `load`/`apply-settings`/nav
commands, react to `position`/`at-boundary`/`key`/`link-clicked`, send `destroy` on unmount.

```
frontend/src/
  lib/
    api.ts                    # typed fetch wrapper (port api/client.ts)
    router.ts                 # tiny hash router (port lib/router.ts)
    cfi.ts                    # CFI generate/resolve + percent fallback (port lib/cfi.ts)
    themes.ts                 # 24 themes (port lib/themes.ts)
    fonts.ts                  # font list incl. runtime ./Fonts/ (port + extend)
    flairs.ts                 # status-tag helpers (port, but server-backed now)
    state/
      session.svelte.ts       # current profile ($state)
      settings.svelte.ts      # reader settings rune store, debounced sync to /api/settings
      library.svelte.ts       # book list + search/sort query
      toasts.svelte.ts        # toast queue
  iframe/
    buildFrameHtml.ts         # srcdoc + CSP + injected book CSS/fonts (port)
    frame.ts                  # in-iframe pagination/scroll/nav engine (port)
    frame.css                 # in-iframe base styles (port)
  components/
    AuthGate.svelte ProfileMenu.svelte OfflineBanner.svelte Toast.svelte
    library/   BookCard.svelte LibraryView.svelte ThemeDropdown.svelte
    reader/    ReaderView.svelte ChapterFrame.svelte ReaderToolbar.svelte
               BottomBar.svelte SettingsPanel.svelte TocPanel.svelte
               SearchBar.svelte SearchPanel.svelte ChapterLoading.svelte
  routes/  Login.svelte  Library.svelte  Read.svelte
  App.svelte                  # router + theme application
```

State uses **runes**:
```ts
// settings.svelte.ts
export const settings = $state<ReaderSettings>(defaultSettings);
$effect.root(() => {
  $effect(() => { saveSettingsDebounced($state.snapshot(settings)); });
});
```

---

## 7. The renderer & reading modes (core feature) [ported engine]

**Isolation:** each chapter loads into a same-origin `<iframe>` built via `srcdoc` with a
**nonce-based CSP** (`style-src 'unsafe-inline'`, allow img/font, no script/connect/frame). Inlines
`frame.css` + `frame.ts` (IIFE) + dynamically injected book CSS / `@font-face` blocks. [ported]

**Stylesheet layering** (cascade order):
1. **Book's own CSS** — included by default; a toggle (`preserveStyles`) strips it.
2. **Reader base stylesheet** (`frame.css`).
3. **User override layer** — settings-driven CSS variables + rules, winning over book CSS.

### Reading modes [ported scroll + paged; extend to two-page]
1. **Scroll** — continuous vertical; progress = `scrollTop / (contentHeight − viewport)` clamped [0,1].
2. **Single-page (paged)** — CSS **columns**; page count via `ResizeObserver`; `current = floor(scrollLeft / colWidth)`; animated turns (240–420ms by distance); **RTL** via `row-reverse`.
3. **Two-page** — **[ported]** `frame.ts` already implements `paged-two` (two-column spread, fixed safe insets, advance two pages at a time, RTL-aware). Not new work.

**Navigation** [ported]: chapter-boundary detection via `IntersectionObserver` + scroll monitor;
swipe/double-tap/wheel to cross chapters (velocity thresholds: 600ms wheel, 200ms touch).
Mode switch / font change / resize **recompute** geometry and **re-anchor via CFI** (percent fallback).

### Settings (all live-applied) [ported list]
`fontSize` (def 26px), `fontFamily` (def serif), `lineHeight`, `paragraphSpacing`, `textIndent`,
`contentWidth`, `displayMode` (scroll|paged|two-page), `marginTop/Bottom/Side` (def 48),
`preserveStyles` (def true = keep book CSS), `preserveFonts` (def false = allow author fonts),
`justify` (def true), `hyphenation` (def false), `theme` (def `rose-pine`),
`chapterTitleAlign/Size/Spacing` (header size + title↔body gap). Debounced 500ms save via
`requestIdleCallback`.

### Themes [ported — full 24]
8 light + 16 dark (light, sepia, catppuccin-latte, gruvbox-light, ayu-light, rose-pine-dawn,
solarized-light, everforest-light, flexoki-light, dark, rose-pine, nord, dracula, catppuccin,
gruvbox, ayu-dark, solarized-dark, tokyo-night, tokyo-storm, everforest-dark, one-dark, kanagawa,
flexoki-dark). Each = `--bg/--fg/--accent`; applied to app chrome **and** mirrored into the iframe.

### Flairs (status tags) [ported + changed to server-side]
Built-ins: Reading (blue), Finished (green), Dropped (red), Plan-to-Read (purple); plus custom with
auto-cycling 8-color palette. **Now stored in SQLite per user** (was localStorage), so they sync.

---

## 8. UX extras [ported — all four]
- **TOC panel** — hierarchical from nav/NCX, click to jump, highlights current position.
- **Toasts** — transient feedback (saved, copied, search jumps, errors); auto-dismiss.
- **Offline banner** — `online`/`offline` events + `/api/health` poll (~15s); manual retry.
- **Gzip middleware** — smart content-type compression, honors `Accept-Encoding`/`no-transform`,
  handles 204/206/streaming; pooled writers/buffers.

---

## 9. API surface (JSON unless noted)

**Reconciled against the actual `internal/api/router.go` (2026-06-10).** `[built]` = present in the
copied backend; `[planned]` = still to add. Real paths are canonical — we keep the legacy routes
rather than churn working code for cosmetic URL changes. All `/api/books/*` routes (except
`resources`) run behind `applyAuth`; `clone`/`delete` use **inline** auth (see §5 deadlock note).

```
# --- Health & static ---
GET    /api/health                                liveness (offline banner)            [built]
GET    /robots.txt                                disallow /api/                       [built]
/fonts/                                           embedded reading fonts (StripPrefix) [built]

# --- Auth & profiles (cookie session) ---
GET    /api/auth/status                           {authenticated, profile}             [built]
GET    /api/auth/profiles                          list -> [{name}]                    [built]
POST   /api/auth/login                            {name, password, remember} -> cookie [built]*
POST   /api/auth/logout                                                                [built]
POST   /api/auth/create                           {name, password}                     [built]*
POST   /api/auth/clone                            {newName, password} (inline auth)    [built]*
DELETE /api/auth/profile                          {password}; deletes CURRENT profile  [built]*
                                                  (inline auth, no {name} param)

# --- Books (applyAuth) ---
GET    /api/books                                 library list                         [built]
                                                  (?q=&sort=&order= params)            [built]
POST   /api/books/upload                          multipart .epub                      [built]
POST   /api/library/rescan                        re-scan ./Library for new files      [built]
GET    /api/books/{id}                             metadata + spine                    [built]
DELETE /api/books/{id}                             remove (adds to ignored_files)      [built]
GET    /api/books/{id}/toc                          table of contents                  [built]
GET    /api/books/{id}/cover                        cover image                        [built]
GET    /api/books/{id}/chapters/{index}            sanitized chapter XHTML, ETag       [built]
GET    /api/books/{id}/resources/{path...}         in-epub resource, token (no auth)   [built]
OPTIONS/api/books/{id}/resources/{path...}         CORS preflight                      [built]
GET    /api/books/{id}/search    ?q=&cursor=&limit= in-book full-text search           [built]

# --- Progress & bookmarks (applyAuth) ---
GET    /api/books/{id}/progress                   {chapter, percent, cfi}              [built]
PUT    /api/books/{id}/progress                   save                                 [built]
POST   /api/books/{id}/progress/beacon            fire-and-forget save on page turn    [built]
GET    /api/books/{id}/bookmarks                  list                                 [built]
POST   /api/books/{id}/bookmarks                  {chapter, percent, cfi, label, comment} [built]
PATCH  /api/books/{id}/bookmarks/{bid}            edit label/comment                   [built]
DELETE /api/books/{id}/bookmarks/{bid}            remove                               [built]

# --- Settings (applyAuth) ---
GET    /api/settings   PUT /api/settings          reader settings                      [built]

# --- Flairs (server-side; new this project, applyAuth) ---
GET    /api/flairs                                list custom flairs                   [built]
POST   /api/flairs                                create custom (label, color)         [built]
DELETE /api/flairs/{id}                           delete custom (+clear assignments)   [built]
PUT    /api/books/{id}/flair                      assign/clear a book's flair          [built]

# --- Fonts API (runtime ./Fonts/ scan; distinct from the /fonts/ static handler) ---
GET    /api/fonts                                 list ./Fonts/ user families          [built]
POST   /api/fonts/rescan                          re-scan ./Fonts/                     [built]
GET    /fonts/user/{dir}/{file}                    serve a discovered user font file    [built]
GET    /fonts/{file}                               serve an embedded font file          [built]
```

> `*` **PIN migration (Phase 2):** these four routes currently take a **`password`** field verified
> with **bcrypt**. The agreed change renames the field to **`pin`** and relaxes the min-length rule;
> hashing stays (a hashed PIN, not plaintext). Login/create/clone/delete bodies and the
> `validateProfileName`/length validators are the only touch points — see §5.

---

## 10. Performance strategy [ported + plan]
- Single binary, embedded assets; gzip on text responses.
- Pure-Go SQLite (WAL, mmap, 32MB cache); in-memory **book cache** (sorted, binary-search insert).
- **Open-book cache** of `*zip.Reader`; per-chapter plain-text + HTML caches; **immutable resource
  caching** (ETag/`Cache-Control`); chapter `ETag` → 304.
- **Prefetch** next chapter while reading; debounced progress/settings writes (beacon for turns).
- Minimal JS: hand-rolled pagination, tree-shaken Svelte output.

---

## 11. Build, run, distribute
- **Dev**: `vite dev` (proxy `/api` → Go) + `go run ./cmd/sayumi`.
- **Release**: `make` → bun build frontend (→ `cmd/sayumi/dist`) → `go build -ldflags "-s -w" -o Reader ./cmd/sayumi`.
- **Windows**: `go-winres make` (icon + metadata) → `CGO_ENABLED=0` build → `Reader.exe`.
- **CI**: GitHub Actions matrix builds linux/macos/windows × amd64/arm64, attaches release artifacts.
- **Launch**: resolve `Library/` + `Fonts/`, migrate DBs, auto-scan books, start on free `localhost`
  port, open browser. **`[N]`** key toggles localhost ↔ `0.0.0.0` (LAN) at runtime.

---

## 12. Build phases (suggested order)
0. **Copy-out + restructure** ✅ *(done)*: copied `sayumi_LEGACY/{go.mod,go.sum,server}` into the
   project root, then restructured into idiomatic `cmd/sayumi/` + `internal/` (imports
   `sayumi/server/…` → `sayumi/internal/…`). `sayumi_LEGACY/` kept untouched as reference.
   `go build ./...`, `go vet ./...`, `gofmt`, and `go mod tidy` all clean; binary links (~20.5 MB).
1. **Skeleton**: confirm Go server, `ServeMux`, `go:embed` SPA fallback, Library/+Fonts/
   resolution, DB migrations, gzip, health, browser auto-open, `[N]` LAN toggle all run.
   - **1a (done)**: Svelte 5 + Vite frontend scaffolded; builds into `cmd/sayumi/dist`; Go binary
     serves it; `/api/health` wired; `.gitignore` added.
2. **Profiles + PIN** ✅ *(done)*: backend migrated password→PIN (`pin` field; optional, 4–12
   digits when set; PIN-less profiles allowed — empty stored hash skips the check; column
   `password_hash`→`pin_hash`; `hasPin` added to the profiles list). Clone/delete inline-auth
   preserved. Frontend ported `api/client.ts` (pin), `lib/router.svelte.ts` ($state), `lib/themes.ts`
   (verbatim) + `lib/theme.ts` helper + `lib/session.svelte.ts`; built `routes/Login.svelte`
   (profile picker → PIN prompt only when `hasPin` → create form, first-run aware) and gated
   `App.svelte` on the session. Verified end-to-end against the binary: open vs PIN login, wrong/
   missing PIN → 401, validation, open-profile delete, `hasPin`, served bundle + assets.
3. **Import + library** ✅ *(done, minus flairs/theme-picker UI)*: backend upload/scan/parse/covers
   already present and unchanged. Built `lib/library.svelte.ts` (store: books, query, client-side
   search + 5 sort keys, upload/delete), `components/library/BookCard.svelte` (cover w/ fallback,
   progress bar, hover-delete + confirm), `routes/Library.svelte` (search/sort/upload bar, responsive
   grid, empty/loading/error states), and `App.svelte` hash routing (`/` → Library, `/read/:id` →
   reader placeholder). Verified against the binary with a **real 2 MB EPUB**: upload+parse (title/
   author/448 chapters/cover), list, cover (image/jpeg), **content-hash dedupe** (re-upload → 200,
   count stays 1), spine+toc in detail, delete → 204. **Still [planned]:** server-side flairs +
   `ThemeDropdown` UI (deferred to a later pass).
4. **Reader core** ✅ *(done)*: ported `iframe/{frame.ts,frame.css,buildFrameHtml.ts}` +
   `lib/{cfi,readerFontFaces,fonts,errors}.ts` verbatim; ported the `frameScriptPlugin` into
   `vite.config.ts` (**switched `transformWithEsbuild`→`transformWithOxc`** for Vite 8/rolldown).
   Built `ChapterFrame.svelte` (parent postMessage protocol via **`{@attach}`** for the iframe ref +
   `destroy` cleanup, `<svelte:window onmessage>` pump, full inbound validation), `lib/settings.svelte.ts`
   (`$state` store + `$derived` `toIframeSettings`), `lib/href.ts` (resolveHref/findTocLabel),
   `TocPanel.svelte` (recursive snippet), and `Read.svelte` orchestrator (chapter load w/ LRU cache +
   prefetch±1 + abort + pendingNav, throttled/interval/beacon progress, CFI restore, boundary
   chapter-turn, link/TOC nav, keyboard, **scroll mode**). `App` routes `/read/:id` under `{#key}`.
   Verified vs binary + real EPUB: chapter shape (7 keys), ETag→304, TOC hrefs, progress+CFI
   round-trip, resource token route (image/jpeg, cookie-free). **Pagination modes wired but
   eyeball-pending** (frame.ts implements them; see Phase 5).
5. **Pagination**: single-page then **two-page** column engine, resize/re-anchor, page-turn anims, RTL.
   ✅ *Visual-verified in browser (Phase 4 check): scroll + paged-two both render; engine ported, modes
   reachable via SettingsPanel.*
6. **Settings & themes** ✅ *(done)*: built `SettingsPanel.svelte` — reading mode (scroll/single/two),
   24-theme swatch picker, font dropdown + use-book-fonts, font size, line-height/paragraph-spacing/
   indent (with Auto), side+vertical margins, content width, justify, hyphenation, keep-book-CSS toggle.
   Controls call `settings.update({...})` on input (debounced save); `Read.svelte` `$effect` pushes
   `settings.iframe`→`applySettings` live and `applyTheme(settings.theme)` syncs the chrome. Wired into
   `Read` (⚙ button + `s` key, right-side panel). **Browser-verified:** theme (rose-pine→sepia, chrome
   matched), font size 26→34, mode→two-page all applied live with 0 errors; settings **persisted**
   server-side (theme/mode/fontSize survived re-login). (Chapter-title typography controls added later in
   Phase 9b; `./Fonts/` user fonts added in Phase 9.)
7. **In-book search + bookmarks** ✅ *(done)*: `SearchPanel.svelte` (debounced query → `/api/books/{id}/search`,
   grouped-by-chapter `<mark>` snippets, load-more, keyboard nav) + `BookmarksPanel.svelte` (list sorted by
   chapter/percent, inline label/comment edit, navigate, delete). Wired into `Read.svelte` via an `activePanel`
   enum (`none|toc|settings|search|bookmarks`); keys `f`=search, `b`=toggle bookmark, `t`=toc, `s`=settings,
   `Escape` cascades. Search jump uses `pendingHighlight` + `onloaded` settle → `highlightSearch`; bookmark
   toggle does optimistic add/remove with toast + `currentBookmarkId` star state. **Browser-verified** (Chromium):
   search highlight in-iframe, bookmark add/list/edit/navigate/delete all working, 0 console errors / 0 4xx.
8. **Polish & perf** ✅ *(toasts + offline banner + chrome auto-hide done)*: reusable global toast
   system (`lib/toast.svelte.ts` `$state` singleton + `components/Toaster.svelte`, mounted once in
   App.svelte; Read.svelte's local toast migrated onto it). `components/OfflineBanner.svelte` (online/
   offline events + `/api/health` poll every 15s + manual Retry, mounted globally). Reader chrome
   auto-hide: header bar + progress overlay a full-bleed stage (so toggling never resizes the iframe →
   no re-pagination); hides after 4s idle, reveals on parent pointermove or iframe centre-tap; in paged
   mode the iframe's left/right click regions turn pages and centre toggles chrome (`onclickregion`
   wired to ChapterFrame). **Browser-verified** (Chromium): toast show+auto-dismiss, bar idle-hide +
   tap/move reveal, offline banner show + retry-clears, 0 console errors / 0 4xx. **Still [planned]:**
   prefetch/cache tuning beyond the existing ±1 LRU.
9. **User fonts + role mapping** ✅ *(done)*: backend `internal/fonts/scan.go` scans sibling `./Fonts/<Family>/`
   (one folder per family, woff2/woff/ttf/otf auto-detected, optional `family.json` {label,category}, filename
   role heuristic), served via `/fonts/user/{dir}/{file}` (ETag, path-validated against the scan). `GET /api/fonts`
   + `POST /api/fonts/rescan`; scanner wired through `Dependencies`; `./Fonts/` resolved in main.go (`--fonts`
   flag + `SAYUMI_FONTS`, beside the exe by default). Per-profile `font_roles` JSON column (idempotent
   `ADD COLUMN` migration) maps each user family id → {regular,italic,bold} file. Frontend: `lib/fontRegistry.svelte.ts`
   `$state` singleton, `buildAllFontFaces()` emits `@font-face` (regular 400, bold 700, italic) from the role map
   (falls back to detected), pushed to the iframe via new `setFontFaces` frame API before each load/settings apply.
   SettingsPanel groups Built-in vs Your-fonts in the picker and shows per-role file selects + Rescan when a user
   family is active. **Browser-verified**: pick family → map regular/italic/bold → correct faces in iframe +
   `font-family: TestFamily` on body, live remap updates the face, mapping persists across reload; 0 errors/4xx.
9b. **Chapter-title typography UI** ✅ *(done)*: added a "Chapter titles" section to SettingsPanel —
   Alignment (Auto/Left/Center/Right segmented) + Title size (px, Auto) + Title spacing (em, Auto), driving
   the existing `chapterTitleAlign/Size/Spacing` settings (frame.ts already applied them to h1–h3).
   **Browser-verified**: each control live-applies the right `override-css` rule and all three persist across
   reload (server round-trip); 0 errors/4xx.
9c. **Server-side flairs** ✅ *(done)*: moved status tags out of legacy localStorage into the per-profile
   DB. New `flairs` (custom defs) + `book_flairs` (one flair per book, `ON DELETE CASCADE`) tables;
   `storage/flairs.go` + `api/flairs.go` (GET/POST `/api/flairs`, DELETE `/api/flairs/{id}` which also clears
   assignments in a tx, PUT `/api/books/{id}/flair` set/clear); `flairId` merged into the book list response.
   Frontend: `lib/flairs.ts` (4 built-in defaults frontend-static + palette helper, no localStorage),
   library store gains `customFlairs`/`flairFilters`/`allFlairs` + optimistic `setFlair`/`addCustomFlair`/
   `removeCustomFlair` + flair filtering in `visible`; BookCard shows a cover badge + a flair-picker popover;
   Library has a filter-chip bar with inline custom-flair create + delete. **Browser-verified**: assign
   built-in + custom, badge shows, filter narrows the grid, create/delete custom, all persist across reload
   (delete also clears the badge); 0 errors/4xx.
9d. **"Literary press" visual redesign** ✅ *(done)*: committed app chrome to an editorial/literary aesthetic
   (book-rendering iframe + 24 theme colors untouched). Design tokens in app.css: two *already-embedded* UI
   typefaces reused at zero net cost — EB Garamond (display: wordmark, titles, panel headings) + Atkinson
   Hyperlegible (UI body/labels) via @font-face from /fonts/; type/spacing scales; derived `--hairline/--surface/
   --muted` via color-mix so all 24 themes adapt; global `prefers-reduced-motion` collapse. Library: serif
   wordmark masthead + hairline rule, small-caps "YOUR LIBRARY · N" eyebrow, refined controls/flair chips,
   covers-as-heroes with serif titles + uppercase author, staggered compositor-only fade-up (capped). Login:
   centered serif wordmark + "A READING ROOM" tagline. Reader bar: serif book title + small-caps chapter line.
   Panels: serif h2 titles, small-caps section labels. Font preloads added to index.html (cut FOUT/CLS).
   Validated every changed .svelte with `svelte-autofixer` (0 issues); a11y pass on the flair popover
   (focus-into-menu on open, Escape closes + restores focus to trigger, `aria-hidden` glyphs, `role=menu`
   tabindex); motion compositor-only. **Browser-verified** in light (sepia) + dark (rose-pine) themes, keyboard
   a11y, and reduced-motion; 0 console errors / 0 4xx. Skills used: bencium-innovative-ux-designer,
   svelte-code-writer, fixing-accessibility, fixing-motion-performance, web-quality-audit.
9e. **Library polish** ✅ *(done)*: (a) `POST /api/library/rescan` — `Scanner.ScanNow` now returns imported
    IDs; the handler `pd.Books.Add`s each so newly-dropped files appear without restart; a "Rescan" button in
    the masthead + `library.rescan()` with a toast result. (b) `GET /api/books?q=&sort=&order=` server-side
    filter/sort (`filterAndSortBooks`, title/author/added/read/progress, asc/desc) for API completeness — client
    keeps instant in-memory filtering. (c) `ThemeDropdown.svelte` in the library masthead (light/dark swatch
    popover, a11y: menu/focus/Escape/aria) — and the library now loads settings + `applyTheme` on mount so it
    reflects the saved theme (previously it showed the default until a book was opened). (d) Library action
    errors (upload/remove/flair) routed to the global `toast`. Validated with svelte-autofixer (0 issues);
    **browser-verified**: theme switch live-applies + persists across reload (focus/Escape correct), rescan
    picks up a directly-dropped file ("Added 1 book", grid 1→2); 0 errors/4xx.
10. **Release infra** ✅ *(done)*: `build.sh` (local optimized build — frontend then `go build -trimpath
    -ldflags "-s -w -X main.version… "`, CGO off, auto-`GOAMD64=v3` when the host CPU has avx2/bmi2/fma;
    `--run`/`--skip-web`), `check.sh` (gofmt/vet/test/svelte-check/build gates, `--fast`), `release.sh`
    (portable cross-compile matrix linux/darwin/windows × amd64/arm64 → tar.gz/zip + SHA256SUMS; baseline
    GOAMD64 for portability; Windows icon/metadata via the committed `.syso`, regenerated by go-winres when
    present), `Makefile` wrapping them, and `.github/workflows/{ci,release}.yml`. main.go gained
    `version`/`buildDate` ldflags vars + a `--version` flag. Vite marks `/fonts/*` external (runtime-served)
    to silence build notes. Verified: local v3 build (15M static stripped, GOAMD64=v3 in build metadata),
    cross-compiled linux/arm64 (real aarch64) + windows/amd64 (.exe in zip), checksums, and the built binary
    serves app+fonts+health.
11. **Quality & polish batch** ✅ *(done)*: (a) **check.sh** rewritten to be thorough *and* read-only/CI-safe —
    gofmt/gofumpt -l + goimports -l + go vet + golangci-lint (no --fix) + govulncheck + go-mod-tidy verify +
    go test + svelte-check + builds, with graceful skips; **fix.sh** (`make fix`) is the separate mutating
    auto-fix pass. This immediately surfaced + fixed 2 errcheck + 1 staticcheck issue and a **real callable CVE**
    (golang.org/x/net v0.54→v0.56, html.Parse in epub search). (b) **Backend test suite** (was 0 tests):
    filterAndSortBooks, PIN validate/hash/verify, calcProgress, settings validate/normalize, font
    detectRoles + parseUserFontPath, HTML sanitizer (XSS) + ContentTypeByExt — all passing; a test caught
    filterAndSortBooks mutating its input (fixed via slices.Clone). (c) **Drag-and-drop upload** — drop .epub(s)
    onto the library (depth-counter dragging state + blurred dropzone overlay; multi-file `uploadFiles`; file
    input now `multiple`). (d) **Command palette** (Ctrl/Cmd+K) — word-AND fuzzy filter over nav/actions/books/
    themes, arrow+Enter, focus + Escape, full dialog a11y — and a **`?` shortcuts help** overlay. New
    `lib/ui.svelte.ts` shared overlay store. All `.svelte` validated with svelte-autofixer (0 issues); browser-
    verified palette (theme-apply + book quick-open + Escape), shortcuts open/close, DnD upload (grid 1→2);
    0 errors/4xx. `./check.sh` fully green.
12. **Embed-2-fonts split** ✅ *(done — original prompt requirement)*: the binary now embeds only the two
    chrome-required reading fonts (EB Garamond serif + Atkinson Hyperlegible sans); the other 7 (Literata, Lora,
    Crimson Pro, Spectral, Ovo, Bookerly, Bitter) moved to a committed `fonts-bundle/<Family>/` source
    (Regular/Italic.woff2 + family.json) that `release.sh` bundles into each archive's `Fonts/` folder (binary +
    Fonts/ + README, unpacks to one dir). Dead Manrope/Quicksand (referenced nowhere) deleted. `READER_FONTS`
    and `readerFontFaces.ts` trimmed to the two embedded; default reading font `spectral`→`eb-garamond` (FE +
    BE). The 7 reappear in the picker's "Your fonts" group via the existing Phase-9 ./Fonts/ scanner.
    **Result: binary 15M→13M.** Verified: `/api/fonts` lists all 7 (correct role detection from Regular/Italic
    names), embedded EB Garamond serves, removed embedded paths 404, picker shows Built-in(2)+Your-fonts(7),
    selecting Literata applies `font-family: Literata` via `/fonts/user/Literata/`; release archive contains
    `Fonts/` + README; `./check.sh` green.

---

## 13. Verification
- **Backend unit tests**: EPUB parse (EPUB2 + EPUB3: spine, TOC, cover, RTL); sanitizer strips
  scripts/handlers; resource-token rewriting; in-book search offsets; SHA256 dedupe; PIN hash/verify.
- **Manual end-to-end**: build binary; create two profiles (one PIN, one open); confirm isolation
  and PIN gate over LAN; import via drop-in **and** upload (confirm dedupe); search/sort/flairs;
  open a book; switch **all three** reading modes; change every setting (live apply); toggle book
  CSS / author fonts; switch among several of the 24 themes; drop a family into `./Fonts/`, rescan,
  select it; in-book search + jump; create/edit/delete a bookmark with comment; close & reopen →
  progress (CFI), settings, flairs persist.
- **Portability**: move `./Reader` + `./Library/` + `./Fonts/` to a new dir (and a different OS/arch
  via cross-compile) and confirm it runs unchanged.
- **Performance**: cold start, chapter-nav latency, memory on a large EPUB, gzip ratio.
```
