# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 8 is complete and verified. Pause after its local completion commit.**

Completion commit subject: `fix(epub): preserve parser and ZIP reader contracts`.
Use Git to resolve the hash of the commit containing this handoff.

- Fully reviewed: `internal/epub/parser.go`, `parser_test.go`, `reader.go`, and
  `reader_test.go`, plus their contract tests and benchmarks (eight Go files).
- The audited inventory contains 143 files (129 Go and 14 tooling): 69 complete,
  74 pending. Completion gates are not counted as reviewed files.
- Adjacent EPUB store, chapter, and edit code was inspected for dependencies, not
  reviewed as whole files. It remains unchecked. No API, schema, dependency,
  frontend source, or other-package changes were needed in CP8.
- No implementation work remains in CP8. Keep the original benchmark executables
  and frozen harnesses below intact, and keep `.skills/` and `.agents/` ignored.
- No sub-agents, push, or amend were used. Stop all jobs before handing off.
- Next, only after the user says **continue**, choose another bounded EPUB batch;
  the ZIP-store lifecycle/resource-ownership files and their tests are a useful
  next scope. Then finish remaining EPUB, fonts, HTTP API, and tooling files.
  Do not repeat completed checkpoints or reopen the storage UPDATE RETURNING
  experiment. No next batch has started.

### Verified CP8 behavior

- A declared package media type wins over an earlier extension-only rootfile.
  The original extension and arbitrary-rootfile fallbacks remain available.
- Explicit spine LTR/RTL progression wins over legacy package direction.
  Creator file-as precedence and metadata/spine ordering remain unchanged.
- Empty cover references no longer suppress later valid candidates. Legacy
  metadata, cover-image properties, and cover-ID priority are preserved.
- Span-wrapped NAV links retain their hrefs without taking a child-list link as
  the parent link. Logical TOC depth and HTML wrapper-depth limits remain bounded.
- Leading-slash references resolve from the archive root. Relative traversal
  still clamps within the archive; URI escapes and query/fragment handling are
  preserved for chapter/resource/edit callers.
- In-memory ZIP reads keep the fixed 64 MiB ceiling. Read, checksum, size, and
  close failures return no partial data; read/limit failures remain primary over
  a secondary close failure. Invalid private-helper limits reject before reading.
- Removed the test-only mutable ZIP ceiling and duplicated index lookup logic.
  Test ZIP cleanup errors are checked. Unused decoded XML fields were removed
  after tracing parsing and token-based editing consumers.
- Text extraction follows DOM links without a width-sized sibling stack, while
  preserving document order, the supplied subtree boundary, and depth inclusion.

### Verification and performance evidence

All passed on the CP8 candidate:
- The initial 14 failing regression subcases now pass. Original-code failures:
  `.agents/benchmarks/checkpoint8/regression-before.log`.
- Formatting/import checks for all eight files, scoped lint with zero issues,
  repeated shuffled race tests (`-count=5`), and CGO-disabled EPUB tests.
- Two 30-second fuzz campaigns with two workers each: 24,224 node-text oracle
  executions and 614,857 bounded-reader executions; no failures. Subsequent
  changes were test lint cleanup and handoff/checklist edits; production and
  fuzz targets were unchanged.
- Full `make check`: tidy, Go/frontend formatting, vet, lint, vulnerability
  checks, all Go race/shuffle tests, frontend lint/types/tests, frontend build,
  and pure-Go production build. Output: `.agents/benchmarks/checkpoint8/make-check.log`.
- Inventory, scoped diff, ignore, and protected-artifact audits.

Raw paired samples and benchstat: `.agents/benchmarks/checkpoint8/comparison.log`.
Ten alternating before/after samples, nine frozen cases, CPU 1, 300ms/sample,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1, AMD Ryzen 7 5800H.
No tests/builds ran concurrently with measurement. Production remains CGO_ENABLED=0.

- Node text, short inline markup: 270.2 -> 199.7 ns/op (-26.11%).
- Node text, wide markup: 100.89 -> 38.73 us/op (-61.61%);
  146,808 -> 46,584 B/op (-68.27%), 24 -> 16 allocs/op.
- Style/deep text, full EPUB parsing, and ZIP reads had no significant timing
  change. Full parse uses six fewer allocations and approximately 144 fewer
  bytes per operation in all three fixtures. Reader allocations are unchanged.
- Do not turn the mixed benchmark geomean into an application-wide speed claim.

### Protected CP8 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint8/` (ignored). Never overwrite the
original executable or change the frozen harnesses for a favorable comparison.

- `before.test.exe` SHA256:
  `45afb838604e441cfd53109f68f014b42cd4dcf170e5149670b2c8dc8bdd667d`.
- Measured `after.test.exe` SHA256:
  `71dd62ed020c9ebd82cd511af13d6d1b32b15f389a70abc5438663bd5e71c3b7`.
- Frozen `internal/epub/parser_bench_test.go` SHA256:
  `1386a39d93551793ef87ff7e1f18cab622e01a0e5545df8fb9d0eff8d5e17718`.
- Frozen `internal/epub/reader_bench_test.go` SHA256:
  `17db40c013e7b7b14c1af6f5e39f0517f76e9f1134a576c258210b368e2a791d`.
- `compare_benchmarks.py` SHA256:
  `5f9947b6bc506a3e3b84c859ae40e8334c7f30bf004683f17edee6d49801f417`.
- `comparison.log` SHA256:
  `40f36b5e4b3112f89cc152f15a3ce98d117a016307b59ee1d2dd0b18ed61f033`.
- Repeat with `python .agents/benchmarks/checkpoint8/compare_benchmarks.py 10`.
  The runner verifies the original executable and harness hashes, alternates
  variants, and uses `benchstat -ignore sample -col 'variant@(before after)' -`.

### Protected CP7 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint7/` (ignored). These remain untouched;
never overwrite the original binaries or modify their frozen harnesses.

- `before.test.exe`: warmed original SHA256:
  `0df41a49821e05861ae648cb4bd1aa4a913db7648e327cc56cbbb5504a79d41e`.
- `before_unwarmed.test.exe`: diagnostic only, not the A/B baseline. SHA256:
  `7d1837c8df31278c058c9221d6a8d0b13e5e74090502e731bced789bd5a5dc93`.
- Frozen `internal/library/scanner_bench_test.go` SHA256:
  `32c43975dd730b574b62ac6f70da29553c2344b06ee93c5ca9fed725c8415205`.
- Frozen `internal/library/cover_bench_test.go` SHA256:
  `d09559039916a98ee2a514b035381ffbefa94d684259bf2fe791d4cb723f05c6`.
- `compare_benchmarks.py` SHA256:
  `e952240e2f8fa049628680cd65f9fa3281e6cbc595df7375db5e1dc77c861316`.
- Measured `after.test.exe` SHA256:
  `b6fa31203ea1914d2b7fef5ce4efdcc99a35605dc11cd7d62d1aefffba82e494`.
- Existing results and quality-gate output: `comparison.log` and
  `make-check-final.log` in that directory. Reproduce, only if needed, with
  `python .agents/benchmarks/checkpoint7/compare_benchmarks.py 10`.

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
