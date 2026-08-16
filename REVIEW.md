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
- [x] b08 components: CommandPalette · OfflineBanner · ShortcutsHelp · Toaster · library/BookCard — arbiter-only review (subagent API failed repeatedly); no bugs found, comment hygiene only
- [x] b09 components/library: EditBookDialog · ProfileDialog · ProfileMenu · ShareDialog · ThemeDropdown — one reviewer pass + arbiter pass (the second subagent pass failed on the model API); no bugs found, comment hygiene only

## Not run (stopped after b09 per owner instruction)

- b10–b13 components/reader and reader/iframe engine files
- b14–b26 test files (except the additions/fixes made inside reviewed batches)
- b27 Tier-2 CSS (app.css · frame.css · searchHighlight.bench)
- Tier 3 configs + AGENTS files

## Process deviations

- Environment caps subagents at 2 concurrent (not 10): each batch got one
  correctness-first + one idiom-focused reviewer instead of two per file.
- b08 completed by the arbiter alone after four consecutive subagent
  "model request failed" errors. b09's correctness pass failed the same way;
  it completed with one reviewer pass plus the arbiter's own full-file pass.

## Closeout

- [x] `bun run check` + `bun run test` green after every fix batch (final: 833 passed / 1 skipped)
- [x] prettier clean (one format commit)
- [x] root `./check.sh` — all ten gates passed
- [x] push branch, final chat report
- Carried forward for a future review pass: artifact citations may remain in
  the unreviewed files (bNN/XNN/docsNN/batch-N family — e.g. components/
  library+reader dialogs, iframe/, app.css:4566, Read.tsx docs29 cleanups done).

Deleted before merge.
