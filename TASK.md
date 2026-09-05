# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 12 is complete and verified: EPUB search text, cancellation, and pagination.**

Base commit: `857600b fix(epub): sanitize SVG attributes at the depth boundary`.
This handoff accompanies the scoped CP12 commit; use Git history for its hash.
CP6-CP11 remain finished; CP13 has not been started. The initial worktree was clean
with no running jobs. Root/internal and applicable frontend instructions, TASK/TRACKER,
relevant Go skills/references, and actual dependency callers were read before edits.

- Reviewed scope: `internal/epub/search.go`, `search_test.go`, and new
  `search_bench_test.go`. Corrections in `internal/AGENTS.md` and
  `frontend/src/iframe/searchHighlight.ts` are contract comments only; frontend
  runtime, API, store, sanitizer, and chapter reads were dependency context, not
  newly completed file reviews.
- Reproduced incorrect offsets/text for removed SVG content, inert HTML templates,
  foreign-namespace elements, unwrapped deep nodes, and the retained depth leaf.
  Search now sanitizes its private parsed document before indexing, distinguishes
  HTML-only traversal rules, and includes the sanitizer's retained depth-501 leaf.
  The sanitizer's policy and 500-depth budget are unchanged.
- Reproduced canceled searches returning success or a filesystem error instead of
  cancellation. Nonempty searches now check cancellation before archive access,
  after extraction, after scans, and before final success. Empty queries remain
  successful no-ops. Physical archive reads/parsing are still non-interruptible;
  valid extracted text can remain cached even when the request is canceled.
- Pagination stops at lookahead before building a discarded snippet or allocating
  the next chapter's rune slice. Inclusive cursors, non-overlapping matches, exact
  code-point snippet offsets, default limits, and nonnil empty results are preserved.
  The one-code-point lowercase implementation is unchanged; inaccurate comments
  that confused Go's simple mapping with JavaScript's full lowercase were corrected.
- Added exact cold/warm text and response regressions, Unicode/cursor/snippet cases,
  boundary limits, missing chapters/archive errors, borrow-release/spine checks,
  deterministic cancellation during ZIP reads, and bounded rune/cursor fuzzing.
- Preserved API generation-lock lifetimes, shared read-only ZIP readers/indexes and
  one Release per borrow, drained Store.Close, exactly-once ResourceReader.Close,
  unknown streaming Size (-1), and derived-cache budgets/generation identity.
  ChapterRenderVersion remains `2026-09-05-2`. No new dependencies or shutdown redesign.
- `TRACKER.md` inventory: 148 files, 84 complete and 64 pending. Only the three search
  files were newly completed. Pause after the scoped local commit. On continuation,
  reassess the remaining EPUB editing pair before moving to another package.

### CP12 measurement and tradeoffs

Ten alternating paired samples, eight cases at GOMAXPROCS 1 and 4, 300ms per case,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1 for matching executables,
AMD Ryzen 7 5800H. Production remains CGO_ENABLED=0. No measurement overlapped tests
or builds; all 320 rows and outliers were retained. Each (pair, variant, case, CPU)
key is unique, with ten samples per variant/case/CPU and no missing values.
An independent pandas/stdlib audit reproduced all 96 metric medians and 48 deltas;
CSV float parsing used round-trip precision without altering samples.

| Case | CPU 1 median before -> after | CPU 4 median before -> after |
| --- | --- | --- |
| Warm page | 37.08 -> 35.25 us | 36.57 -> 36.84 us |
| Warm Unicode page | 54.44 -> 59.80 us | 60.92 -> 60.28 us |
| Warm cross-chapter lookahead | 389.32 -> 42.85 us | 400.81 -> 44.23 us |
| Warm no match | 991.15 -> 997.45 ns | 983.60 -> 985.15 ns |
| Cold plain text | 428.02 -> 432.79 us | 415.29 -> 422.02 us |
| Cold SVG text | 376.51 -> 392.53 us | 372.78 -> 398.34 us |
| Cold structural text | 352.13 -> 328.30 us | 355.70 -> 328.62 us |
| Cold depth boundary | 2.484 -> 2.457 ms | 2.486 -> 2.472 ms |

- Cross-chapter lookahead time decreased 88.99%/88.97% at CPUs 1/4 (both p<.001).
  Median allocation volume fell from 401939.5/401937 B/op to 192 B/op (-99.95%);
  allocation counts fell from 11 to 8. Before editing, a separate original CPU/heap
  profile identified the discarded whole-chapter rune slice as the allocation
  hotspot. Profile timings are not substituted for the paired benchmark samples.
- Correctness costs: cold SVG at CPU 4 increased 6.86% (p=.029); CPU 1 increased
  4.25% without a significant difference (p=.143). The new sanitization pass is
  retained for correct indexed text, rather than reverting it for a favorable number.
- ColdStructural time decreased 6.77%/7.61% (p=.001/.003), but indexed text changed
  from 4095 to 2431 runes by excluding inert/removed content. ColdDepthBoundary now
  indexes the retained six-rune match instead of nothing; allocations rose 1541 ->
  1546. Neither case is an equivalent-work speedup claim.
- Other timings were not significant at the usual .05 threshold; that is not proof
  of equivalence. Warm page/Unicode lookahead uses one fewer allocation (33 -> 32)
  without a demonstrated timing win. Exact allocation/byte medians are archived.
- Limits: one host/toolchain, synthetic fixtures, some wide timing intervals, and
  ordinary unadjusted benchstat comparisons. Cold cases evict only text-cache keys
  inside timing, with ZIP readers and OS caches warm. Setup, warmup, and response
  validation are outside timing. CPU settings do not measure parallel-search
  throughput; no application-wide improvement or aggregate geomean claim is made.

### Verification

Focused regressions, full EPUB tests, five shuffled race repetitions at CPUs 1/4,
pure-Go EPUB tests, formatter/import checks, and scoped lint passed. A 30-second,
four-worker rune/cursor fuzz campaign completed 228236 executions without failure
with a 64 KiB input cap. Full `make check` passed: tidy, Go/frontend formatting,
vet, lint, vulnerability checks, all-package shuffled race tests, frontend lint/
types/tests, and frontend/pure-Go builds. The initial embed-build preflight reused
existing output; the final build gate performed the frontend and Go builds.
The measured source/executable/harness hashes remained unchanged through that check.
No new actual-browser/cross-language DOM round-trip test was performed; the parsed
sanitizer/body-renderer regressions and existing frontend tests are not that guarantee.
All jobs exited. Skills/artifacts remain ignored/untracked, and the CP7-CP11 protected
artifacts/historical source hashes were rechecked unchanged.

```sh
go test -count=1 -shuffle=on -run '^(TestSearch|TestFoldRunes|TestRuneOffsetToByteIndex|TestPlainTextExtractor|FuzzSearchRuneOffsets)' -timeout=60s ./internal/epub
go test -count=1 -shuffle=on -timeout=60s ./internal/epub
go test -race -shuffle=on -count=5 -cpu=1,4 -timeout=120s ./internal/epub
CGO_ENABLED=0 go test -count=1 -timeout=60s ./internal/epub
go test -run='^$' -fuzz='^FuzzSearchRuneOffsets$' -fuzztime=30s -parallel=4 -timeout=90s ./internal/epub
gofumpt -d internal/epub/search{,_test,_bench_test}.go
goimports -d -local sayumi internal/epub/search{,_test,_bench_test}.go
golangci-lint run ./internal/epub/... --timeout=5m
make check
git diff --check
```

### Protected CP12 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint12/` (ignored). The true-original was
compiled before production edits from CP11 production at `857600b` plus the new
regressions and frozen harness. Both executables passed all sixteen one-iteration
benchmark smoke cases. Never overwrite the protected executables, harness, runner,
profiles, or original logs, including when a comparison is unfavorable.

- `before.test.exe` SHA256:
  `1143e33572839f9e763b56a59241093bf703878595fddfeaad5731ef5e8fdab5`.
- Frozen `internal/epub/search_bench_test.go` SHA256:
  `c813c2153dbd1f992753103b93a0f64183d6de22915d6133ab44b34acaa9c6e4`.
- Original `internal/epub/search.go` at `857600b` SHA256:
  `6ac069f2d413847af60bd77bc020dc44ceffe657976dd0842a1935a2a5c0e26b`.
- Verified/measured `after.test.exe` SHA256:
  `ae16dcddeb5659f8999eaf3e8db657cf6da6ded363ad00821442e0dddcc9221c`.
- Frozen `compare_benchmarks.py` SHA256:
  `d487f1f78d06dccda644efcbc8e1964c5b290eb114427e7eda56e6076affbd26`.
- `comparison.log` SHA256:
  `53006dbaec5bc2fa749ad00e7184151dfa0fc06df695a9e31afca222dc790fcd`.
- `make-check.log` SHA256:
  `e673b2bd4c02c81b0dfd082fb67d1c6b84f4a61c27769a1fb32d8b8ea44f4a1e`.
- `scoped-checks.log` SHA256:
  `6377a42866d7f8da81ced2428882bfb7bdf3663276de77f697f99d4c86d1d177`.
- Raw `runs/20260905T165700.925196Z/samples.csv` SHA256:
  `6b7e5e94c7a4499af25fc3a7b1968423d478121b6253a948b1905962a6cc359d`.
- `before-cpu.pprof` SHA256:
  `93d2459cbb6ee49d89689392a6598be7d854d0772bc58c4c3527dbfa72c4b35f`.
- `before-mem.pprof` SHA256:
  `2073250a72bc69773e55953f286aa71c0e8fefbc50b11c3558f26e968628d547`.
- `baseline-hashes.txt` and `candidate-hashes.txt` record build/source provenance;
  source hashes identify measured trees, not permanently frozen production files.
  `regression-before.log` retains the failing originals; `regression-after.log`
  retains the compiled candidate passes. Original pprof listings remain saved.
- The run directory also contains all twenty raw executable outputs, complete
  benchstat input/output, exact medians, and environment settings. Repeat only on
  the recorded source/test tree with no concurrent tests/builds:
  `python .agents/benchmarks/checkpoint12/compare_benchmarks.py 10`.
  The runner checks both executables, harness, and measured source/tests; alternates
  order; requires all sixteen rows per run; and invokes benchstat with sample ignored.
  Reruns get a new output directory; never overwrite comparison.log or rebuild the
  protected originals. Use a new checkpoint/harness for subsequent production changes.
- Reproduce the original failures (expected exit 1) without rebuilding; substitute
  `after.test.exe` for the verified passing comparison:

```sh
.agents/benchmarks/checkpoint12/before.test.exe \
  -test.run='^TestSearch(SanitizedText|Cancellation)$' -test.v -test.timeout=60s
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
