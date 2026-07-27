# frontend/AGENTS.md

Svelte 5 + TypeScript + Vite, built with **bun**. The build output goes to
`../cmd/sayumi/dist` and is embedded in the Go binary, so a stale build ships stale UI.

`package.json` is the source of truth for versions — don't restate pins in docs.

## Commands

From this directory: `bun install`, `bun run dev`, `bun run check` (svelte-check),
`bun run test` (vitest + happy-dom), `bun run build`, `bun format`.

## Conventions

- Runes only: `$state`, `$derived`, `$effect`. No stores in new code.
- Reactive modules are named `*.svelte.ts` (e.g. `lib/library.svelte.ts`).
- Plain CSS with custom properties in `app.css`. No framework, no CSS-in-JS.
- Formatting comes from `.prettierrc.json`; don't hand-format around it.

## Invariants

- **`lib/flairs.ts` mirrors `internal/api/flairs.go`.** Both sides or neither.
- **`lib/themes.ts` mirrors `iframe/frame.css`** — chrome background must equal that
  theme's reader `--bg-primary`.
- **The reader is a separate document.** `src/iframe/` runs inside a `srcdoc` iframe with
  its own CSP; shell CSS and shell state cannot reach it. Communicate by message only.
- **Reduced motion is handled once, globally, in `app.css`** — it zeroes duration _and_
  delay for everything, so per-component blocks are redundant. JS-driven motion is the
  exception: Svelte `fly`/`fade` and imperative `scrollIntoView` must check it themselves.
- **Library sorting uses the shared `Intl.Collator`** so "Book 2" precedes "Book 10". It is
  deliberately not the server's ASCII `NOCASE` order.
- **Popover menus dismiss via window-level listeners, not a fixed scrim.** Card and
  masthead ancestors establish containing blocks that clip `position: fixed` to their own
  box.
- **Protect the load-time budget** (~99 Lighthouse). Animate on interaction, not on mount.

## Testing

Vitest with happy-dom, beside the code. `bun run check` must be clean — type and a11y
findings are gate failures, not warnings.
