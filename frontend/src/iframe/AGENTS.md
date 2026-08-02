# frontend/src/iframe/AGENTS.md

The reader engine. This code runs **inside** the reader's `srcdoc` iframe, not in the SPA.
Treat the directory as a separate application with a security boundary around it.

## How it gets there

`vite.config.ts`'s `frameScriptPlugin` bundles `frame.ts` with esbuild (IIFE) into
`virtual:frame-script`, and `buildFrameHtml.ts` inlines that string into the iframe's
nonce'd `<script>`. Two consequences:

- Module syntax in `frame.ts` is only safe while that bundler is in place.
- Everything here ends up inlined into a security-sensitive HTML string. Nothing in the
  iframe may be built from unescaped book content.

## Invariants

- **CSP is deliberately permissive for assets and locked for behavior.**
  `img`/`font`/`media` allow `*` plus explicit `data:` and `blob:` (a `*` source does not
  cover those schemes); scripts are nonce-only and `connect-src` is `'none'`. Don't
  "tighten" the asset sources — real EPUBs break. Don't loosen script or connect.
- **Book content is untrusted.** Sanitizing is fail-closed and depth-bounded, remote URLs
  become `about:invalid`, and local ones route through the book's resources endpoint.
- **Chapter CSS arrives flat.** The backend splices in-EPUB `@import` targets into the
  chapter stylesheet (`internal/epub/chapter.go`, `inlineCSSImports`) because this
  code parses CSS through a constructed `CSSStyleSheet`, and `replaceSync` drops
  `@import` outright — an import that reaches the frame is lost from every preserve
  mode and is never color-stripped or font-split. Imports still present here are
  remote or unresolvable, so dropping them is correct.
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
