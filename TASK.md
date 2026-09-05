# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 9 is complete and verified: EPUB ZIP-store lifecycle and resource ownership.**

Base commit: `2f326c2 fix(epub): preserve parser and ZIP reader contracts`.
This handoff accompanies the scoped CP9 commit; use Git history for its hash.
CP8 and CP7 are finished. No subsequent checkpoint has been started.

- Bounded scope: `internal/epub/store.go`, `store_test.go`,
  `store_concurrency_test.go`, `store_contract_test.go`, and `store_bench_test.go`.
  Adjacent chapter, search, API replacement/resource, and profile-shutdown callers
  are dependency inspection only, not completed file reviews.
- Root/internal instructions, the three store files, relevant concurrency,
  safety, testing, and benchmark skills, and Git status/diffs were inspected.
  The worktree was clean at the base commit. All existing benchmark protections
  below still apply; `.skills/` and `.agents/` remain ignored.
- The true-original benchmark executable and six-case harness are frozen below.
  Original resource-close tests reproduced four failing subcases and a real data
  race. A mechanical private-opener extraction then allowed controlled tests to
  reproduce live-reader leakage on ErrInsecurePath and a lost CloseBook request
  during loading; shared loading, failed-load retry, and ordinary eviction passed.
- Implemented rejected-reader cleanup before error publication, exactly-once
  resource close/release with retained close errors, and explicit-close intent
  across loading while preserving later reacquisition. Index construction now
  runs outside the global store mutex. No LRU algorithm or shutdown redesign.
- Strengthened existing tests and ownership/retention documentation. Scoped
  tests, race tests with five shuffled repetitions at CPUs 1 and 4, CGO-disabled
  tests, formatting/import checks, and scoped lint (zero issues) all passed.
  Full `make check` also passed: tidy, formatting, vet, lint, vulnerability checks,
  all-package shuffled race tests, frontend checks/tests, and production builds.
  The six-case paired benchmark comparison completed separately from all checks.
- Preserve the ready-channel publication barrier, one Release per successful
  OpenIndexed, pinned-reader survival during eviction, byte-budgeted derived
  caches, keep-true DeleteFunc semantics, unknown streaming Size (-1), and MIME
  behavior. API generation locks and drained Store.Close remain required.
- `TRACKER.md` now lists 145 files: 74 complete and 71 pending. Only the five
  store files were completed in this batch. No API, schema, dependency, frontend,
  shutdown-lifecycle, MIME, or resource-size contract changes were made.

### CP9 measurement and verification

Ten alternating paired samples, six cases at CPUs 1 and 4, 300ms per case,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1 for matching benchmark
executables, AMD Ryzen 7 5800H. Production remains CGO_ENABLED=0. Fixtures and
warmup are outside timing; benchmark runs did not overlap tests or builds.

| Case | CPU 1 median before -> after | CPU 4 median before -> after |
| --- | --- | --- |
| Indexed hit, 128 entries | 63.86 -> 63.99 ns | 64.21 -> 64.23 ns |
| Cold open, 128 entries | 118.2 -> 117.5 us | 119.7 -> 116.9 us |
| Cold open, 2048 entries | 1.283 -> 1.236 ms | 1.228 -> 1.214 ms |
| Parallel indexed hit | 64.05 -> 64.57 ns | 169.3 -> 170.5 ns |
| Resource Store4KiB | 11.79 -> 12.19 us | 11.73 -> 11.84 us |
| Resource Deflate64KiB | 32.36 -> 32.65 us | 32.36 -> 33.24 us |

- No statistically significant timing change except parallel hits at CPU 4:
  +0.74%, p=.030. Retained as a small measured cost of this ownership batch,
  not evidence of an application-wide slowdown or speedup.
- Large cold-open results were especially noisy (26-50% confidence-interval
  widths). Moving index construction outside the mutex reduces lock scope;
  these fixtures do not establish a cross-book latency or throughput gain.
- Warm and parallel indexed hits remain 0 B / 0 allocations. Cold128 remains
  662 allocations; Cold2048 remains about 10,283, without a significant change.
- Resource cases remain eight allocations per operation. Store4KiB increases
  from 336 to 352 B/op at both CPU settings. Deflate64KiB medians increase from
  349 to 361 B/op at CPU 1 and 349 to 365 B/op at CPU 4. These small cleanup-state
  costs are accepted for race-free, exactly-once close semantics.
- Final checks: `go test -count=1 ./internal/epub`;
  `go test -race -shuffle=on -count=5 -cpu=1,4 ./internal/epub`;
  `CGO_ENABLED=0 go test -count=1 ./internal/epub`; scoped formatter/import/lint
  checks; full `make check`; and `git diff --check`. All passed.

### Next step

Pause after the scoped CP9 commit and report. On the next explicit continuation,
select one bounded remaining EPUB batch from `TRACKER.md` (chapter rendering and
its cache tests are a natural next focus). Reinspect actual files and Git state;
do not repeat completed parser/reader/store work or treat dependency reads as
completed reviews. No delegation, push, amend, or unattended follow-on work.

### Protected CP9 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint9/` (ignored). Never overwrite the
true-original executable or change the frozen harness to improve the comparison.

- `before.test.exe` SHA256:
  `4d0917acb19b6f1f4fd8aacf4fe04e81627aa54371b441fac824f9f8eb93c3df`.
- Frozen `internal/epub/store_bench_test.go` SHA256:
  `ba50de2ba213dbef39b332ff077e0da620c27b7d968bd1cb19c18b176b6cbec7`.
- `compare_benchmarks.py` SHA256:
  `6e5c2af5157e6df816d6d688630025682e0be96af36e4c0fb9b781ff975d4b01`.
- Measured `after.test.exe` SHA256:
  `4f51b52b0473415c19adb36a2111107237ae45c29ea8c979a74d79c762281e2b`.
- `comparison.log` SHA256:
  `5e4861ac4a71e89304102c48f06ac6dc48df356dfa72513b4bcffc6fd2c65464`.
- Full-check output is preserved in `make-check.log` in this directory.
- Repeat with `python .agents/benchmarks/checkpoint9/compare_benchmarks.py 10`.
  The runner verifies protected inputs, checks all twelve case/CPU rows in every
  sample, alternates variant order, and invokes benchstat with sample ignored.
- `regression-before.log` and `race-before.log` capture true-original close
  failures. `regression-loading-before.log` captures the additional controlled
  failures with only the private-opener extraction applied, not candidate fixes.
- The blocked-close test was adjusted after reproduction because synctest cannot
  treat sync.Once mutex waits as durably blocked. Its final form checks reference
  retention through cleanup and repeat-call errors; barrier tests cover concurrent
  closes. The original close-guard race remains independently reproduced.

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
