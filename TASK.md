# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**Checkpoint 13 is complete and verified: EPUB rewrite metadata, archive identity, and failure cleanup.**

Base commit: `abaae48 fix(epub): align search text and cancellation contracts`.
This handoff accompanies the scoped CP13 commit; use Git history for its hash.
CP6-CP12 remain finished; CP14 has not been started. The initial worktree was clean
with no running jobs. Root/internal instructions, TASK/TRACKER, relevant Go skills
and references, and actual dependency callers were read before edits.

- Reviewed scope: `internal/epub/edit.go`, `edit_test.go`, and new
  `edit_bench_test.go`. API editing, parser, reader, and store reads were dependency
  context, not newly completed file reviews. No API, frontend, cache, or dependency
  changes were needed.
- Reproduced a failed-copy temp leak and unintended rewrites of unselected ZIP
  entries. Cleanup now uses the owned file's stable name, accounts for source-close
  errors before handing back success, and closes each owned handle once. OPF and
  cover replacement use the selected entry's identity; other entries retain raw
  compressed bytes, methods, and order. The original archive stays unchanged.
- Enforced the existing 8 MiB OPF rewrite limit before opening and during reading,
  rather than after the generic bounded 64 MiB read. The unsupported-compression
  regression proves an oversized declaration is rejected before opening its body.
- Reproduced metadata readback failures for nested/out-of-metadata names, self-closed
  fields, clearing multiple creators, and attribute case/local-name collisions.
  Splices follow the existing parser's direct ancestry and case-sensitive local-name
  policy, preserve field attributes, update the decoded file-as value, and clear all
  creator values when requested. New Dublin Core/OPF nodes bind their namespaces
  explicitly. Extra roots, unfinished trailing structure, and outside text are refused.
- Missing declared covers are filled at their existing resolved path, and matching
  direct manifest media types are repaired, including absent attributes. Parser-selected
  image bytes are checked rather than assuming any new .jpg file is the selected cover.
  The unused mediaTypeForHref helper was removed after repository-wide reference tracing.
- Added deterministic archive/metadata regressions, namespace-aware insertion checks,
  and bounded immutable/readback/idempotence fuzzing. Strengthened legacy assertions
  and fixture error checks. No whole-OPF remarshal, new XML depth policy, filesystem
  abstraction, or injected late-close failure fixture was added.
- Preserved API generation-lock lifetimes, shared read-only OpenIndexed readers/indexes,
  one Release per borrow, drained Store.Close, exactly-once ResourceReader.Close,
  streaming Size (-1), and all derived-cache budgets/versioning. RewriteBook still owns
  an independent source reader and requires a stable source generation during the call.
- `TRACKER.md` inventory: 149 files, 87 complete and 62 pending. Only the three editing
  files were newly completed; the EPUB processing inventory is now complete. Pause
  after the scoped local commit. On continuation, reassess one bounded API or fonts
  batch from the remaining checklist rather than treating dependency reads as reviews.

### CP13 measurement and tradeoffs

Ten alternating paired samples, six cases at GOMAXPROCS 1 and 4, 300ms per case,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=1 for matching executables,
AMD Ryzen 7 5800H. Production remains CGO_ENABLED=0. No measurement overlapped tests
or builds. All 240 rows and all twenty executable outputs were retained. An independent
pandas/stdlib audit reparsed the raw outputs without the runner's regex and reproduced
all 72 metric medians and 36 deltas. Keys include the full benchmark family, so the two
CoverReplace cases cannot be merged accidentally. No values or outliers were excluded.

| Case | CPU 1 median before -> after | CPU 4 median before -> after |
| --- | --- | --- |
| OPF small metadata | 38.44 -> 38.83 us | 38.06 -> 38.05 us |
| OPF large metadata | 10.829 -> 10.678 ms | 10.797 -> 11.002 ms |
| OPF cover replacement | 37.00 -> 36.91 us | 36.94 -> 36.89 us |
| OPF cover insertion | 30.92 -> 32.14 us | 30.88 -> 32.06 us |
| Archive metadata | 1.595 -> 1.584 ms | 1.568 -> 1.561 ms |
| Archive cover replacement | 1.505 -> 1.508 ms | 1.481 -> 1.486 ms |

- No timing comparison was statistically significant at .05; no speedup is claimed.
  This is not evidence of equivalence. Cover insertion rose 3.94%/3.82% at CPUs 1/4
  (p=.052/.143); other timing p-values ranged .247-1.000. Correctness is not reverted
  to obtain a more favorable comparison.
- OPF allocated bytes/op rose 1.40%-3.81% (all p<.001). Small metadata: 11392 -> 11552
  B/op and 250 -> 255 allocations; large metadata: about 3.683 -> 3.809 MB/op and
  67865 -> 67870 allocations. Cover replacement: 10080 -> 10256 B/op with 248
  allocations unchanged. Cover insertion: 9443 -> 9803 B/op and 201 -> 204 allocations.
  These are allocation-volume costs, not peak-memory measurements.
- Archive metadata medians gained 5/4 allocations at CPUs 1/4 (both p<.001); cover
  replacement allocation counts remained 438. Archive bytes/op differences were not
  significant; exact byte/count medians and p-values remain in the archived outputs.
- Limits: one host/toolchain, synthetic fixtures, warm OS/ZIP caches, and ordinary
  unadjusted benchstat comparisons. The large OPF adds 2048 manifest items; archive
  fixtures include a 288 KiB stored resource. Setup, JPEG generation, warmup, and
  semantic checks are outside timing. Archive timing includes creation, copy, Sync,
  Close, and output removal. CPU settings are not parallel-edit throughput. No
  application-wide improvement or aggregate geomean claim is made.

### Verification

Focused regressions, full EPUB tests, five shuffled race repetitions at CPUs 1/4,
pure-Go EPUB tests, formatting/import checks, and scoped lint passed. A 30-second,
four-worker OPF fuzz campaign completed 343892 executions without failure with a
64 KiB input cap. Full `make check` passed: tidy, Go/frontend formatting, vet, lint,
vulnerability checks, all-package shuffled race tests, frontend lint/types/tests,
and frontend/pure-Go builds. The initial embed preflight reused existing frontend
output; the final gate performed both builds. Measured source, tests, harness, and
both executable hashes remained unchanged through the check. All jobs exited.
Skills and generated benchmark artifacts remain ignored/untracked; CP7-CP12 protected
artifacts and historical source hashes were rechecked unchanged.

```sh
go test -count=1 -run '^(TestRewrite(Book|OPF)|TestApplySplices|FuzzRewriteOPF)' -timeout=60s ./internal/epub
go test -count=1 -shuffle=on -timeout=60s ./internal/epub
go test -race -shuffle=on -count=5 -cpu=1,4 -timeout=120s ./internal/epub
CGO_ENABLED=0 go test -count=1 -timeout=60s ./internal/epub
go test -run='^$' -fuzz='^FuzzRewriteOPFRoundTrip$' -fuzztime=30s -parallel=4 -timeout=90s ./internal/epub
gofumpt -d internal/epub/edit{,_test,_bench_test}.go
goimports -d -local sayumi internal/epub/edit{,_test,_bench_test}.go
golangci-lint run ./internal/epub/... --timeout=5m
make check
git diff --check
```

### Protected CP13 benchmark artifacts

Directory: `.agents/benchmarks/checkpoint13/` (ignored). The true-original was
compiled before production edits from CP12 production at `abaae48`, with the initial
regressions and self-contained frozen harness. Both executables passed all twelve
one-iteration smoke cases. Never overwrite the protected executables, harness, runner,
or original logs, including unfavorable comparisons or failed intermediate checks.

- `before.test.exe` SHA256:
  `32df15f774c80fcc80f7b768addd7636ed0d6b2e3794b1c45b2706b6619e69b0`.
- Frozen `internal/epub/edit_bench_test.go` SHA256:
  `d3c6b7f6f614d39b6cd0dc84f2389f3b53dfd0b7585789bf6c3f7b5b0bb62672`.
- Original `internal/epub/edit.go` at `abaae48` SHA256:
  `68ab08b9effcf1cd74296c8dd025380b5247b774f87e51538f5283b53ac68691`.
- Verified/measured `after.test.exe` SHA256:
  `00d39208813da8a9aabd910fb09bc824d1bf071d01c1b5881805e7bce31171d8`.
- Frozen `compare_benchmarks.py` SHA256:
  `41005094c13a8e1345c33e24dd833572a2fee95e9f46337311cb63281df87cc1`.
- `comparison.log` SHA256:
  `6e745c9a7d7e28ad4a250141a1a47af19566902635d09663d2baeca4f2f0fd9a`.
- `scoped-checks-final.log` SHA256:
  `be78dd36d52acaa18f727257b04e6cd228b43f2c5c282f436eadc564c8c027a4`.
- `make-check.log` SHA256:
  `9a077e27d947b5dc07fbc3a7ae62a6eb837609b4c9b45c87622c3488fe532ccc`.
- Raw `runs/20260905T174311.481897Z/samples.csv` SHA256:
  `b6ef6b6698308b791ee61ad95e3c23bce114288eac4d2b349cbbaf58ebe336a9`.
- Exact `runs/20260905T174311.481897Z/medians.csv` SHA256:
  `6d4e6e949aa4fb81d74fcc0440dd8a3411e3f653977abbce7e57ac9ad3c6a819`.
- `baseline-hashes.txt` and `candidate-hashes.txt` record build/source provenance;
  measured source hashes are not permanent production freezes. `regression-before.log`
  preserves the initial failures; `structural-regression-before.log` preserves the
  additional attribute/trailing-structure failures against the first candidate.
  `regression-after.log` contains the compiled final candidate passes. Failed lint
  and intermediate check logs are retained separately; use scoped-checks-final.log
  for the successful final scoped gate. `protected-after.log` records the final audit.
- The run directory contains all twenty raw outputs, complete benchstat input/output,
  exact medians, and environment settings. Repeat only on the recorded source/test
  tree with no concurrent tests/builds:
  `python .agents/benchmarks/checkpoint13/compare_benchmarks.py 10`.
  The runner checks both binaries, harness, and measured source/tests; alternates
  order; requires twelve distinct family/case/CPU rows per run; and ignores sample
  labels in benchstat. Reruns get new output directories. Never overwrite comparison.log
  or rebuild the originals; use a new checkpoint/harness for later production changes.
- Reproduce the initial failures (expected exit 1) without rebuilding; substitute
  `after.test.exe` for the verified passing comparison:

```sh
.agents/benchmarks/checkpoint13/before.test.exe \
  -test.run='^(TestRewriteBook(FailedCopyRemovesTemp|PreservesUnselectedEntries|RejectsOversizeOPFBeforeOpening|CoverReadback)|TestRewriteOPF)' \
  -test.v -test.timeout=60s
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

Pause after the scoped CP13 commit and report. On the next explicit continuation,
reassess one bounded remaining API or fonts batch from `TRACKER.md`. The EPUB
processing inventory is complete. Confirm the dependency boundary from actual
files and Git state before editing. Do not repeat CP6-CP13 or count dependency
reads as completed reviews. Preserve all protected artifacts.
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
