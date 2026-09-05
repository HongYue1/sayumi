# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Paused at Checkpoint 3: storage foundations, profiles, and sessions are verified.**
Wait for the user to say **continue** before starting another review batch.
Do not use sub-agents or delegate reviews.

Next task: review storage book records and BookCache, with their tests and
query-plan/benchmark coverage as needed. Preserve title ordering, path identity,
profile isolation, and immutable shared cache slices. Use `TRACKER.md` for the
remaining file inventory and keep the next batch bounded.

Current verified state:
- CLI/server-entry, selected tooling, and the storage foundation batch are reviewed.
  `TRACKER.md` is the source of truth for completed and remaining file coverage.
- The source tree passes `make check`, including race/shuffle tests across all Go
  packages, formatting, lint, vulnerability scanning, frontend checks, and builds.
  The storage tests also passed three consecutive shuffled race-enabled runs.
- Repeated, alternating A/B benchmarks support reduced allocations in storage
  row scans. Most timings are statistically unchanged; do not turn allocation
  reductions or individual operation results into application-wide speed claims.
- Baseline/candidate benchmark executables and comparison runners are retained in
  ignored `.agents/benchmarks/checkpoint2/` and `.agents/benchmarks/checkpoint3/`.
  Do not overwrite a baseline executable with a newer build or compare mismatched
  benchmark sources. Complete debug request formatting retains its measured
  correctness cost; preserve attribute resolution, escaping, and extra context.
- Verified review changes are committed locally. No push is authorized.
  No dependency, database schema, or frontend source changes were made.
- `.skills/` and local measurement artifacts remain ignored and untracked;
  the focused Go benchmark source is tracked with the code.
- The overall backend review remains incomplete. A passing automated check is not
  a completed file review.

## Resume instructions

1. Open `C:\Users\Administrator\Documents\Projects\GO\sayumi` through Local_MCP.
   Reuse an already-open workspace for this folder when available.
2. Read this file, `TRACKER.md`, `AGENTS.md`, and applicable nested `AGENTS.md` files.
3. Inspect Git status and existing diffs before editing; preserve the user's work.
4. Read relevant Go skills under `.skills/cc-skills-golang/skills/`, particularly
   `golang-code-style`, `golang-testing`, `golang-benchmark`, `golang-performance`,
   `golang-cli`, `golang-safety`, and `golang-security`. Load their relevant
   references before applying a technique. Skills remain local and untracked.
5. Finish the current checkpoint before starting the next package. Continue through
   storage, library, EPUB processing, fonts, and HTTP API using `TRACKER.md` as the
   file inventory. Reassess package order if a concrete dependency requires it.

## Working rules

- Do not use sub-agents or delegate work unless the user explicitly lifts this
  restriction. Perform reviews, edits, and verification directly.
- Commit coherent, verified changes with professional, scoped conventional messages
  when ready. Stage only reviewed files. Do not amend or push without a separate request.
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
