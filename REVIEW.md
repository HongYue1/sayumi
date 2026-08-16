# REVIEW.md — frontend review tracker

Working tracker for the Solid 2 frontend review on branch `review/frontend-solid`.
Status only, not a changelog. Temporary scaffolding — delete before merging to
main (root AGENTS.md bans standing review artifacts).

Legend: `[ ]` pending · `[~]` in flight · `[x]` done (reviewed, fixes committed) · `[-]` reviewed, clean

## Tier 1 — source files (2 reviewers/file, arbiter confirms)

- [x] b01 lib: router.ts · href.ts · searchText.ts · cfi.ts · keyboard.ts
- [x] b02 lib: library.ts · session.ts · sessionGate.ts · settings.ts · theme.ts
- [x] b03 lib: customThemes.ts · themes.ts · flairs.ts · errors.ts · ui.ts
- [x] b04 lib: toast.ts · progress.ts · reachability.ts · specimen.ts · signOut.ts
- [x] b05 lib: fonts.ts · fontRegistry.ts · readerFontFaces.ts · icons.ts · Icon.tsx
- [x] b06 lib: focusTrap.ts · scrollLock.ts · searchMarks.ts · frameMessages.ts · api/client.ts
- [~] b07 routes: Library.tsx · Login.tsx · Read.tsx · App.tsx · main.tsx
- [ ] b08 components: CommandPalette · OfflineBanner · ShortcutsHelp · Toaster · library/BookCard
- [ ] b09 components/library: EditBookDialog · ProfileDialog · ProfileMenu · ShareDialog · ThemeDropdown
- [ ] b10 components/reader: BookmarksPanel · ChapterFrame · CustomThemeDialog · SearchPanel · SettingsPanel
- [ ] b11 components/reader: TocPanel · frame-types · frameMessageQueue · iframe: reduceMotion · boundary
- [ ] b12 iframe: buildFrameHtml · cssText · frameHtmlTemplate · pagination · searchHighlight
- [ ] b13 iframe: frame.ts · test-setup.ts · test/library-harness.ts · vite-env.d.ts

## Tier 1 — test files (criteria: stale/wrong comments, dead helpers, Svelte leftovers, assertions that don't test what they claim)

- [ ] b14 App.test · main.test · indexHtml.test · api/client.test · lib/ui.test
- [ ] b15 lib: cfi · customThemes · errors · flairs · focusTrap
- [ ] b16 lib: fontRegistry · fonts · frameMessages · href · icons
- [ ] b17 lib: keyboard · library · libraryLifecycle · modalBoundary · profileState
- [ ] b18 lib: progress · reachability · readerFontFaces · router · scrollLock
- [ ] b19 lib: searchMarks · searchText · session.integration · session · sessionGate
- [ ] b20 lib: settings · specimen · theme · themes · toast
- [ ] b21 lib/Icon.test.tsx · components: CommandPalette · OfflineBanner · ShortcutsHelp · library/BookCard
- [ ] b22 components/library: EditBookDialog · ProfileDialog · ProfileMenu · ShareDialog · ThemeDropdown
- [ ] b23 components/reader: BookmarksPanel · ChapterFrame · CustomThemeDialog · SearchPanel · SettingsPanel
- [ ] b24 components/reader: TocPanel · frameMessageQueue · iframe: boundary · cssText · frame
- [ ] b25 iframe: frameGraph · frameHtmlTemplate · pagination · reduceMotion · searchHighlight
- [ ] b26 routes: Library · Login · Read · test/library-harness · iframe/cssText.bench

## Tier 2 — CSS (2 reviewers/file)

- [ ] b27 src/app.css · src/iframe/frame.css · iframe/searchHighlight.bench

## Tier 3 — self-review (no subagents)

- [ ] index.html · vite.config.ts · vitest.config.ts · tsconfig.json · package.json
- [ ] .oxlintrc.json · .prettierrc.json · frontend/AGENTS.md · src/iframe/AGENTS.md

## Closeout

- [ ] Carried: normalize unresolvable artifact citations (docs29/b49/X35/X54/bNN/batch-N refs) as each file's batch lands
- [ ] `bun run check` + `bun run test` green after every fix batch (rolling)
- [ ] root `./check.sh` — all ten gates
- [ ] push branch, final chat report, REVIEW.md dropped before merge
