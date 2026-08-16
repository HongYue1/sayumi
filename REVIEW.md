# REVIEW.md — frontend review tracker

Working tracker for the Solid 2 frontend review on branch `review/frontend-solid`.
Status only, not a changelog. Temporary scaffolding — delete before merging to
main (root AGENTS.md bans standing review artifacts).

Scope note: the owner stopped the review after batch 9. Batches b10–b27
(components/reader files, reader/iframe engine files, test files, Tier-2 CSS,
Tier-3 configs) were NOT reviewed, except where earlier batches legitimately
touched them (SettingsPanel/ThemeDropdown fixes, ui.test.ts and fonts.test.ts
comment fixes, keyboard/searchText/library/settings/Read/client test
additions).

Legend: `[x]` done (reviewed, fixes committed) · `[-]` reviewed, clean

## Tier 1 — source files

- [x] b01 lib: router.ts · href.ts · searchText.ts · cfi.ts · keyboard.ts
- [x] b02 lib: library.ts · session.ts · sessionGate.ts · settings.ts · theme.ts
- [x] b03 lib: customThemes.ts · themes.ts · flairs.ts · errors.ts · ui.ts
- [x] b04 lib: toast.ts · progress.ts · reachability.ts · specimen.ts · signOut.ts
- [x] b05 lib: fonts.ts · fontRegistry.ts · readerFontFaces.ts · icons.ts · Icon.tsx
- [x] b06 lib: focusTrap.ts · scrollLock.ts · searchMarks.ts · frameMessages.ts · api/client.ts
- [x] b07 routes: Library.tsx · Login.tsx · Read.tsx · App.tsx · main.tsx
- [x] b08 components: CommandPalette · OfflineBanner · ShortcutsHelp · Toaster · library/BookCard — no bugs found, comment hygiene only
- [x] b09 components/library: EditBookDialog · ProfileDialog · ProfileMenu · ShareDialog · ThemeDropdown — no bugs found, comment hygiene only
- [x] b10 components/reader: BookmarksPanel · ChapterFrame · CustomThemeDialog · SearchPanel · SettingsPanel — no bugs found; comment hygiene + the <select> conditional-child comment re-anchored beta.29 → rc.0 (re-probed on the installed runtime, still holds)
- [x] b11 components/reader: TocPanel · frame-types.ts · frameMessageQueue.ts + iframe: reduceMotion.ts · boundary.ts — no bugs found; TocPanel comment hygiene only, the other four clean
- [x] b12 iframe: buildFrameHtml.ts · cssText.ts · frameHtmlTemplate.ts · pagination.ts · searchHighlight.ts — no bugs found; header citation/provenance trims only; TEXT_BOUNDARY_TAGS ↔ search.go mirror re-verified exact
- [x] b13 iframe: frame.ts · test-setup.ts · test/library-harness.ts · vite-env.d.ts — no bugs found; frame.ts comment reattachment + citation drop only

- [x] b14 tests: App · main · indexHtml · api/client · lib/ui — assertions all test what they claim; comment hygiene in App.test.ts + ui.test.ts only
- [-] b15 tests lib: cfi · customThemes · errors · flairs · focusTrap — reviewed, clean
- [-] b16 tests lib: fontRegistry · fonts · frameMessages · href · icons — reviewed, clean (fontRegistry's tick() hits are its own setTimeout helper)
- [-] b17 tests lib: keyboard · library · libraryLifecycle · modalBoundary · profileState — reviewed, clean
- [-] b18 tests lib: progress · reachability · readerFontFaces · router · scrollLock — reviewed, clean
- [-] b19 tests lib: searchMarks · searchText · session.integration · session · sessionGate — reviewed, clean
- [-] b20 tests lib: settings · specimen · theme · themes · toast — reviewed, clean
- [x] b21 tests: lib/Icon · CommandPalette · OfflineBanner · ShortcutsHelp · library/BookCard — assertions all test what they claim; comment hygiene in OfflineBanner/ShortcutsHelp/BookCard only
- [x] b22 tests components/library: EditBookDialog · ProfileDialog · ProfileMenu · ShareDialog · ThemeDropdown — no bad assertions found; citation drops only
- [x] b23 tests components/reader: BookmarksPanel · ChapterFrame · CustomThemeDialog · SearchPanel · SettingsPanel — no bad assertions; citation drops + the <select> comment re-anchored to rc.0 (matches the source comment, re-probed)
- [x] b24 tests: TocPanel · frameMessageQueue · iframe boundary · cssText · frame — no bad assertions; TocPanel's b37 citation dropped, the rest clean
- [-] b25 tests iframe: frameGraph · frameHtmlTemplate · pagination · reduceMotion · searchHighlight — reviewed, clean
- [x] b26 tests routes: Library · Login · Read · test/library-harness · iframe/cssText.bench — Library/harness/bench clean; Login's porting stamp + X60 title and Read's line-number citation dropped
- [x] b27 Tier-2 CSS: app.css · frame.css · searchHighlight.bench — no dead rules (every class has a production hit); 11 "Ported out of" stamps trimmed to their prefix constraints; docs29/Svelte/PANEL_MS leftovers and four line-number citations rewritten standalone
- [x] Tier 3: index.html · vite.config.ts · tsconfig.json · package.json · .oxlintrc.json · .prettierrc.json clean; vitest.config.ts's stale esbuild mention dropped; frontend/AGENTS.md's stale "(beta)" stage dropped; src/iframe/AGENTS.md's "batch 10" citation dropped

## Not run (stopped after b09 per owner instruction)

(all assigned batches complete)

## Closeout

- [x] `bun run check` + `bun run test` green after every fix batch (final: 833 passed / 1 skipped)
- [x] prettier clean (one format commit)
- [x] root `./check.sh` — all ten gates passed
- [x] push branch, final chat report
- Carried forward for a future review pass: artifact citations may remain in
  the unreviewed files (bNN/XNN/docsNN/batch-N family — e.g. components/
  library+reader dialogs, iframe/, app.css:4566, Read.tsx docs29 cleanups done).

Deleted before merge.
