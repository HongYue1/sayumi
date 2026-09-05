# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 10 is complete and verified: chapter rendering and derived-cache contracts.**

Base commit: `edd048f fix(epub): enforce ZIP-store resource ownership`.
This handoff accompanies the scoped CP10 commit; use Git history for its hash.
CP6-CP9 remain finished. No subsequent checkpoint has been started.
The worktree was verified clean with no running jobs before CP10. Root/internal
instructions, TASK/TRACKER, the three chapter files, relevant Go skills and
references, and actual dependency callers were read before implementation.

- Bounded scope: `internal/epub/chapter.go`, `chapter_test.go`,
  `chapter_cache_test.go`, and new `chapter_bench_test.go`. Parser, ZIP reader/store,
  sanitizer, and API callers were dependency context only, not completed reviews.
- The true-original executable and eight-case harness were frozen before any
  production edit. Regressions reproduced successful warm-cache returns after
  cancellation, malformed CSS URLs from quoted filenames, unsafe URL bytes, and
  book-supplied query tokens shadowing the trusted resource token.
- ProcessChapter now checks cancellation before its cache lookup, preserving
  chapter-index validation precedence. Resource URLs percent-encode unsafe bytes
  for CSS strings/srcset without decoding or double-escaping valid URI escapes.
  The trusted token precedes the preserved book query to match first-value
  authorization. ChapterRenderVersion is `2026-09-05-1` for response/ETag changes.
- Tests now prove actual sequential stylesheet decompression reuse, full-response
  cache replay without a retained ZIP, version separation, generation replacement
  and book isolation, concurrent cold renders, exactly one Release on success and
  read/render failure, and exact LRU byte-budget survivors. Weak rewrite assertions
  were replaced with exact output expectations.
- Clarified immutable-generation and sanitized-tree preconditions, and the limits
  of CSS layout/import heuristics. This is not a general CSS parser, import-budget,
  or shutdown redesign. No production dependencies or adjacent package files changed.
- Preserved per-generation cache identity/budgets, API generation-lock lifetime,
  shared read-only indexes, drained Store.Close, ResourceReader.Close ownership,
  and unknown streaming Size (-1). `.skills/` and benchmark artifacts remain ignored
  and untracked. All 22 recorded protected inputs/results across CP7-CP10 matched.
- `TRACKER.md` was recounted: 146 files, 78 complete and 68 pending. Only the four
  chapter files were completed in CP10. Pause after the scoped local commit.

### CP10 measurement and tradeoffs

Ten alternating paired samples, eight cases at GOMAXPROCS 1 and 4, 300ms per
case, Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1 for matching benchmark
executables, AMD Ryzen 7 5800H. Production remains CGO_ENABLED=0. No benchmark
run overlapped tests or builds, and no samples/outliers were discarded.
Medians below use benchstat's rounded display; exact raw rows are archived below.

| Case | CPU 1 median before -> after | CPU 4 median before -> after |
| --- | --- | --- |
| Warm chapter response | 87.83 -> 87.88 ns | 87.99 -> 88.36 ns |
| Plain chapter miss | 71.76 -> 72.40 us | 72.39 -> 71.68 us |
| Resource chapter, warm CSS | 695.8 -> 694.2 us | 695.7 -> 704.8 us |
| Resource chapter, cold CSS | 874.5 -> 897.9 us | 879.4 -> 892.5 us |
| Resource URL, safe input | 514.9 -> 553.6 ns | 516.6 -> 548.3 ns |
| Resource URL, quoting | 671.6 -> 811.3 ns | 670.5 -> 795.5 ns |
| Resource URL, escapes/query | 677.6 -> 742.9 ns | 661.8 -> 743.1 ns |
| CSS URL rewrite | 279.6 -> 283.9 us | 272.6 -> 280.5 us |

- No statistically significant chapter-render or CSS-rewrite timing differences
  in these fixtures; this is not proof of equivalence or an application-wide gain.
- URL-helper costs are significant: safe inputs +7.54%/+6.15%, quoted inputs
  +20.81%/+18.64%, and escaped/query inputs +9.64%/+12.29% at CPUs 1/4 respectively
  (all p <= .002). These costs are accepted for correct serialization; no speedup
  is claimed. The mixed-case timing geomean is +5.12%, not application throughput.
- Allocation counts are unchanged at both CPU settings: warm response 0, plain
  miss 359, resource chapter warm/cold CSS 3783/4041, URL safe/quoting/escaped-query
  5/6/6, and CSS rewrite 787. Warm hits remain 0 B/op; URL cases remain 240/384/384 B/op.
- CSS rewrite at CPU 4 grows from a median 138634 to 138945 B/op (+311 bytes,
  +0.22%, p=.009). Other byte differences are not statistically significant.
  The allocation-count result does not imply identical output size or byte cost.
- Limits: synthetic fixtures on one host/toolchain; ZIP readers and OS file caches
  are warm. Chapter misses include the explicit derived-cache deletion plus normal
  decompression/render/publication, not cold filesystem I/O. CPU settings are not
  parallel-render throughput tests. Fixtures and validation are outside timing.

### Verification

Focused regressions, full EPUB tests, five shuffled race repetitions at CPUs
1 and 4, pure-Go EPUB tests, formatter/import checks, and scoped lint all passed.
Full `make check` passed all gates, including tidy, formatting, vet, lint,
vulnerability checks, all-package shuffled race tests, frontend lint/types/tests,
and frontend/production builds. The measured candidate source hashes remained
unchanged through the final check.

```sh
go test -count=1 -shuffle=on -timeout=60s ./internal/epub
go test -race -shuffle=on -count=5 -cpu=1,4 -timeout=120s ./internal/epub
CGO_ENABLED=0 go test -count=1 -timeout=60s ./internal/epub
gofumpt -d internal/epub/chapter{,_test,_cache_test,_bench_test}.go
goimports -d -local sayumi internal/epub/chapter{,_test,_cache_test,_bench_test}.go
golangci-lint run ./internal/epub/... --timeout=5m
make check
git diff --check
```

### Protected CP10 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint10/` (ignored). The true-original
executable was compiled with unchanged CP9 production code and the new harness;
its one-iteration smoke run passed at CPUs 1 and 4. Never overwrite this binary
or change the frozen harness, even if a later comparison is unfavorable.

- `before.test.exe` SHA256:
  `1933c4b4e3eb8a69176ec807dceac9f0e3cc3a691cc6bb5e4a91b238ee7348d8`.
- Frozen `internal/epub/chapter_bench_test.go` SHA256:
  `38abaed738c66cf6fdf262b66b003c5bb71fb37074e240e8279d57b7f5f06728`.
- Original `internal/epub/chapter.go` SHA256:
  `3880e32aafeeca3c481cc4deff76599c54ddb4c0e277ecfdba8faaef477a3de8`.
- `regression-before.log` reproduces warm-cache cancellation, unsafe URL
  serialization, and first-query-token shadowing before any production edit.
- Verified candidate `after.test.exe` SHA256:
  `a4a5e2d09260a283a06463ffe7a1ae8f4dd1c3ceae45a5dc7cc9d4d9ec2a2ee4`.
- Frozen `compare_benchmarks.py` SHA256:
  `addb472762f23f10222b323fb591dbab70f8a882dd0f552ea0a16cb3cb0d9b7a`.
- `comparison.log` SHA256:
  `3db12185709bce70d854b3c75d1d0b54d6b7e1e7be4075af55736e628deaaa51`.
- `make-check.log` SHA256:
  `3c62009f46ba97c5201dc915ae62698fd500924d46d2fbf73eab55f3b95996b1`.
- `scoped-checks.log` and `regression-after.log` preserve successful verification.
- Raw samples and benchstat input/output are in
  `.agents/benchmarks/checkpoint10/runs/20260905T150319.847692Z/`.
  All 320 case/CPU rows were validated (16 rows per executable run, ten pairs).
- Repeat with `python .agents/benchmarks/checkpoint10/compare_benchmarks.py 10`.
  The runner checks both executables and the harness, alternates variant order,
  checks every expected case/CPU row, and invokes benchstat with sample ignored.
  Each rerun creates a fresh raw-output directory; do not overwrite comparison.log
  or rebuild either protected executable.
- Reproduce the original failures (expected exit 1) without rebuilding:

```sh
.agents/benchmarks/checkpoint10/before.test.exe \
  -test.run='^(TestProcessChapterCancellation|TestBuildResourceURLSerialization|TestBuildResourceURLTrustedTokenFirst|TestRewriteCSSURLQuotedFilename)$' \
  -test.v -test.timeout=45s
```

### Next step

Pause after the scoped CP10 commit and report. On the next explicit continuation,
select one bounded remaining EPUB batch from `TRACKER.md`; sanitizer behavior and
its tests are a natural next boundary to confirm. Reinspect actual files and Git
state. Do not repeat CP6-CP10 or count dependency reads as completed reviews.
No delegation, push, amend, or unattended follow-on work.

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
