# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 7 is complete and verified. Pause after its local completion commit.**

Completion commit subject: `fix(library): preserve scan progress and uploaded covers`.
Use Git to resolve the hash of the commit containing this handoff.

- All eight `internal/library/` Go files are fully reviewed and checked in
  `TRACKER.md`: scanner/cover source, existing tests, contract tests, and benchmarks.
- The audited inventory contains 139 files (125 Go files and 14 tooling files):
  61 complete, 78 pending. Completion gates are not counted as reviewed files.
- The CLI, selected tooling, storage, and library reviews are complete. The only
  adjacent API edit is a stale import-method comment in `internal/api/upload.go`;
  it is not a completed API file review. No dependency, schema, or frontend source
  changes were needed.
- No implementation work remains in CP7. Preserve the original benchmark binaries
  and frozen harnesses below. Keep `.skills/` and `.agents/` ignored and untracked.
- No sub-agents, push, or amend were used. Stop all jobs before handing off.
- Next, only after the user says **continue**, choose a bounded EPUB-processing
  batch from `TRACKER.md`; then fonts, HTTP API, and remaining tooling. No next
  package review has started. Do not repeat completed checkpoints or reintroduce
  the rejected storage UPDATE RETURNING experiment.

### Verified CP7 behavior

- Scan cancellation and backfill-query errors retain committed import/path/cover
  results for callers. Overlapping callers share read-only result slices.
- Filesystem cover failures remain retryable; malformed EPUBs, missing declared
  covers, oversized images, and undecodable images are resolved non-results.
- Cover cancellation is checked around slot acquisition and between read, decode,
  resize, encode, and publication stages. Decode slots are released on errors and
  before encoding. The four-slot limit, image limits, JPEG quality 85, slash-form
  stored paths, and existing resize/filter/compositing behavior are unchanged.
- Both cover writers serialize only final target validation/publication. Uploads
  replace regular covers; extraction preserves a cover uploaded during decoding.
  Non-regular targets are rejected, and discarded extraction temps are removed.
  This is in-process coordination, not a guarantee against external writers.
- Hash sizes now count the bytes actually hashed; SHA-256 values and 16-hex IDs
  remain compatible. Fixed digest/hex buffers avoid unnecessary allocations.
- Removed unused `Scanner.ImportFile` and `SaveCoverImage` wrappers; migrated all
  test callers to the live upload and encode/write APIs. Tests no longer mask a
  missing cover with a skip or exercise cancellation with an assertion-free sleep.
- Read-only ZIP cleanup is logged rather than turning a committed import into an
  error that would make the upload handler delete its now-owned EPUB.

### Verification

Passed on the CP7 candidate:
- `gofumpt -d internal/library` and `goimports -d -local sayumi internal/library`.
- `go test -race -shuffle=on -count=5 -timeout=120s ./internal/library`.
- `CGO_ENABLED=0 go test -count=1 -timeout=90s ./internal/library`.
- `golangci-lint run ./internal/library/... --timeout=5m` (zero issues).
- Full `make check`, run separately from benchmarks: module tidy, Go/frontend
  formatting, vet, lint, vulnerability checks, all Go tests with race/shuffle,
  frontend lint/types/tests, frontend build, and pure-Go production build.
- Inventory audit: no missing, extra, duplicate, or nonexistent checklist paths.
  Final staging must remain limited to CP7 code/tests, the API comment, and these
  two handoff/checklist files.

### CP7 performance evidence

Raw paired samples and benchstat: `.agents/benchmarks/checkpoint7/comparison.log`.
Full quality-gate output: `.agents/benchmarks/checkpoint7/make-check-final.log`.

One serial comparison: ten alternating before/after samples, 15 frozen cases,
CPU 1, 300ms/sample, Go 1.27.0 windows/amd64 (GOAMD64=v1, CGO_ENABLED=1),
AMD Ryzen 7 5800H. No tests/builds ran concurrently with measurement. Production
remains CGO_ENABLED=0.

- Content hashing: 648 -> 440 B/op and 7 -> 4 allocs/op for all four sizes.
  Empty/4KiB median time fell 21.43%/15.82%; 1MiB/16MiB timing was not significant.
- ID generation: median time fell 29.10%/22.73%/15.18% for short/library/long paths.
  Allocations fell 4 -> 2 (short), 5 -> 3 (other paths); bytes fell
  224 -> 80, 304 -> 160, and 544 -> 400 B/op respectively.
- Cover encoding showed no significant timing change and unchanged allocation
  counts. Safer cover publication costs 616.5 -> 644.3 us/op (+4.51%), roughly
  528 extra B/op, and 36 -> 46 allocs/op. Retained as an explicit correctness cost.
- No resize optimization was attempted. Do not attribute the isolated RGBA
  benchmark movement to an algorithmic gain; its implementation is unchanged.

### Protected CP7 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint7/` (ignored). Never overwrite the two
original binaries or modify the frozen harnesses to improve a comparison.

- `before.test.exe`: original production, warmed hash harness. SHA256:
  `0df41a49821e05861ae648cb4bd1aa4a913db7648e327cc56cbbb5504a79d41e`.
- `before_unwarmed.test.exe`: diagnostic only, NOT the A/B baseline. SHA256:
  `7d1837c8df31278c058c9221d6a8d0b13e5e74090502e731bced789bd5a5dc93`.
- Frozen `internal/library/scanner_bench_test.go` SHA256:
  `32c43975dd730b574b62ac6f70da29553c2344b06ee93c5ca9fed725c8415205`.
- Frozen `internal/library/cover_bench_test.go` SHA256:
  `d09559039916a98ee2a514b035381ffbefa94d684259bf2fe791d4cb723f05c6`.
- `compare_benchmarks.py` SHA256:
  `e952240e2f8fa049628680cd65f9fa3281e6cbc595df7375db5e1dc77c861316`.
- Measured `after.test.exe` SHA256:
  `b6fa31203ea1914d2b7fef5ce4efdcc99a35605dc11cd7d62d1aefffba82e494`.
- Run `python .agents/benchmarks/checkpoint7/compare_benchmarks.py 10` to repeat
  the same 15-case comparison. It uses
  `benchstat -ignore sample -col 'variant@(before after)' -`.

## Resume instructions

1. Open `C:\Users\Administrator\Documents\Projects\GO\sayumi` through Local_MCP.
   Reuse an already-open workspace for this folder when available.
2. Read this file, `TRACKER.md`, `AGENTS.md`, and applicable nested `AGENTS.md` files.
3. Inspect Git status and existing diffs before editing; preserve the user's work.
4. Read relevant Go skills under `.skills/cc-skills-golang/skills/`, particularly
   code style, testing, benchmarking, performance, concurrency, safety, and security;
   add CLI/database skills when that batch calls for them. Read relevant references
   before applying a technique. Skills remain local and untracked.
5. Finish one bounded checkpoint before starting another package. Use
   `TRACKER.md` as the file inventory; reassess order for concrete dependencies.

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
  tests/builds. Give long comparisons and final checks separate job time budgets.
  Do not claim gains from noise or a single run.
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
