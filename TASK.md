# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Paused after Checkpoint 6: custom themes, flairs, and presets storage.**
Wait for the user to say **continue** before starting the next batch. Continue
working directly without sub-agents.

Next bounded scope: `internal/library/scanner.go`, `scanner_test.go`, `cover.go`,
and `cover_test.go`, plus focused regression tests or benchmarks justified by the
review. Preserve filesystem/DB/cache consistency and replacement lock lifetimes.

Verified current state:
- CLI/server-entry, selected tooling, and every Go file under `internal/storage/`
  are reviewed. `TRACKER.md` remains the source of truth for completed files; the
  overall backend review is incomplete.
- The current source tree passed `make check`: formatting, vet, lint,
  vulnerability scanning, race/shuffle tests across all Go packages, frontend
  checks, and builds. Storage also passed three consecutive shuffled race runs.
- Custom-theme, flair, and preset lists reuse lazy query-local scan destinations
  and return value copies. Book-flair maps share one query-local pair of scan
  targets. Empty-result behavior and profile isolation are preserved.
- Regression coverage protects full-record ordering and ownership, duplicate-ID
  rejection, exact preset JSON, theme timestamps, cancellation without writes,
  and scoped transactional flair cleanup with rollback on injected failure.
  Concurrent ID tests join all workers before reporting errors and verify the
  existing prefixes and lowercase-hex format; production ID generation is unchanged.
- Ten alternating 300ms CPU-1 samples on Go 1.27.0 windows/amd64 support 13–20%
  fewer allocated bytes and 5–33% fewer allocations in tested 1000-row reads.
  The book-flair map case measured 14% less time; list-read and theme-update
  timing changes were not statistically clear. These are operation-specific
  measurements, not application-wide speed claims.
- Theme updates retain their original update/affected-row-check/reload path.
  The RETURNING experiment was rejected: its modest successful-update gain did
  not justify the much slower missing-row path. The reason is beside the code.
- Baselines, candidates, and runners remain in ignored
  `.agents/benchmarks/checkpoint*/` directories. The current comparison is in
  `checkpoint6/`: `before.test.exe` versus `after.test.exe`; `returning.test.exe`
  is the rejected experiment, not the final candidate. Never overwrite a baseline
  or compare different benchmark sources. Exclude sample identifiers from table
  grouping so repeated samples are analyzed together.
- Preserve earlier measured tradeoffs: debug logging correctness has a formatting
  cost, and large-cache ordering gains do not imply every small-cache case is faster.
- Checkpoint commits are local only; no push is authorized. Check Git history for
  the current commit. No dependency, database schema, or frontend source changes
  were made in this batch.
- `.skills/` and measurement artifacts remain ignored and untracked; focused Go
  regression tests and benchmark source are tracked with the implementation.
- The sanitizer's incidental comment cleanup and API contract reads do not count
  as full reviews; those files remain pending in `TRACKER.md`.

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
   library, EPUB processing, fonts, HTTP API, and remaining tooling using
   `TRACKER.md` as the file inventory. Reassess order if a concrete dependency requires it.

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
  tests/builds. Give long benchmark comparisons and final checks separate job time
  budgets. Do not claim gains from noise or a single run.
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
