# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Paused at Checkpoint 5; awaiting the user's `continue`.**
Do not start another batch until the user resumes the review. Continue directly,
without sub-agents, and stop again after the next verified, committed checkpoint.

Next bounded scope: the remaining storage stores, `internal/storage/customthemes.go`,
`customthemes_test.go`, `flairs.go`, `flairs_test.go`, `presets.go`, and
`presets_test.go`, plus focused tests or benchmarks needed for that batch. Preserve
profile scoping, custom/built-in identifier contracts, and frontend compatibility.

Verified current state:
- CLI/server-entry, selected tooling, storage foundations, book storage/cache,
  progress, bookmarks, and settings are reviewed. `TRACKER.md` remains the source
  of truth for completed files; the overall backend review is incomplete.
- The current source tree passed `make check`: formatting, vet, lint,
  vulnerability scanning, race/shuffle tests across all Go packages, frontend
  checks, and builds. Storage also passed three consecutive shuffled race runs.
- Reading-state scans reuse one lazily allocated destination and return value
  copies. Tests cover user/book scoping, independent results, NULL/empty CFIs,
  missing-book writes, and canceled reads/writes without persisted mutations.
- Settings tests exercise every field through insert, replacement, and clearing,
  plus independent boolean toggles on an existing row. Keep stored NULL/Auto
  choices distinct from fresh-profile defaults; saves replace a full snapshot.
- Ten-sample alternating comparisons support lower allocations in populated
  progress/bookmark scans, with no extra allocation for empty or single-row
  results. Progress-read timing gains were limited to tested 32/1000-row cases;
  bookmark-read and progress-write timing changes were not statistically clear.
  Treat these as operation-specific results, not application-wide speed claims.
- Baseline/candidate executables and comparison runners remain in ignored
  `.agents/benchmarks/checkpoint2/`, `checkpoint3/`, `checkpoint4/`, and
  `checkpoint5/`. Never overwrite a baseline with a newer build or compare
  different benchmark sources. Keep sample identifiers out of benchmark table
  grouping so repeated samples are analyzed together.
- Preserve earlier measured tradeoffs: debug logging correctness has a formatting
  cost, and large-cache ordering gains do not imply every small-cache case is faster.
- Checkpoint commits are local only; no push is authorized. Check Git history for
  the current commit. No dependency, database schema, or frontend source changes
  were made in this batch.
- `.skills/` and measurement artifacts remain ignored and untracked; focused Go
  regression tests and benchmark source are tracked with the implementation.
- The sanitizer's incidental comment cleanup and partial API contract reads do
  not count as full reviews; those files remain pending in `TRACKER.md`.

## Resume instructions

1. Open `C:\Users\Administrator\Documents\Projects\GO\sayumi` through Local_MCP.
   Reuse an already-open workspace for this folder when available.
2. Read this file, `TRACKER.md`, `AGENTS.md`, and applicable nested `AGENTS.md` files.
3. Inspect Git status and existing diffs before editing; preserve the user's work.
4. Read relevant Go skills under `.skills/cc-skills-golang/skills/`, particularly
   `golang-code-style`, `golang-testing`, `golang-benchmark`, `golang-performance`,
   `golang-cli`, `golang-database`, `golang-safety`, and `golang-security`.
   Load their relevant references before applying a technique. Skills remain
   local and untracked.
5. Finish the current checkpoint before starting the next package. Continue through
   storage, library, EPUB processing, fonts, and HTTP API using `TRACKER.md` as the
   file inventory. Reassess package order if a concrete dependency requires it.

## Working rules

- Do not use sub-agents or delegate work unless the user explicitly lifts this
  restriction. Perform reviews, edits, and verification directly.
- Commit coherent, verified changes with professional, scoped conventional messages
  when ready. Stage only reviewed changes. Do not amend or push without a separate request.
- Keep changes small and justified; avoid cosmetic churn or adding dependencies
  merely because a general-purpose skill recommends a library.
- Preserve repository-specific invariants: profile isolation, lock lifetimes,
  immutable shared cache slices, title ordering, bounded EPUB traversal, token
  authorization, and opt-in-only external sharing.
- Add a regression test for each behavior fix. Confirm references and supported
  build configurations before calling anything dead code.
- For performance changes: write a representative benchmark first, measure before
  and after on the same toolchain/machine/settings, report allocations, and compare
  repeated samples with `benchstat`. Run measurements serially without concurrent
  tests/builds. Do not claim gains from noise or a single run.
- Run targeted checks during a batch and `make check` before handing it back.
  Report failures and incomplete work rather than marking them done.
- `TRACKER.md` contains current file status and completion gates only, never a
  changelog, journal, or benchmark report. A file is done only when fully reviewed
  and any required changes are tested and verified.
- Keep reasoning beside the relevant code/test. Do not create aggregated review
  documents or add session history to `AGENTS.md`.
- Update this file in place with the current task and next step at every checkpoint;
  do not append session logs, dates, or historical progress entries.

## Checkpoint protocol

At the end of each coherent batch:
1. Finish or safely stop all background jobs; do not leave work running.
2. Update this file and `TRACKER.md` to reflect only verified current state.
3. Report the reviewed scope, changes, test results, measured performance evidence
   where applicable, and remaining work to the user.
4. Stop and wait for the user to say **continue**. Do not automatically start the
   next batch or leave review work running after reporting a checkpoint.

For a new chat, ask the assistant to open the project and read `TASK.md` and
`TRACKER.md` before continuing; previous chat context is not assumed.
