# frontend/AGENTS.md

Solid 2.0 (beta) + TypeScript + Vite, built with **bun**. The build output goes to
`../cmd/sayumi/dist` and is embedded in the Go binary, so a stale build ships stale UI.

`package.json` is the source of truth for versions — don't restate pins in docs.

## Commands

From this directory: `bun install`, `bun run dev`, `bun run check` (tsc --noEmit),
`bun run test` (vitest + happy-dom), `bun run build`, `bun run format`,
`bun run lint` (oxlint, type-aware).

`dev` and `build` invoke vite's JS entry under bun explicitly
(`bun node_modules/vite/bin/vite.js …`): the frame-script plugin needs the `Bun`
global, and vite's node-shebang bin would otherwise spawn real node.

## Conventions

- Signals and memos: `createSignal`, `createMemo`. Effects are compute/apply
  `createEffect(compute, apply)` pairs — only the compute phase tracks. Lifecycle is
  `onSettled` / `onCleanup`. No Svelte files or rune modules remain.
- Components are `.tsx`; stores are plain `.ts` modules built on signals (e.g.
  `lib/library.ts`).
- Plain CSS with custom properties in `app.css`. No framework, no CSS-in-JS. Component
  classes are prefixed (`.bc-`, `.tocp-`, `.rdp-`, …); shared design-system classes
  (`.field`, `.btn`, `.icon-btn`, `.press`, `.kbd`, `.backdrop-dismiss`) stay global.
- Icons come from `lib/icons.ts` glyphs via `lib/Icon.tsx`. No lucide dependency.
- Side panels and other heavy UI split with `clientOnly(loader, { lazy: true })` from
  `@solidjs/web`; there is no `lazy` in Solid 2.0.
- Formatting comes from `.prettierrc.json`; don't hand-format around it.

## Invariants

- **`lib/flairs.ts` mirrors `internal/api/flairs.go`.** Both sides or neither.
- **`lib/themes.ts` mirrors `iframe/frame.css`** — chrome background must equal that
  theme's reader `--bg-primary`.
- **The reader is a separate document.** `src/iframe/` runs inside a `srcdoc` iframe with
  its own CSP; shell CSS and shell state cannot reach it. Communicate by message only.
- **Reduced motion is handled once, globally, in `app.css`** — it zeroes duration _and_
  delay for everything, so per-component blocks are redundant. Imperative motion is the
  exception: programmatic `scrollIntoView` and timer-driven animation must check it
  themselves.
- **A zoom gesture never repaints the reader by itself.** Sandboxed without
  `allow-same-origin`, the iframe has an opaque origin and is composited as its own surface,
  and a pinch rescales that surface without asking it to redraw — the text stays a stretched
  bitmap until paint is dirtied inside the frame. Shell UI stays sharp because the parent's
  own surface re-rasters normally; only the isolated frame is stranded. The parent watches
  `visualViewport` and posts `refresh-raster`; dirty paint with anything but opacity, since a
  transparency group costs every glyph its subpixel antialiasing.
- **Never leave a compositor promotion on at rest.** A layer promoted permanently — by a
  3D transform, `backface-visibility`, or a standing `will-change` — keeps its own raster
  and blur buffer alive while idle. Promote transiently around the animation, then drop it.
- **Library sorting uses the shared `Intl.Collator`** so "Book 2" precedes "Book 10". It is
  deliberately not the server's ASCII `NOCASE` order.
- **Popover menus dismiss via window-level listeners, not a fixed scrim.** Card and
  masthead ancestors establish containing blocks that clip `position: fixed` to their own
  box.
- **Protect the load-time budget** (~99 Lighthouse). Animate on interaction, not on mount.
- **Router hash parsing is defensive.** A malformed percent-encoded book ID in
  `#/read/<id>` is an invalid route and falls back to the library — never a crash
  or a stuck reader.
- **A list styled `list-style: none` keeps `role="list"`**, behind a one-line
  `eslint-disable jsx-a11y/no-redundant-roles -- …` comment (the TocPanel shape):
  Safari/VoiceOver drop the semantics, and a config-level exception would hide
  the next instance.
- **Every `<Icon>` declares exactly one accessibility intent.** Use `label` for a
  meaningful standalone image, `decorative` beside exposed text or outside a control,
  and `labelFromParent` only for an icon-only control that owns a non-empty
  `aria-label` or `aria-labelledby`. The development audit warns when the declared
  parent contract and the rendered accessible-name source disagree.
- **Read `<For>` index accessors inside JSX.** Its row factory is deliberately
  untracked: reading `i()` while constructing the row snapshots the index and emits
  `STRICT_READ_UNTRACKED`; a JSX binding such as `String(i())` owns the reactive read.

## Testing

Vitest with happy-dom, beside the code. `bun run check` must be clean — type and a11y
findings are gate failures, not warnings.
