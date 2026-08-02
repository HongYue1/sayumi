# frontend/src/iframe/AGENTS.md

The reader engine. This code runs **inside** the reader's `srcdoc` iframe, not in the SPA.
Treat the directory as a separate application with a security boundary around it.

## How it gets there

`vite.config.ts`'s `frameScriptPlugin` bundles `frame.ts` with `Bun.build` (IIFE) into
`virtual:frame-script`. `buildFrameHtml.ts` binds that string together with
`frame.css?raw`, and `frameHtmlTemplate.ts` renders the document around them. The split
is deliberate: the virtual module only exists once the plugin has run, and
`vitest.config.ts` keeps the plugin out of the test run, so the template holds every
decision and `buildFrameHtml.ts` holds nothing but the two imports. Two consequences:

- Module syntax in `frame.ts` is only safe while that bundler is in place.
- Everything here ends up inlined into a security-sensitive HTML string. Nothing in the
  iframe may be built from unescaped book content.

## Invariants

- **CSP is deliberately permissive for assets and locked for behavior.**
  `img`/`font`/`media` allow `*` plus explicit `data:` and `blob:` (a `*` source does not
  cover those schemes); scripts are nonce-only and `connect-src` is `'none'`. Don't
  "tighten" the asset sources — real EPUBs break. Don't loosen script or connect.
  `style-src` is `'unsafe-inline'` and nothing more: every sheet in the document is one
  of the `<style>` slots below, and book content carries inline `style=""` attributes
  that need the keyword. It also carried a `*`, which permitted nothing reachable —
  `@import` never survives to the frame (see below) — so the `*` was dropped in batch 10.
  That is a source for _stylesheet URLs_, not an asset source; it is not the loosening
  this bullet warns about.
- **The `<style>` slots are a cascade contract.** In order: `base-css`,
  `initial-theme-css`, `font-face-css`, `book-css`, `override-css`. `frame.ts` writes the
  last three by id and needs `override-css` to stay last. `initial-theme-css` carries a
  custom theme's palette at first paint: custom themes have no static `html.theme-<id>`
  rule in `frame.css`, so without it the document falls through to the bare `html` rule —
  which _is_ the light palette — and a custom dark theme flashes white until the parent's
  first apply-settings arrives. `color-scheme` follows the same split: custom palettes
  carry theirs in that slot (`deriveReaderVars`), and the built-ins declare theirs in
  `frame.css` next to the palette, classified by `ThemeDef.group` so the frame and the
  shell can never disagree. Without it every UA-drawn surface inside the frame — the
  default focus ring, EPUB3 form controls, spellcheck underlines, native selection —
  renders light on a dark theme.
- **Both srcdoc payloads are raw-text sinks.** A literal `</style>` or `</script>` inside
  the CSS or the bundle ends the tag early whatever the CSS or JS context says, so
  `frameHtmlTemplate.ts` escapes the terminator in everything it interpolates. This is
  the one place in the iframe where escaping protects the _shell_ rather than the book.
- **Book content is untrusted.** Sanitizing is fail-closed and depth-bounded, remote URLs
  become `about:invalid`, and local ones route through the book's resources endpoint.
- **The sanitizer is a deny-list, so book markup keeps its own classes and attributes.**
  `sanitizeAttributes` (`internal/epub/sanitize.go`) drops `on*` handlers and dangerous
  URI schemes and preserves everything else. Anything the reader injects into chapter
  content must therefore be identified by a private attribute, never by a class name a
  book could also use: search highlights are `mark[data-search-mark]` in both
  `searchHighlight.ts` and `frame.css`, because a chapter shipping
  `<mark class="search-highlight">` would otherwise paint permanently as the active hit.
  The same reasoning applies to book CSS, which reaches the frame with its declarations
  intact — `frame.css` sets no `scroll-snap-type`, so a book's own `scroll-snap-align`
  cannot arm a snap container against the scrollLeft page turns.
- **Chapter CSS arrives flat.** The backend splices in-EPUB `@import` targets into the
  chapter stylesheet (`internal/epub/chapter.go`, `inlineCSSImports`) because this
  code parses CSS through a constructed `CSSStyleSheet`, and `replaceSync` drops
  `@import` outright — an import that reaches the frame is lost from every preserve
  mode and is never color-stripped or font-split. Imports still present here are
  remote or unresolvable, so dropping them is correct.
- **One reduced-motion probe, in `reduceMotion.ts`.** It had grown four copies with
  three different cache lifetimes. The `MediaQueryList` is live, so caching it only
  saves an allocation — worth keeping because `boundary.show()` reads it on every
  `touchmove` — and the cache is keyed on the `window.matchMedia` reference so a test
  that swaps it is never served a query built by the implementation it replaced. The
  `typeof` guard stays: some DOMs have no `matchMedia` at all.
- **CFI logic lives in `lib/cfi.ts` only.** Inlined copies drifted once and mis-resolved
  malformed segments. An unresolvable CFI falls back to percentage.
- **Every position report carries a CFI or an explicit empty marker.** Omitting it makes
  the parent keep an older anchor that then overrides newer progress.
- **Paged mode is one multicol scroller**, not stacked page layers. Page math stays in
  positive logical reading order; only DOM reads and writes convert RTL to negative
  `scrollLeft`.
- **Never cache `scrollHeight` for boundary checks.** Late-loading media changes it
  without a resize, and crossing a boundary emits no scroll event.
- **`cleanupFrame` must stay exhaustive** — listeners, timers, rAFs, observers, module
  `dispose()`s. This has been verified; don't re-flag it as a leak.
- **Reduced motion must be checked in JS here.** The shell's CSS cannot reach this
  document, and imperative scrolling isn't suppressible by CSS anyway.
