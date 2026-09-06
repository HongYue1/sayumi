# Next task: CP21

**Status: not started.** Complete exactly one bounded pending API or tooling review
checkpoint, then stop. Select the batch after inspecting its actual dependencies.

This file holds the active task, or the next task once the active one is complete.
`TRACKER.md` holds file status. Git is the changelog: do not retain completed-task
narratives, benchmark tables, session logs, or copied commit histories here.

## Start safely

1. Open `C:\Users\Administrator\Documents\Projects\GO\sayumi` through Local_MCP;
   reuse an already-open workspace for this folder. These Markdown files are local
   repository files, not websites.
2. Read `AGENTS.md`, applicable nested instructions, this file, and `TRACKER.md`.
   Read the relevant skills/references under `.skills/cc-skills-golang/skills/`
   before applying their techniques; repository rules override generic advice.
3. Confirm the actual branch, HEAD, index, working tree, existing diffs, and running
   jobs before editing. The expected branch is `main`; preserve unrelated work.
   Do not reset an advanced checkout to a handoff commit or discard staged changes.
4. Run the protected-evidence verification below. Choose one small, coherent batch
   from the pending inventory; inspect dependency boundaries before fixing scope.
   Record only that active scope and any real blockers here while work is underway.

## Review constraints

- Work directly without delegation. Review each credited file in full, including
  its tests and relevant callers. Dependency reads and passing tests alone do not
  count as completed reviews. Do not repeat completed batches.
- Improve correctness, idiomatic Go, security, and justified simplicity. Trace
  references and supported build configurations before removing allegedly dead code.
  Avoid speculative optimization, cosmetic churn, and dependency/toolchain changes.
- Preserve API/storage boundaries, pure-Go SQLite, profile isolation and reference
  ownership, lock lifetimes, immutable shared cache slices, title ordering, bounded
  EPUB traversal, token authorization, and local-first/opt-in-only external sharing.
- Add regressions for behavior changes. Keep reasoning beside the affected code or
  tests; do not create aggregated review logs or move history into `AGENTS.md`.
- Where measurement is warranted, establish and protect the original baseline
  before production edits. Use fresh checkpoint paths and harnesses, identical
  toolchain/machine/settings, repeated samples, allocations, and `benchstat`.
  Measure serially without concurrent tests/builds. Retain failed and unfavorable
  results; report limitations, not gains inferred from noise or a single run.
- Read tool schemas before use. Use dedicated file operations with revision guards
  and background jobs for long checks. After an ambiguous connection failure,
  inspect files, Git, and jobs before retrying any mutation.
- Keep `.skills/`, generated evidence, and `.agents/MCP_Feedback.md` ignored and
  untracked. MCP feedback is operational advice, not authorization to change the
  MCP server. Update an existing feedback entry only when useful, without duplicates.

## Protected evidence — read-only

- Preserve CP6–CP18 and the completed font-normalization side quest. Keep all
  existing contents under `.agents/benchmarks/` unchanged, including original and
  candidate binaries/bundles, every harness version, drivers, manifests, fixtures,
  raw samples, receipts, audits, and failed/intermediate/unused artifacts.
- Keep all benchmark source files designated frozen by the existing verification
  chain/manifests byte-identical. Recorded production, ordinary-test, and handoff
  source hashes are historical provenance, not permanent bans on legitimate edits.
- Do not rebuild protected binaries, rerun completed measurements, run old
  freeze/seal/reseal modes, overwrite artifacts, or regenerate a manifest to make
  verification pass. Use only this entry point to verify existing protections:

```sh
python .agents/benchmarks/checkpoint18/comparison.py verify
```

The existing CP18 manifest is
`.agents/benchmarks/checkpoint18/validated/evidence-manifest.json`;
its retained SHA-256 anchor is
`d20e1401120b51f58704cddacd6ad2c34c8b5e176e7d1ba4151ae670cc4ba683`.
If verification fails, stop and investigate without modifying protected evidence.
Legacy drivers and their historical handoffs remain in evidence/Git, not this task.

## Finish CP21

1. Complete the bounded review and required regressions. Run focused checks and a
   fresh full `make check` on final sources, then the protection check above.
   Finish or safely stop all jobs; do not leave review work running.
2. Mark only fully reviewed and verified files done in `TRACKER.md`; reconcile new
   files, counts, and remaining inventory. Do not declare the overall review done
   while any inventory entry remains pending. Keep completion requirements here,
   not check-run history in the tracker.
3. Replace this task with the next bounded task, leaving it explicitly not started.
   If blocked instead, retain only the active scope, blocker, and next action.
   Do not append a completed-CP21 report; Git records the completed work.
4. Verify the exact staged paths and contents; make one conventional scoped local
   commit containing only reviewed changes. Report its hash, findings, checks,
   limitations, and remaining inventory in chat. No amend or push.
5. Stop after CP21. Wait for explicit continuation; do not start CP22 automatically.
