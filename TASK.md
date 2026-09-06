# Backend review task

## Overall goal

Review Sayumi's Go backend file by file. Improve correctness, idiomatic Go,
maintainability, comments, tooling, and CLI usability. Remove genuinely unused
code only after tracing references and verifying behavior. Keep performance
changes only when repeatable benchmarks support them where measurement is practical.
Preserve the local-first design, existing API contracts, and pure-Go build.

## Current task

**CP19 settings API review is complete and verified.**

Base: `dca264d0dc5eddb38ccf86d88752de8b08ba6dce` (completed CP18).
This handoff accompanies one scoped local CP19 commit; use Git history for its hash.

- Fully reviewed `internal/api/setting.go` and `setting_test.go`, and added/reviewed
  `setting_contract_test.go`. The original five tests remain unchanged. Only
  `setting.go`, the new contract test file, `TASK.md`, and `TRACKER.md` changed.
- Corrected two diagnostics: the theme's 32-byte ceiling now says bytes rather
  than characters, and an oversized font-role family ID reports the shared
  128-byte ceiling. Settings PUT and preset creation share these messages.
  No acceptance bounds, normalization rules, defaults, storage mappings, response
  shapes, authorization, or profile-reference ownership changed.
- Clarified the stored-record versus fresh-profile defaults beside the converter.
  Explicit Auto/NULL title and indent fields remain Auto on reload; NULL margins
  retain the separate legacy 48px fallback. PUT remains a complete snapshot,
  not a patch, including false booleans, explicit zeroes, and cleared optionals.
- Presets, auth, router, profile-manager, storage, frontend and tooling reads were
  dependency context, not additional review credit. No dependency/toolchain churn,
  frontend edits, speculative optimization, or dead-code removal.
- Inventory: **160 files, 114 done, 46 pending (38 API + 8 tooling)**. The new test
  file adds one inventory entry; two previously pending files are now done.
  The overall completion gate stays unchecked. CP20 has not started.

### CP19 verification and limitations

- Started on `main` at the CP18 commit with a clean index/working tree and no
  running jobs. The original five settings tests passed before edits. With new
  tests and unchanged production, exactly four diagnostic regressions failed:
  theme and font-role ID messages through both settings PUT and preset creation.
  All other new tests passed on that original production source.
- All **19 scoped top-level tests** now pass: 20 shuffled repetitions at each
  GOMAXPROCS 1/4 with CGO disabled, and five at each setting with `-race`.
  Coverage includes all 20 numeric field boundaries and optional NULLs, enum/byte
  limits, role filenames and pruning, nullable conversions, fresh defaults,
  complete SQLite snapshot round-trips, resets, invalid/oversized writes leaving
  saved settings unchanged, canceled/closed DB errors, missing profiles,
  authentication gates, profile isolation, and borrowed-reference ownership.
  Exact 64KiB bodies with known/unknown lengths, unknown fields and duplicate
  scalar keys retain the existing decoder compatibility.
- Persistence tests use real isolated temporary SQLite databases and request
  recorders, not SQL mocks or an external service. Ownership fixtures model the
  middleware's borrowed reference; this is not a new end-to-end session or
  profile-manager lifecycle test. Host execution was Windows/amd64, Go 1.27.0;
  the integer-conversion test's 32-bit overflow arm was not executed on this host.
- Fresh full **make check passed all ten gates** on the final Go sources: tidy,
  Go/frontend formatting, vet, lint, vulnerability scan, seven Go packages with
  race/shuffle, frontend lint/types/tests, and frontend/pure-Go builds. The initial
  embed gate reused existing output; the final build gate rebuilt the frontend.
  No lint suppression, broad formatter rewrite, or test-only follow-up fix was needed.
- Only diagnostic strings and comments changed in production. No performance
  measurement was warranted or run, and no performance/equivalence claim is made.
  No new fuzz campaign, coverage percentage, network, or app-wide claim is implied.
- Protected evidence was verified before and after the work using only
  `python .agents/benchmarks/checkpoint18/comparison.py verify`. CP6-CP18 and the
  completed side quest remain protected below; no measurement reruns, freezes,
  reseals, or protected-artifact edits. `.skills/` and `.agents/MCP_Feedback.md`
  remain ignored and untracked. No delegation, amend, push, or automatic CP20.

### Protected CP18 evidence

Directory: `.agents/benchmarks/checkpoint18/` (ignored). The manifest at
`validated/evidence-manifest.json` seals **110 artifact files plus the manifest**
across the whole CP18 directory, including the initial failed baseline, validated
comparison, regressions, both full checks/fuzz runs, coverage, and independent audit.
Preserve all three executables and both benchmark-harness versions. Never rerun
freeze/measure/seal modes from either driver or overwrite evidence. Only the current
benchmark harness is frozen in source; other source hashes are historical provenance,
not a prohibition on legitimate later checkpoint edits.

- Manifest SHA256: `d20e1401120b51f58704cddacd6ad2c34c8b5e176e7d1ba4151ae670cc4ba683`
- Validated original executable: `b0c136ef20ce33340de947717452759c98bff02ddd1600adb66a7695a7a06d29`
- Candidate executable: `a988e2edb2d381c14920c45aa143e915489bc1f98033c0a1b7675572b491a064`
- Frozen harness: `0152ecff087e645960347a427697502c39f2574036f67b7d24a6ecebd9e35efd`

Safe read-only verification, including all earlier protected evidence:

```sh
python .agents/benchmarks/checkpoint18/comparison.py verify
```

### Protected CP17 evidence

Directory: `.agents/benchmarks/checkpoint17/` (ignored). The seal covers **83 artifact
files plus the manifest** and the frozen throttle benchmark harness. Preserve both
executables, original source, provenance, plan, all raw runs/receipts, aggregate logs,
benchstat, regression/check/coverage/fuzz evidence, and independent audit files.
Never rerun freeze/measure/seal modes or overwrite their outputs. Production hashes
record historical provenance and do not prohibit legitimate later checkpoints.

- Manifest SHA256: `839e49274af3456fbbec9bfb1a73d118731e3d98129828413d5357b7dc9bc797`
- Original executable: `91068c785230a4dfa618c1bb4ca27a4c2cf58fa22ddd056350b25ec35748c318`
- Candidate executable: `f2e01dd20e24138ba3a97d799d1f056f792bc88b8d81b45adf648c4540e56b88`
- Frozen harness: `76c5cc5595a914eb98eb39a36883ee23379d280bcea1d453a32aa05493b9add0`

Safe read-only verification, including prior protected evidence:

```sh
python .agents/benchmarks/checkpoint17/checkpoint.py verify
```

### Protected CP16 evidence

Directory: `.agents/benchmarks/checkpoint16/` (ignored). The seal covers **94 artifact
files plus the manifest** and the frozen benchmark harness. Preserve the original/
candidate executables, provenance, all twenty raw runs/receipts, plan, aggregate logs,
benchstat output, regression/check/fuzz logs, intermediate failures, native trailer
probe, audit script and `independent/{profile.json,summary.csv,audit-receipt.json}`.
Do not rerun freeze/measure/seal modes or overwrite their outputs. Production and
handoff hashes record history; they do not prohibit legitimate later checkpoints.

- Manifest SHA256: `a285e4e20da559ce0536ad07e3c5d70120a80ab054aeef05d17f39c7d966755d`
- Original executable: `461d7440926cab92d1cb4d0ac4fe7e1a245218310adf4cce6154122fc2c895fd`
- Candidate executable: `2f8a0ba8b94ed8aba0be646d296b455f93b8dc4dc6d20bc6b69bdfc5cb0c8f6f`
- Frozen harness: `398a6d21397d40f05410685f03bcfe1130e4eacac4d20a0ff05543a67c2634dc`

Safe verification, which also checks all CP7–CP15/side-quest protections:

```sh
python .agents/benchmarks/checkpoint16/seal_cp16.py verify
```

### Retained CP15 outcome

CP15 font embedding/scanning is complete and verified.

Base commit: `bc9b2cd04e48d998bf12a234346161a70f0125a7`
(`fix(reader): make font normalization resilient`, completed side quest).
This handoff accompanies one scoped local CP15 commit; use Git history for its hash.
The interrupted changes and already-frozen original executable were recovered and
verified, not reset or rebuilt from the candidate.

- Fully reviewed `internal/fonts/embed.go`, `embed_test.go`, `scan.go`, and
  `scan_test.go`; added and froze `embed_bench_test.go` and `scan_bench_test.go`.
  Only those six font files plus `TASK.md`/`TRACKER.md` changed. API/router/frontend
  callers were dependency context only, not edits or completed reviews. No subagents,
  dependency/toolchain changes, or changes to CP14 metrics/sfnt code.
- Correct conditional GET/HEAD behavior: weak entity-tag comparison across multiple
  header lines, quoted commas, existing-file checks before wildcard matches, and
  no HEAD error body. Failed requests clear success entity headers and use no-store,
  including a file disappearing between conditional stat and read. Literal dots,
  Unicode, percent signs, and encoded filenames work without double decoding.
- Discover fonts, read optional metadata, and extract metrics within the same
  `os.Root` boundary used for serving. Reject escaping/dangling links and nonregular
  files; preserve supported relative in-root links and deterministic ordering.
  Limit optional family.json to 64 KiB; retain the existing 8 MiB metrics-extraction
  cap (not a font-serving size limit).
- A new directory-link regression reproduced an index-bounds panic in this host's
  Go 1.27.0 rooted-path resolver. A narrow guard at rooted Stat/Open boundaries
  fails closed for that runtime panic; unrelated panic categories are rethrown.
  No unrestricted-path fallback or toolchain workaround was introduced.
- Exclude numeric `-700` bold faces from the non-bold regular fallback. The legacy
  last-resort first-file fallback remains when no suitable face exists. Preserve
  borrowed immutable snapshots, membership indexes, publication/concurrency locks,
  API token URLs, CORS, and successful font-cache policies. User ETags still use
  size/mtime, not content hashes; this is not a universal hostile-filesystem guarantee.
- `TRACKER.md` reconciles to **153 files, 98 complete, 55 pending**: API 47 and
  tooling eight. The fonts and EPUB inventories are complete. The overall review
  gate remains unchecked. **Stop here; no automatic CP16.**

### CP15 verification

- Original regression failures are preserved. Additional directory/dangling-link,
  cached-path replacement, escaping metadata/file/family, supported in-root link,
  encoded-path, weak-validator, error-cache, and snapshot/concurrency cases pass.
  The link cases actually ran on this host; they were not permission skips.
- Pure-Go and race-enabled font suites each passed 20 shuffled repetitions at
  GOMAXPROCS 1 and 4. Pure-Go statement coverage: **93.0%**. The 30-second weak-ETag
  fuzz campaign passed **721,584 executions**, with the initial seeds also covered.
- Fresh full `make check` passed: tidy, Go/frontend formatting, vet, lint,
  vulnerability checks, all-package shuffled race tests, frontend lint/types/tests,
  and final frontend/pure-Go builds. Only the initial embed preflight reused dist;
  the final build gate rebuilt both. Scoped gofumpt/goimports and lint also passed.
- Reverified all 226 CP7-CP14 artifacts, 109 side-quest evidence files, and 18 earlier
  frozen Go harnesses. The two CP15 harnesses and measured candidate sources also
  remain byte-identical. Skills and generated evidence stay ignored/untracked.

### CP15 measurement and tradeoffs

Ten predeclared, alternating AB/BA pairs ran strictly serially with frozen binaries:
**20 raw outputs, 400 observations**, ten samples per version/case/GOMAXPROCS.
No missing, duplicate, excluded, or overlapping executions. Independent raw parsing,
pandas reconciliation/profiling, and stdlib checks agree on all **120 metric medians**.
Go 1.27.0/windows/amd64, Ryzen 7 5800H, CGO=0, GOAMD64=v1, GOGC=100, no memory limit;
binary build settings/dependencies match. Each case ran for 200 ms at GOMAXPROCS 1/4.
The fixed real WOFF2 fixture is 47,792 bytes; Rescan8 uses eight copied families.
Setup/initial scan are untimed; filesystem caches were not flushed. Handler costs
include the response recorder/body, not network sockets or browser rendering.

Selected timing costs at GOMAXPROCS 1 (changes of marginal medians):

| Case | Before | After | Change | benchstat p, n=10 |
| --- | --- | --- | --- | --- |
| UserHEAD | 78.0205 us/op | 79.2460 us/op | +1.57% | .019 |
| MissingGET | 1.7135 us/op | 1.9180 us/op | +11.93% | .012 |
| Rescan8 | 12.8035 ms/op | 13.3859 ms/op | +4.55% | .015 |

The other 17 timing comparisons were inconclusive by benchstat; that is not proof
of equivalence. These p-values are unadjusted across 20 timing comparisons.
MissingGET adds 32 B/op and one allocation at both settings. Rescan8 allocation
medians rise from 652 to 1038.5/1038 (+about 59%); byte medians rise about 6.2-6.7 KiB
against roughly 4.3 MiB/op, with inconclusive byte tests. Cached Families remains
zero-allocation; other handler allocation counts are unchanged. These are retained
confinement/error-contract costs, not performance optimizations. No application,
startup, throughput, peak-memory, cold-disk, browser, or cross-host speedup is claimed.

### Protected CP15 evidence

Keep `.agents/benchmarks/checkpoint15/` ignored/untracked. `evidence-manifest.json`
seals 65 artifact files (excluding itself), including original/failed/setup logs,
both binaries/provenance records, the predeclared plan, 20 raw outputs, CSV/benchstat,
and the independent audit receipt/reproducer. Do not overwrite or rerun either
freeze routine or overwrite the comparison outputs. Recorded production hashes
identify the measured tree; they do not prohibit later legitimate source edits.

- Manifest SHA-256: `467aa0c4a6eb77519a079ae93261edc5faa3ce8792cec69a5f78cd9e6771637e`.
- Original binary SHA-256: `623b194ee250be20635b706ec83c6226a94a9809138e8ca11d97b7ed0aa17aee`.
- Candidate binary SHA-256: `2fcd915882668dce085d1d1722558b9d746ff396fa48e82debfefc14b5b278ca`.
- Recheck evidence: `python .agents/benchmarks/checkpoint15/seal_cp15.py verify`.
- Reproduce the independent analysis with `audit_cp15.py INPUT_DIR FRESH_OUTPUT_DIR`
  in a pandas-equipped environment. It refuses to overwrite its outputs. Analysis
  used the computer's existing Python 3.13.14/pandas 3.0.5; no project dependency added.

The retained sections below are prior provenance, not work rerun in CP15.

### Retained side-quest verification

- Full frontend lint, types, formatting and tests passed: 73 test files, 979 passed,
  one pre-existing conditional CSSOM skip. Tests cover retry, fallback coverage,
  consensus/outliers, raster failure, timeout/abort/cleanup, bounded caches/workers,
  role snapshots, metadata/leading guards, and five reader ownership/settings cases.
- Chrome 152 on Windows passed all 13 frozen browser cases (the original passed
  seven and failed six), including TTF, WOFF1, CFF-OTF and a real variable WOFF2.
  Five additional integration cases applied the generated CSS and verified painted
  ink plus line-box heights. No temporary probe faces or test Chrome processes remain.
- Ten generated fonts were fully decompiled by local FontTools 4.63.0 and their
  metadata/cmap coverage independently checked. Fixture generation used FontTools
  4.64.0 on the computer. The local CLI and uv were already installed; no installation
  or project dependency was added.
- Fresh full `make check` passed: tidy, Go/frontend formatting, vet, lint,
  vulnerability checks, all-package shuffled race tests, frontend types/tests,
  and final frontend/pure-Go builds. The initial embed preflight reused existing dist;
  the final build gate rebuilt both. Go parser code was unchanged; no new Go fuzz
  campaign was needed (the protected CP14 campaign below is prior evidence).
- All 226 CP7-CP14 artifact files and all 18 tracked Go benchmark harnesses were
  reverified unchanged. Skills and generated evidence remain ignored/untracked.

### Retained side-quest measurement and tradeoffs

Ten alternating, strictly serial before/after pairs used the frozen original bundle
and verified candidate with identical inputs/flags, without overlapping tests/builds.
Three workloads produce **60 observations in 20 raw outputs**, with no exclusions.
Reference/font HTTP caches are warmed in both variants. The synthetic missing.ttf
fixture is used for 120 fresh-registry calls, 500 same-registry calls, and 2000 CSS
builds per output. This is one Windows 11/Chrome 152 host, not app-level performance.

| Workload | Before median ms/call | After median ms/call | Change of medians |
| --- | --- | --- | --- |
| Fresh registry identity | 0.1150 | 0.3875 | +236.96% |
| Same registry identity | 0.1313 | 0.0093 | -92.92% |
| Build font CSS | 0.005900 | 0.005925 | +0.42% |

Fresh calibration was slower in all ten pairs; the paired median absolute cost was
+0.2642 ms/call (paired percentage range +164.04% to +337.19%). Retained as a
correctness/coverage cost, not an optimization. Same-identity calls were faster in
all ten pairs, but the old reader already avoided repeated same-file measurements;
this module-cache result is **not an application speedup**. CSS-building results were
inconclusive (four slower/six faster). Exact paired sign-test p-values were .001953,
.001953 and .753906, unadjusted across three workloads. Marginal-median changes above
are not the median of within-pair percentages. Full audit records both. No startup,
throughput, JS allocation-volume, peak-memory, or cross-browser claim is made.

### Protected font-normalization evidence

Keep all of `.agents/benchmarks/font-normalization/` ignored/untracked, including
failed/setup logs and unused transfer drafts. Do not overwrite the frozen bundles,
entry, harnesses, fixture corpus, raw results, or audit. `evidence-manifest.json`
records 108 evidence files (excluding itself), plus CP7-CP14 tree fingerprints and
tracked benchmark hashes; SHA-256
`a16c92e1bdf5a46c0c655b8cc0542f37697d7b625b840d6bbc9c1f48eee72258`.

- `before.mjs`: `011a6ddd21992937f19a89c254988f0d7796ceb361497ce73d5ff687ed880912`.
- `after.mjs`: `9f4eea081b82c5eb03c0aaa7eb0e37e34e44b65d957766129e4ef12dddc3df20`.
- `fixtures.json`: `f6c79a190a796b3d8f5a6d1af1c0ebc5f9be659c724949f0c05c1456650a95c8`.
- `before-provenance.json`: `511ea674fa52f150fdd0e7afb38138b756c3b626a552488cc86c8da95452e303`;
  captures pre-edit sources, embedded fonts, harness hashes and CP7-CP14 protections.
- `measurement-provenance.json`: `92227e65dededacdb7e24e3f0eb1d9442f25bc8deb515d82620c4be12857d607`;
  captures all six measured source/test hashes and the candidate. These are provenance,
  not permanent bans on legitimate future production changes.
- Paired run: `runs/20260906T044945.609844Z/`; `samples.json` SHA-256
  `5c22ad3876f7fbed927d468d4b5bffb596c030b4eca09337492c27cd551ad2fb`.
  All 60 rows were transferred with an exact byte/hash check and independently audited
  using pandas/stdlib. `audit_measurements.py` preserves the portable audit procedure;
  `measurement-audit.json`: `72f6c166f2779206f33422a4dda0f5cc8ca7facc713f929139d80e63a527a50f`.
- Original browser run: `runs/20260905T200139.641283Z/`; candidate:
  `runs/20260906T044721.205664Z/`; rendered CSS: `layout-runs/20260906T044721.177920Z/`.
- `comparison.log`: `6bef3d79602efafb170c422ce44e39bde1537d415613d2e1534264e755d2eec0`.
- `make-check.log`: `4a45ae4f193229c1de070dde5297e30ae5d1939c298dd769a01728500ebff0df`.

The CP14 measurement/verification material below is retained baseline provenance,
not work repeated as part of this side quest.

### CP14 measurement and tradeoffs

Ten alternating serial pairs, seven cases at GOMAXPROCS 1 and 4, 200ms per case,
Go 1.27.0 windows/amd64, GOAMD64=v1, CGO_ENABLED=0 for both matching executables,
AMD Ryzen 7 5800H, GOGC=100 and GOMEMLIMIT=off. No measurement overlapped tests or
builds. All 280 rows and twenty executable outputs were retained. An independent
parser bound each full case/CPU name to its fixture size/hash, reconciled every
CSV field through a canonical full-row hash, and used pandas plus stdlib to verify
all 84 metric medians and 42 relative changes. No missing, duplicate, or excluded
measurements were found. Benchstat used its ordinary unadjusted comparisons.

| Case | CPU 1 median time change (p) | CPU 4 median time change (p) |
| --- | --- | --- |
| SFNT minimal | +16.26% (<.001) | +14.20% (<.001) |
| SFNT 256 KiB glyph payload | +16.93% (<.001) | +17.55% (<.001) |
| WOFF1 raw 256 KiB | +25.54% (<.001) | +26.44% (<.001) |
| WOFF1 zlib 256 KiB | -1.16% (.631) | +1.18% (.796) |
| WOFF2 synthetic 256 KiB | +1.68% (.529) | +1.61% (.247) |
| WOFF2 embedded Atkinson | +0.41% (.684) | -0.07% (.912) |
| WOFF2 embedded Literata italic | -0.04% (1.000) | -0.10% (.796) |

- Raw-container validation adds 47.5-94.4 ns/op to these tiny directory/header
  workloads; all 60 corresponding within-pair time differences were positive.
  Their B/op and allocation counts were unchanged. Retained as measured
  correctness/validation costs, not as a speed optimization.
- None of the eight compressed-font timing comparisons was significant at .05;
  no speedup or equivalence is claimed. WOFF1 zlib gained 9 median B/op and two
  allocations (26 -> 28). WOFF2 synthetic gained 5 B/op and one allocation
  (22 -> 23); Atkinson gained 4.5 median B/op at each CPU setting (23 -> 24);
  Literata gained 14/13.5 median B/op at CPUs 1/4 (24 -> 25). These byte/count
  increases were significant at p<.001 despite rounded benchstat byte percentages
  displaying +0.00%. They are allocation-volume costs, not peak-memory measurements.
- Limits: one host/toolchain, five synthetic cases and the smallest/largest embedded
  WOFF2 faces. Fixture construction, compression, and fingerprint logging are outside
  the timed loops; ReadMetrics and success/em checks are inside. Raw SFNT/WOFF cases
  alias the large glyph payload rather than traversing it, so their reported MB/s is
  not full-font throughput. CPU settings are not parallel-reader throughput. No
  application-wide improvement or aggregate-geomean claim is made.

### Verification

Focused regressions, full shuffled fonts tests, five shuffled race repetitions at
CPUs 1/4, pure-Go fonts tests, formatting/import checks, and scoped lint passed.
A 30-second, four-worker container fuzz campaign completed 872921 executions without
failure with a 64 KiB input cap. Full `make check` passed: tidy, Go/frontend formatting,
vet, lint, vulnerability checks, all-package shuffled race tests, frontend lint/types/
tests, and frontend/pure-Go builds. The initial embed preflight reused existing output;
the final gate performed both builds. Measured source, tests, harness, and both
executable hashes remained unchanged. All jobs exited. Skills and generated benchmark
artifacts remain ignored/untracked. All 179 files in CP7-CP13 artifact directories,
recorded protected artifacts, and historical source hashes were rechecked unchanged.

Non-overwriting verification commands:

```sh
go test -count=1 -run '^(TestFont|TestWOFF|TestReadMetricsMinimumDescender|TestReadMetricsOptionalOS2)' -v -timeout=60s ./internal/fonts
go test -count=1 -shuffle=on -timeout=90s ./internal/fonts
go test -race -shuffle=on -count=5 -cpu=1,4 -timeout=180s ./internal/fonts
CGO_ENABLED=0 go test -count=1 -timeout=90s ./internal/fonts
go test -run='^$' -fuzz='^FuzzReadMetricsContainers$' -fuzztime=30s -parallel=4 -timeout=90s ./internal/fonts
gofumpt -d internal/fonts/{metrics,metrics_test,metrics_bench_test,sfnt,sfnt_test}.go
goimports -d -local sayumi internal/fonts/{metrics,metrics_test,metrics_bench_test,sfnt,sfnt_test}.go
golangci-lint run ./internal/fonts/... --timeout=5m
make check
python .agents/benchmarks/checkpoint14/verify_protected.py --final
git diff --check
```

### Protected CP14 benchmark artifacts

Keep `.agents/benchmarks/checkpoint14/` ignored and untracked. Do not overwrite its
binaries, runner, raw samples, failed/intermediate logs, or the tracked frozen harness.
The original executable was built once from base `4cdf722` production with the same
CP14 tests/harness as the candidate. Both use the pure-Go build and identical recorded
toolchain/settings. The candidate's source hashes identify the measured revision,
not permanent freezes on production code. Baseline failures are intentional regression
evidence, not candidate failures. Keep the initial lint-failure `baseline-setup.log` too.

- Original executable: `.agents/benchmarks/checkpoint14/before.test.exe`
  SHA-256 `968182c15bee06bdcf48068bf8b842b37ba6d431a17340330855d6cbbb6b2182`.
- Candidate executable: `.agents/benchmarks/checkpoint14/after.test.exe`
  SHA-256 `71d212ff6db36ece9b22b146ee7c1a8b5cd65bc2cb3d9b559258163814486a06`.
- Frozen harness: `internal/fonts/metrics_bench_test.go`
  SHA-256 `45dbc106c0f21c047ea3f04f9fb84996af4496c7aaa232dcae575c207c577ddc`.
- Runner: `.agents/benchmarks/checkpoint14/compare_benchmarks.py`
  SHA-256 `ee3729bcbb51a0438b78ee9c31e2ae1d885a75f260348c757d9dc3b58b7aec8d`.
- Original `internal/fonts/metrics.go` / `sfnt.go` at `4cdf722`:
  `043980b742f394235f75dc359eece2aafc4a7e5809a9c636808eec2716f11624` /
  `b215b8c96f79a9cecb551b50c74863077b339a4fc5220b1ab1bbd3e1dfe7dce2`.
- Candidate `internal/fonts/metrics.go` / `sfnt.go`:
  `7b7e509f35025c2c82f94803e8b69dd977dac8d7eac25b572afaf667d87ad83c` /
  `af6174a516709402dd4723be15b8be85e482f0fe33e32da61e75d501c58b9369`.
- Matching `internal/fonts/metrics_test.go` / `sfnt_test.go`:
  `9b550322508b4a57cdec7251a14efb1f5d21a3817645a190466b381343565e28` /
  `9a75338715f306ef74a538516148dbd4dc412b331971a08b452149bccfee4541`.
- Comparison log: `.agents/benchmarks/checkpoint14/comparison.log`
  SHA-256 `54d7e142338933eb30f11d7b2cbb36c6801d3d381b2ad97182d20a8ab6afcae7`.
- Scoped checks: `.agents/benchmarks/checkpoint14/scoped-checks.log`
  SHA-256 `4fd5ed6c7f2c2192091bca58f605c8c42a7d035aeedef3b5ae26fdc778fa7b01`.
- Full check: `.agents/benchmarks/checkpoint14/make-check.log`
  SHA-256 `21e990a3ff28f3481881a6155cf19708b781d01ae37a53dd70e7b93453d39010`.
- Run directory: `.agents/benchmarks/checkpoint14/runs/20260905T185916.341529Z/`.
  `samples.csv`: `6e85f57613cec827e6ab3917742168be6a3051fe26e4e9d6ed8254f3beda5e00`;
  `medians.json`: `9f844ec2b6b6c5c7f585a2818c5677c462ead8c720ea711e2d8863c7422ec457`;
  `benchstat.txt`: `7277a1ad514affa99b71b758f9fbd870a5b02717bf05260458b483a1f0635445`;
  `environment.json`: `624073227e041d4b68e1ec645b9337add0fd7a9199d9a4e810f091d5d3f3507a`.
  Preserve all twenty per-executable raw outputs and both benchstat inputs/outputs.
- Independent numerical audit: `.agents/benchmarks/checkpoint14/independent-audit.json`
  SHA-256 `59d45941eabf1d31681b8b1e0a6da2bcb5827916a614b9581d3e756b5d6f08f4`.
- Protection helper: `.agents/benchmarks/checkpoint14/verify_protected.py`
  SHA-256 `62d58503b79a07a690da3c91a7608fb7884420847c3f0ddbeea989e257259e8c`;
  final protection log `protected-after.log`:
  `3504e7c6588d10c4fcc656e7ed0b832b82b171d4e0e3cce3a6348bae92c080c8`.
  The old CP11 helper covers CP7-CP10 only; this helper adds CP11-CP13 and compares
  all CP7-CP13 directory fingerprints with `protected-before.log`. It does not cover
  the new CP14 directory; preserve the separate CP14 snapshot below as well.
- Complete CP14 directory snapshot: 47 files,
  SHA-256 `d7b16fcd8fe6ab625f0f96a76ac4bb7e2717b964a2e74cba89ad9c226f045924`.
  Fingerprint: sorted relative POSIX paths, each followed by NUL and its file's
  SHA-256 digest bytes. This includes failed/unfavorable evidence and all run files.

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

Pause after the scoped CP19 commit and report. CP20 has not started. On the next
explicit continuation, reassess one bounded API or tooling batch for CP20 from
`TRACKER.md`; 38 API files and eight tooling files remain. Settings and the prior
JSON/context, throttle, gzip, fonts and EPUB batches are complete; presets, auth,
router and profile-manager reviews remain pending. Confirm the dependency boundary
from actual files and Git state before editing. Do not repeat CP6-CP19, rerun the
completed side quest, or count dependency reads as completed reviews. Preserve all
protected artifacts, including CP18.
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
