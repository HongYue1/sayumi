// Run with Bun from any directory. No dependencies, network, or real publishing.
// Keep executable failure cases beside the workflows, and call this from Go's
// ordinary test gate as well as the fast workflow-only command.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { runInNewContext } from "node:vm";

const root = resolve(import.meta.dir, "../..");
const read = path => readFileSync(join(root, path), "utf8");
const load = name => Bun.YAML.parse(read(`.github/workflows/${name}.yml`));
const ci = load("ci"), maintenance = load("go-toolchain-bump"), release = load("release");
const job = maintenance.jobs.bump, publisher = maintenance.jobs.publish;
const step = id => job.steps.find(s => s.id === id);
const publish = publisher.steps.find(s => s.id === "publish");
const prepare = publisher.steps.find(s => s.id === "prepare");
const ref = (id, key) => "${{ steps." + id + ".outputs." + key + " }}";
const changed = "steps.bump.outputs.changed == 'true'";
const branchGuard = "github.ref == format('refs/heads/{0}', github.event.repository.default_branch)";
const checkIndex = name => ci.jobs.check.steps.findIndex(s => s.name === name);

assert.deepEqual(Object.keys(ci.on).sort(), ["merge_group", "pull_request", "push", "workflow_dispatch"]);
assert.deepEqual(ci.on.push, { branches: ["main", "master"] });
assert.equal(ci.on.pull_request, null); // Includes fork and Dependabot PRs, no path filter.
assert.deepEqual(ci.on.merge_group, { types: ["checks_requested"] });
assert.deepEqual(Object.keys(maintenance.on).sort(), ["schedule", "workflow_dispatch"]);
assert.deepEqual(maintenance.on.schedule, [{ cron: "17 6 * * 1" }]);
assert.deepEqual(ci.permissions, { contents: "read" });
assert.deepEqual(maintenance.permissions, {});
assert.deepEqual(job.permissions, { contents: "read" });
assert.deepEqual(publisher.permissions, { contents: "write", "pull-requests": "write" });
assert.equal(job.if, branchGuard);
assert.equal(publisher.needs, "bump");
assert.equal(publisher.if, branchGuard + " && needs.bump.result == 'success' && needs.bump.outputs.changed == 'true'");
assert.deepEqual(maintenance.concurrency, { group: "go-toolchain-bump", "cancel-in-progress": false });
assert.deepEqual(ci.concurrency, {
  group: "ci-${{ github.workflow }}-${{ github.event_name }}-${{ github.event_name == 'pull_request' && github.ref || github.run_id }}",
  "cancel-in-progress": "${{ github.event_name == 'pull_request' }}",
});

// Evaluate only these asserted boolean/string expressions, with lowercase event
// enums and refs. This is not an emulator for GitHub scheduling or token policy;
// actionlint validates expression syntax/types independently.
const expression = (source, context) => runInNewContext(source, {
  ...context, format: (pattern, value) => pattern.replace("{0}", value),
}, { timeout: 1000 });
const expand = (source, context) => source.replace(/\$\{\{(.*?)\}\}/g, (_, body) => expression(body, context));
const context = (event, refName, runID = 1, result = "success", didChange = "true", base = "main") => ({
  github: { workflow: "CI", event_name: event, ref: refName, run_id: runID, event: { repository: { default_branch: base } } },
  needs: { bump: { result, outputs: { changed: didChange } } },
});
const group = c => expand(ci.concurrency.group, c);
const pr = context("pull_request", "refs/pull/42/merge");
assert.equal(group(pr), group(context("pull_request", "refs/pull/42/merge", 2)));
assert.notEqual(group(pr), group(context("pull_request", "refs/pull/43/merge")));
for (const event of ["push", "merge_group", "workflow_dispatch"]) {
  const c = context(event, "refs/heads/main");
  assert.notEqual(group(c), group(context(event, "refs/heads/main", 2)));
  assert.equal(expand(ci.concurrency["cancel-in-progress"], c), "false");
}
assert.equal(expand(ci.concurrency["cancel-in-progress"], pr), "true");
assert.notEqual(group(context("push", "refs/heads/main")), group(context("workflow_dispatch", "refs/heads/main")));
for (const base of ["main", "master", "trunk"]) {
  for (const event of ["schedule", "workflow_dispatch"]) {
    const c = context(event, `refs/heads/${base}`, 1, "success", "true", base);
    assert.equal(expression(job.if, c), true);
    assert.equal(expression(publisher.if, c), true);
  }
  assert.equal(expression(job.if, context("workflow_dispatch", "refs/heads/feature", 1, "success", "true", base)), false);
}
for (const result of ["failure", "cancelled", "skipped"]) {
  assert.equal(expression(publisher.if, context("schedule", "refs/heads/main", 1, result)), false);
}
for (const value of ["false", "", "TRUE\nextra=true"]) {
  assert.equal(expression(publisher.if, context("schedule", "refs/heads/main", 1, "success", value)), false);
}

for (const workflow of [ci, maintenance]) {
  for (const consumer of Object.values(workflow.jobs)) {
    assert.ok(consumer["timeout-minutes"] > 0 && consumer["timeout-minutes"] <= 30);
    assert.equal(consumer["continue-on-error"], undefined);
    for (const s of consumer.steps) {
      if (s.uses) assert.match(s.uses, /^(actions\/checkout|actions\/setup-go|oven-sh\/setup-bun)@[a-f0-9]{40}$/);
      if (s.uses?.startsWith("actions/checkout@")) assert.equal(s.with["persist-credentials"], false);
      assert.equal(s["continue-on-error"], undefined);
      assert.ok(!s.run?.includes("${{"), "pass workflow data through env, never interpolate shell source");
    }
  }
}
for (const consumer of Object.values(ci.jobs)) {
  assert.equal(consumer.if, undefined);
  assert.equal(consumer.permissions, undefined);
  const setup = consumer.steps.find(s => s.uses?.startsWith("actions/setup-go@"));
  assert.equal(setup.with.cache, true);
  assert.deepEqual(setup.with["cache-dependency-path"].trim().split("\n"), ["go.sum", "provision.sh"]);
}
assert.equal(ci.env.GOTOOLCHAIN, "local");
assert.equal(ci.jobs.check.env.CGO_ENABLED, "1");
assert.equal(ci.jobs["platform-test"].env.CGO_ENABLED, "0");
assert.deepEqual(ci.jobs["platform-test"].strategy, { "fail-fast": false, matrix: { os: ["windows-latest", "macos-latest"] } });
assert.equal(job.env.GOTOOLCHAIN, "local");
assert.equal(job.env.CGO_ENABLED, "1");
assert.equal(job.steps[0].with.ref, "${{ github.sha }}");
assert.equal(publisher.steps[0].with.ref, "${{ github.sha }}");
assert.equal(job.steps.find(s => s.uses?.startsWith("actions/setup-go@")).with.cache, false);
assert.equal(job.steps.find(s => s.uses?.startsWith("actions/setup-go@")).with["check-latest"], true);
assert.equal(job.steps.find(s => s.uses?.startsWith("actions/setup-go@")).with["go-version"], ref("go-minor", "version"));
assert.deepEqual(job.outputs, { changed: ref("bump", "changed"), version: ref("validated", "version"), "mod-blob": ref("validated", "mod-blob") });
assert.equal(step("bump").env.OLDGO, ref("go-minor", "current"));
for (const id of ["quality", "validated"]) assert.equal(step(id).if, changed);
assert.ok(job.steps.indexOf(step("quality")) < job.steps.indexOf(step("validated")));
assert.equal(step("validated").run, "bash .github/scripts/go-toolchain-patch.sh verify");
assert.deepEqual(step("validated").env, { NEWGO: ref("bump", "version") });
assert.equal(publisher.steps.length, 3); // Checkout, data-only reconstruction, publication. No cache/artifact/build action.
assert.equal(prepare.run, "bash .github/scripts/go-toolchain-patch.sh prepare");
assert.deepEqual(prepare.env, { NEWGO: "${{ needs.bump.outputs.version }}", MOD_BLOB: "${{ needs.bump.outputs.mod-blob }}" });
assert.equal(publisher.steps[1], prepare);
assert.equal(publisher.steps[2], publish);
assert.deepEqual(publish.env, { GH_TOKEN: "${{ github.token }}", NEWGO: "${{ needs.bump.outputs.version }}", BASE_BRANCH: "${{ github.event.repository.default_branch }}", BASE_SHA: "${{ github.sha }}" });
assert.ok(!JSON.stringify(job).includes("github.token"));
assert.ok(checkIndex("Install workflow validator (pinned)") < checkIndex("Validate CI and maintenance workflows"));
assert.equal(ci.jobs.check.steps[checkIndex("Validate CI and maintenance workflows")].run, "make workflow-check");

// Preserve the canonical runtime and locked/no-hook provisioning contracts,
// including the unchanged release consumer. Never silently select latest Bun.
const bunPin = JSON.parse(read("frontend/package.json")).packageManager;
assert.match(bunPin, /^bun@(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/);
for (const consumer of [ci.jobs.check, ci.jobs["platform-test"], job, release.jobs.build]) {
  const setups = consumer.steps.filter(s => s.uses?.startsWith("oven-sh/setup-bun@"));
  assert.equal(setups.length, 1);
  assert.deepEqual(setups[0].with, { "bun-version-file": "frontend/package.json", ...(consumer === job ? { "no-cache": true } : {}) });
  const installs = consumer.steps.filter(s => s.name === "Install frontend deps");
  assert.equal(installs.length, 1);
  assert.equal(installs[0].run, "bash ./provision.sh frontend");
  assert.equal(installs[0].shell, "bash");
  assert.equal(installs[0]["working-directory"], undefined);
  assert.ok(!consumer.steps.some(s => /^\s*go install\s/m.test(s.run || "")));
}
for (const consumer of [ci.jobs.check, ci.jobs["platform-test"], release.jobs.build]) {
  const setup = consumer.steps.find(s => s.uses?.startsWith("actions/setup-go@"));
  assert.equal(setup.with["go-version-file"], "go.mod");
  assert.equal(setup.with["go-version"], undefined);
}
for (const consumer of [ci.jobs.check, job, release.jobs.build]) {
  const tools = consumer.steps.filter(s => s.name === "Install Go quality tools (pinned)");
  assert.equal(tools.length, 1);
  assert.equal(tools[0].run, "bash ./provision.sh quality-tools");
  assert.equal(tools[0].shell, "bash");
}
for (const name of ["Install frontend deps", "Install Go quality tools (pinned)"]) assert.equal(job.steps.find(s => s.name === name).if, changed);
const resources = release.jobs.build.steps.find(s => s.name === "Install go-winres");
assert.equal(resources.run, "bash ./provision.sh release-tools");
assert.equal(resources.shell, "bash");
const dependabot = Bun.YAML.parse(read(".github/dependabot.yml"));
assert.equal(dependabot.version, 2);
assert.deepEqual(dependabot.updates.map(u => [u["package-ecosystem"], u.directory]), [["github-actions", "/"], ["gomod", "/"], ["bun", "/frontend"]]);
for (const update of dependabot.updates) {
  assert.equal(update.schedule.interval, "weekly");
  assert.equal(update["insecure-external-code-execution"], undefined);
  assert.equal(update["target-branch"], undefined);
}

let cases = 0;
// Keep user Git configuration, tokens and shell startup hooks out of fixtures.
const envBase = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(GIT_|GH_|GITHUB_|BASH_ENV$|ENV$)/i.test(key)));
const bash = process.env.BASH || "bash";
function execute(script, cwd, env, success = true, args = []) {
  const r = spawnSync(bash, ["--noprofile", "--norc", "-euo", "pipefail", "-c", script, "workflow-fixture", ...args], {
    cwd, env: { ...envBase, BASH_ENV: "", ...env }, encoding: "utf8", timeout: 10000,
  });
  assert.ifError(r.error);
  assert.equal(r.status === 0, success, `exit=${r.status}\n${r.stdout}${r.stderr}`);
  cases++;
  return r.stdout + r.stderr;
}
function run(script, env = {}, success = true) {
  const dir = mkdtempSync(join(tmpdir(), "sayumi-workflow-"));
  const output = join(dir, "output").replaceAll("\\", "/");
  try {
    return execute(script, dir, { GITHUB_OUTPUT: output, ...env }, success) + (existsSync(output) ? readFileSync(output, "utf8") : "");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
const mockModule = `awk() { command awk "$1" <(printf '%s\\n' "$GO_MOD"); }\n`;
for (const [text, version] of [
  ["module test\n\ngo 1.27.0\n", "1.27.0"], ["go\t1.26.12\r\n", "1.26.12"],
  ["go 1.27.1 // pinned patch\n", "1.27.1"], ["module test\n", null],
  ["go 1.27\n", null], ["go 1.27rc1\n", null], ["go 1.27.00\n", null],
  ["go 1.27.0\ngo 1.26.1\n", null],
]) {
  const out = run(mockModule + step("go-minor").run, { GO_MOD: text }, version !== null);
  if (version) assert.ok(out.includes(`current=${version}\nversion=${version.slice(0, version.lastIndexOf("."))}.x\n`));
}
const mockBump = String.raw`
go() {
  case "$*" in
    'get go@patch') return "$GET_STATUS" ;;
    'mod tidy') return "$TIDY_STATUS" ;;
    'list -m -f {{.GoVersion}}') printf '%s\n' "$CANDIDATE"; return "$LIST_STATUS" ;;
    *) return 90 ;;
  esac
}
git() { [[ "$*" == 'diff --quiet -- go.mod go.sum' ]] || return 90; return "$DIRTY"; }
`;
const defaults = { OLDGO: "1.27.0", CANDIDATE: "1.27.1", DIRTY: "1", GET_STATUS: "0", TIDY_STATUS: "0", LIST_STATUS: "0" };
for (const [env, success, didChange] of [
  [{ CANDIDATE: "1.27.0", DIRTY: "0" }, true, false], [{}, true, true],
  [{ OLDGO: "1.26.9", CANDIDATE: "1.26.10" }, true, true],
  [{ OLDGO: "1.27.10", CANDIDATE: "1.27.9" }, false],
  [{ CANDIDATE: "1.28.0" }, false], [{ CANDIDATE: "1.27rc1" }, false],
  [{ CANDIDATE: "" }, false], [{ CANDIDATE: "1.27.0" }, false],
  [{ GET_STATUS: "1" }, false], [{ TIDY_STATUS: "1" }, false],
  [{ LIST_STATUS: "1" }, false], [{ DIRTY: "128" }, false],
]) {
  const out = run(mockBump + step("bump").run, { ...defaults, ...env }, success);
  if (success) assert.ok(out.includes(`changed=${didChange}\n`));
  else assert.ok(!out.includes("changed=true"));
  if (didChange) assert.ok(out.includes(`version=${env.CANDIDATE || defaults.CANDIDATE}\n`));
}
const mockQuality = String.raw`
command() { [[ "$1" == -v && "$2" != "$MISSING" ]]; }
go() { [[ "$*" == 'env CGO_ENABLED' ]] || return 90; printf '%s\n' "$CGO"; }
make() { echo MAKE_CHECK; return "$MAKE_STATUS"; }
`;
for (const script of [step("quality").run, ci.jobs.check.steps.find(s => s.name === "Run checks").run]) {
  for (const env of [{}, { MISSING: "gcc" }, { MISSING: "govulncheck" }, { CGO: "0" }, { CGO: "garbage" }, { MAKE_STATUS: "1" }]) {
    const success = Object.keys(env).length === 0;
    const out = run(mockQuality + script, { MISSING: "", CGO: "1", MAKE_STATUS: "0", ...env }, success);
    if (env.MISSING || env.CGO) assert.ok(!out.includes("MAKE_CHECK"));
  }
}

// Real Git fixtures prove the exact validated go.mod is reconstructed, without
// allowing dependency changes, dirty sources, injected outputs or artifact code.
const helper = read(".github/scripts/go-toolchain-patch.sh");
function fixture(body) {
  const dir = mkdtempSync(join(tmpdir(), "sayumi-go-patch-")), repo = join(dir, "repo"), hooks = join(dir, "hooks");
  mkdirSync(repo); mkdirSync(hooks);
  const env = { ...envBase, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(dir, "no-global-config"), GITHUB_OUTPUT: join(dir, "output").replaceAll("\\", "/") };
  const git = (...args) => {
    const r = spawnSync("git", ["-c", "core.autocrlf=false", "-c", `core.hooksPath=${hooks}`, "-c", "user.name=Workflow fixture", "-c", "user.email=workflow@example.invalid", "-c", "commit.gpgsign=false", ...args], { cwd: repo, env, encoding: "utf8", timeout: 10000 });
    assert.ifError(r.error); assert.equal(r.status, 0, r.stdout + r.stderr);
    return r.stdout.trim();
  };
  const write = (path, content) => writeFileSync(join(repo, path), content);
  const original = "module fixture\n\ngo 1.27.0\n\nrequire example.invalid/dep v1.2.3\n";
  try {
    git("init", "-q");
    write("go.mod", original); write("go.sum", "fixture checksum\n"); write("README.md", "fixture\n");
    git("add", "--", "go.mod", "go.sum", "README.md"); git("commit", "-qm", "fixture");
    const call = (mode, extra = {}, success = true, prefix = "") => execute(prefix + helper, repo, { ...env, NEWGO: "1.27.1", ...extra }, success, [mode]);
    body({ repo, git, write, original, call, output: env.GITHUB_OUTPUT });
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
fixture(({ repo, write, original, call, output, git }) => {
  const candidate = original.replace("go 1.27.0", "go 1.27.1");
  write("go.mod", candidate); call("verify");
  const out = readFileSync(output, "utf8"), blob = out.match(/^mod-blob=([a-f0-9]{40})$/m)?.[1];
  assert.ok(out.includes("version=1.27.1\n")); assert.ok(blob);
  write("go.mod", original); call("prepare", { MOD_BLOB: blob });
  assert.equal(readFileSync(join(repo, "go.mod"), "utf8"), candidate);
  assert.equal(git("diff", "--name-only"), "go.mod");
  write("go.mod", original);
  for (const bad of ["", "../module", "a".repeat(40), blob + "\nextra=true"]) call("prepare", { MOD_BLOB: bad }, false);
  assert.equal(readFileSync(join(repo, "go.mod"), "utf8"), original);
  write("README.md", "unreviewed\n"); call("prepare", { MOD_BLOB: blob }, false);
  write("README.md", "fixture\n");
  call("prepare", { MOD_BLOB: blob }, false, 'git() { [[ "$1" != status ]] || return 91; command git "$@"; }\n');
  assert.equal(readFileSync(join(repo, "go.mod"), "utf8"), original);
});
for (const value of ["", "1.27.0", "1.26.9", "1.28.0", "1.27rc1", "1.27.01", "1.27.999999999999999999999", "1.27.1\nchanged=true", "$(touch injected)"]) {
  fixture(({ call, repo }) => {
    const out = call("verify", { NEWGO: value }, false);
    // A later dirty-tree/blob failure is not evidence of version validation.
    assert.match(out, /missing validated Go version|invalid Go patch version|Go minor changes require manual review|Go patch must strictly increase/);
    assert.equal(existsSync(join(repo, "injected")), false);
  });
}
for (const change of ["dependency", "toolchain", "sum", "tracked", "untracked", "mismatched-version", "git-error"]) {
  fixture(({ write, original, call, output }) => {
    let candidate = original.replace("go 1.27.0", "go 1.27.1");
    if (change === "dependency") candidate = candidate.replace("v1.2.3", "v9.0.0");
    if (change === "toolchain") candidate += "\ntoolchain go1.27.1\n";
    write("go.mod", candidate);
    if (change === "sum") write("go.sum", "different graph\n");
    if (change === "tracked") write("README.md", "unreviewed\n");
    if (change === "untracked") write("injected.go", "package fixture\n");
    call("verify", change === "mismatched-version" ? { NEWGO: "1.27.2" } : {}, false, change === "git-error" ? 'git() { [[ "$1" != diff ]] || return 128; command git "$@"; }\n' : "");
    assert.equal(existsSync(output), false, "failed handoff must emit no publishable outputs");
  });
}

const mockPublish = String.raw`
git() {
  if [[ "$1" == ls-remote ]]; then
    if [[ "$2" == --exit-code ]]; then
      printf '%s\trefs/heads/%s\n' "$BASE_OID" "$BASE_BRANCH"; return "$BASE_STATUS"
    fi
    [[ -z "$REMOTE_OID" ]] || printf '%s\trefs/heads/deps/go-toolchain-patch\n' "$REMOTE_OID"
    return "$REMOTE_STATUS"
  fi
  printf 'git'; printf ' <%s>' "$@"; printf '\n'
  [[ "$1" != "$GIT_FAIL" ]] || return 1
  if [[ "$1" == push ]]; then return "$PUSH_STATUS"; fi
  return 0
}
gh() {
  if [[ "$1 $2" == 'pr list' ]]; then printf '%s' "$PR_NUMBER"; return "$PR_STATUS"; fi
  printf 'gh'; printf ' <%s>' "$@"; printf '\n'
  case "$1 $2" in
    'auth setup-git') return "$AUTH_STATUS" ;;
    'pr create')
      if [[ "$CREATE_STATUS" != 0 ]]; then echo 'GraphQL: GitHub Actions is not permitted to create or approve pull requests' >&2; fi
      return "$CREATE_STATUS" ;;
    'pr edit') return "$EDIT_STATUS" ;;
    *) return 90 ;;
  esac
}
`;
const publishing = { NEWGO: "1.27.1", BASE_BRANCH: "trunk", BASE_SHA: "b".repeat(40), BASE_OID: "b".repeat(40), BASE_STATUS: "0", REMOTE_OID: "", REMOTE_STATUS: "0", PR_NUMBER: "", PR_STATUS: "0", PUSH_STATUS: "0", CREATE_STATUS: "0", EDIT_STATUS: "0", AUTH_STATUS: "0", GIT_FAIL: "" };
for (const number of ["", "42"]) {
  const oid = number ? "a".repeat(40) : "";
  const out = run(mockPublish + publish.run, { ...publishing, PR_NUMBER: number, REMOTE_OID: oid });
  assert.ok(out.includes("<build(ci): bump Go to 1.27.1>"));
  assert.ok(out.includes(`<--force-with-lease=refs/heads/deps/go-toolchain-patch:${oid}>`));
  assert.ok(out.includes("git <add> <--> <go.mod>"));
  assert.ok(!out.includes("<go.sum>"));
  assert.ok(out.includes(number ? "gh <pr> <edit> <42>" : "gh <pr> <create> <--base> <trunk>"));
}
for (const env of [{ NEWGO: "" }, { AUTH_STATUS: "1" }, { BASE_STATUS: "2" }, { BASE_OID: "c".repeat(40) }, { REMOTE_STATUS: "1" }, { REMOTE_OID: "bad" }, { GIT_FAIL: "commit" }, { PUSH_STATUS: "1" }, { PR_STATUS: "1" }]) {
  const out = run(mockPublish + publish.run, { ...publishing, ...env }, false);
  assert.ok(!out.includes("gh <pr> <create>") && !out.includes("gh <pr> <edit>"));
  if (env.BASE_OID || env.BASE_STATUS || env.REMOTE_OID || env.REMOTE_STATUS || env.AUTH_STATUS) assert.ok(!out.includes("git <push>"));
}
const denied = run(mockPublish + publish.run, { ...publishing, CREATE_STATUS: "1" }, false);
assert.ok(denied.includes("GraphQL: GitHub Actions is not permitted"));
assert.ok(denied.includes("::error::Bump branch pushed, but PR creation failed."));
assert.ok(denied.includes("Allow GitHub Actions to create and approve pull requests"));
assert.ok(!denied.includes("gh <pr> <edit>"));
const failedEdit = run(mockPublish + publish.run, { ...publishing, PR_NUMBER: "42", EDIT_STATUS: "1" }, false);
assert.ok(!failedEdit.includes("gh <pr> <create>"));
console.log(`Workflow regressions passed (${cases} executable shell cases, plus trigger/permission/cache/order contracts).`);
