# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 11 is complete and verified: SVG sanitizer depth-boundary contracts.**

Base commit: `1c2044d fix(epub): preserve chapter rendering and cache contracts`.
This handoff accompanies the scoped CP11 commit; use Git history for its hash.
CP6-CP10 remain finished; CP12 has not been started. The initial worktree was clean
with no running jobs. Root/internal and applicable nested instructions, TASK/TRACKER,
relevant Go skills/references, and actual dependency callers were read before edits.

- Reviewed scope: `internal/epub/sanitize.go`, `sanitize_test.go`, and new
  `sanitize_bench_test.go`. `chapter.go` has only the required render-version bump;
  its CP10 review was not repeated. API, parser, store, and frontend reads were
  dependency context, not newly completed file reviews.
- Reproduced unsafe attributes surviving on an SVG leaf at document depth 501
  after HTML-to-SVG and SVG/HTML-integration handoffs. A real parsed chapter
  retained `onload` in returned HTML; direct sanitizer output also retained the
  dangerous href, which the later chapter URL rewrite independently neutralized.
  No browser execution or CSP bypass was demonstrated.
- SVG attribute filtering now runs on entry, before the depth guard, rather than
  after traversal. SVG children are no longer filtered twice; HTML children still
  have their own attributes checked before delegation. The existing 500-depth
  budget and sanitized-leaf-at-501 convention are unchanged.
- Added exact boundary tests at depths 500/501/502 for three ancestry shapes,
  parsed/chapter-pipeline reproduction, benign HTML/SVG/MathML preservation,
  promoted-child ordering, comparison-only URI normalization, and tree-link/
  same-tree idempotence fuzz properties. Existing URL, SMIL, and element policy
  remains unchanged; no speculative sanitizer-policy expansion was made.
- Documented exclusive ownership of the mutable parsed tree and the complementary
  resource-rewrite/sandbox/CSP defenses. ChapterRenderVersion is `2026-09-05-2` to
  invalidate prior rendered responses and HTTP ETags.
- Preserved cache budgets/generation identity, API generation-lock lifetimes,
  shared read-only ZIP indexes and one Release per borrow, drained Store.Close,
  exactly-once ResourceReader.Close, and unknown streaming Size (-1). No new
  dependencies or adjacent package changes. Skills/artifacts remain ignored and
  untracked. All 23 prior protected disk artifacts and the CP10 historical source
  hash matched, as did the CP11 comparison inputs/results.
- `TRACKER.md` was recounted: 147 files, 81 complete and 66 pending. Only the three
  sanitizer files were newly completed. Pause after the scoped local commit.

### CP11 measurement and tradeoffs

Ten alternating paired samples, eight cases at GOMAXPROCS 1 and 4, 300ms per case,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1 for matching executables,
AMD Ryzen 7 5800H. Production remains CGO_ENABLED=0. No benchmark overlapped tests
or builds; no samples or outliers were discarded. All 320 rows have a unique
(pair, variant, case, CPU) key, with ten samples per variant/case/CPU and no missing
values. An independent CSV/pandas audit reproduced all 96 grouped metric medians.
Medians below use benchstat's rounded display; exact raw rows are archived below.

| Case | CPU 1 median before -> after | CPU 4 median before -> after |
| --- | --- | --- |
| Warm chapter response | 87.82 -> 88.60 ns | 88.47 -> 88.63 ns |
| Plain chapter miss | 77.32 -> 78.09 us | 78.73 -> 78.20 us |
| Resource chapter, warm CSS | 735.7 -> 744.5 us | 726.4 -> 732.0 us |
| Resource chapter, cold CSS | 905.0 -> 897.4 us | 926.5 -> 913.3 us |
| Parse + sanitize, plain | 311.7 -> 310.5 us | 312.7 -> 307.2 us |
| Parse + sanitize, SVG | 510.9 -> 469.9 us | 508.1 -> 469.4 us |
| Parse + sanitize, hostile | 477.8 -> 475.5 us | 475.2 -> 478.1 us |
| Parse + sanitize, depth boundary | 2.384 -> 2.399 ms | 2.406 -> 2.407 ms |

- SVG fixture time decreased 8.04% at CPU 1 (p=.001) and 7.61% at CPU 4 (p=.004).
  Removing duplicate SVG checks also reduced allocations from 5599 to 5407
  (-192, -3.43%) and median bytes from 140937 to 136840/136840.5 at CPUs 1/4
  (about 4 KiB, -2.91%; both allocation metrics p<.001).
- No other timing differences were statistically significant at the usual .05
  threshold. This is not proof of equivalence or an application-wide speedup.
  The cutoff fix retains the intended pruning behavior while removing the unsafe
  attributes; its allocation-count/byte medians did not increase in this fixture.
- All non-SVG allocation-count medians were unchanged. Warm chapter hits remain
  0 B/op and 0 allocations. Plain chapter miss at CPU 1 used 33890 -> 33838 B/op
  (-52, -0.15%, p=.003); other non-SVG byte differences were not significant.
  No separate optimization claim is made from that small plain-miss byte change.
- Limits: one host/toolchain and synthetic fixtures. New benchmarks include a
  fresh html.Parse on every timed iteration, not just sanitizer walking or a
  repeatedly sanitized tree. Setup/warmup and output validation are outside timing.
  The depth fixture intentionally produces safer output after the fix. The four
  unchanged CP10 chapter cases include cache invalidation and normal rendering;
  ZIP/OS caches are warm, and these CPU settings are not parallel-render throughput.

### Verification

Focused regressions, full EPUB tests, five shuffled race repetitions at CPUs 1/4,
pure-Go EPUB tests, formatter/import checks, and scoped lint passed. A 30-second,
four-worker fuzz campaign completed 180798 executions without failure, checking
same-tree idempotence and link consistency with a 64 KiB input cap; it is not a
browser round-trip or exhaustive security oracle. Full `make check` passed all
gates: tidy, Go/frontend formatting, vet, lint, vulnerability checks, all-package
shuffled race tests, frontend lint/types/tests, and frontend/pure-Go builds.
The measured candidate source/executable/harness hashes remained unchanged through
that check. All jobs exited before handoff.

```sh
go test -count=1 -shuffle=on -run '^(TestSanitize|TestNormalizeURIForSafetyCheck|FuzzSanitizeStableTree)' -timeout=60s ./internal/epub
go test -count=1 -shuffle=on -timeout=60s ./internal/epub
go test -race -shuffle=on -count=5 -cpu=1,4 -timeout=120s ./internal/epub
CGO_ENABLED=0 go test -count=1 -timeout=60s ./internal/epub
go test -run='^$' -fuzz='^FuzzSanitizeStableTree$' -fuzztime=30s -parallel=4 -timeout=90s ./internal/epub
gofumpt -d internal/epub/sanitize{,_test,_bench_test}.go internal/epub/chapter.go
goimports -d -local sayumi internal/epub/sanitize{,_test,_bench_test}.go internal/epub/chapter.go
golangci-lint run ./internal/epub/... --timeout=5m
make check
git diff --check
```

### Protected CP11 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint11/` (ignored). The true-original was
compiled before production edits, from CP10 production at `1c2044d` plus the new
regressions and harness. Its one-iteration benchmark smoke run passed at CPUs 1/4.
Never overwrite either protected executable or change the frozen harnesses/runner.

- `before.test.exe` SHA256:
  `19d8ff13e80dab69b2f30273dc81fe20573c150fb2eb14c29199fcefa6ad1515`.
- Frozen `internal/epub/sanitize_bench_test.go` SHA256:
  `4f8225676ffa8bcba49b7db222b5442f60bfd38ebba8606e0dd242609cfd84f6`.
- The reused four `BenchmarkChapterProcess` cases retain the unchanged CP10
  `internal/epub/chapter_bench_test.go` hash recorded in the next section.
- Original `internal/epub/sanitize.go` at `1c2044d` SHA256:
  `7a6df6997c46eabf5bdb68b345d363f937c71b185a7946e148071951e658420f`.
- Original `internal/epub/chapter.go` at `1c2044d` SHA256:
  `5fb0e07a2f14872b87c250980c7d87201869e54b889f20464dae55730e65b647`.
- Verified/measured `after.test.exe` SHA256:
  `d2eb1280240be39f307ba2bec1d34ff5a1e2495d99605ece5a9bad14ede70c3b`.
- Frozen `compare_benchmarks.py` SHA256:
  `d9be6d05d5ed5533b5b261d79b71d7bb8735196632cef36f3b2fb27a3db0a0b0`.
- `comparison.log` SHA256:
  `d9be162936bdc496aa1d620d59dd6f92d341c4ff61424f03d4bfe5068a87eed5`.
- `make-check.log` SHA256:
  `6867b6f91d428054d2b0b8f153ab22346c668151c3167b3a4d7db405f9b53d1c`.
- `scoped-checks.log` SHA256:
  `1bff18fdf866f1962f4d8047612a9a2f3c56fd497e372a616f76cffe0cf95c97`.
- Raw `runs/20260905T161101.002165Z/samples.csv` SHA256:
  `b74f18fcb4ab4965bf34011177061cc2bfde2c03ab201aba606e3dc423dd2f1d`.
- `regression-before.log` preserves the original failing cases;
  `regression-after.log` preserves their pass in the compiled candidate.
  `baseline-hashes.txt` and `candidate-hashes.txt` record build/source provenance;
  the source hashes identify the measured trees, not permanently frozen production.
- The run directory also contains all twenty raw executable outputs, complete
  benchstat input/output, and exact medians. Repeat with:
  `python .agents/benchmarks/checkpoint11/compare_benchmarks.py 10`.
  The runner checks both executables and both harnesses, alternates order,
  validates all sixteen rows per run, and invokes benchstat with sample ignored.
  Each rerun uses a new output directory; do not overwrite comparison.log or
  rebuild the protected binaries. Stop tests/builds before measuring.
- Reproduce the original failures (expected exit 1) without rebuilding; substitute
  `after.test.exe` for the verified passing comparison:

```sh
.agents/benchmarks/checkpoint11/before.test.exe \
  -test.run='^TestSanitize(Parsed)?SVGDepthBoundary$' -test.v -test.timeout=60s
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

Pause after the scoped CP11 commit and report. On the next explicit continuation,
select one bounded remaining EPUB batch from `TRACKER.md`: either search.go and
its tests or edit.go and its tests, not both automatically. Confirm the dependency
boundary from actual files and Git state before editing. Do not repeat CP6-CP11
or count dependency reads as completed reviews. Preserve all protected artifacts.
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
