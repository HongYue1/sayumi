// Offline release contracts. Compiler/frontend/resources are fakes; the archive
// helper, archive bytes, checksums and isolated publisher verifier are real.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runInNewContext } from "node:vm";

const workflow = Bun.YAML.parse(readFileSync(".github/workflows/release.yml", "utf8"));
const { build, publish } = workflow.jobs;
assert.deepEqual(workflow.on.push.tags, ["v*"]);
assert.ok("workflow_dispatch" in workflow.on);
assert.deepEqual(workflow.permissions, { contents: "read" });
assert.deepEqual(build.permissions, { contents: "read" });
assert.equal(workflow.env.GOTOOLCHAIN, "local");
assert.equal(build.env.CGO_ENABLED, "1");
assert.equal(publish.needs, "build");
for (const [event_name, ref, allowed] of [
  ["push", "refs/tags/v1.2.3", true],
  ["push", "refs/heads/main", false],
  ["push", "refs/tags/other", false],
  ["workflow_dispatch", "refs/heads/main", false],
  ["workflow_dispatch", "refs/tags/v1.2.3", false],
  ["pull_request", "refs/tags/v1.2.3", false],
]) {
  assert.equal(runInNewContext(publish.if, { github: { event_name, ref }, startsWith: (s, prefix) => s.startsWith(prefix) }, { timeout: 1000 }), allowed, `${event_name} ${ref}`);
}
assert.deepEqual(publish.permissions, { contents: "write", "id-token": "write", attestations: "write" });
for (const job of [build, publish]) {
  for (const step of job.steps) {
    if (step.uses) assert.match(step.uses, /@[a-f0-9]{40}$/);
    if (step.uses?.startsWith("actions/checkout@")) assert.equal(step.with["persist-credentials"], false);
  }
}
// Keep the credentialed job closed to checkout, setup, install and execution of
// downloaded payloads. Its sole shell step is the literal inline verifier.
assert.equal(publish.steps.length, 4);
assert.equal(publish.steps.filter(s => s.run).length, 1);
assert.equal(publish.steps.find(s => s.run).id, "verify");
assert.ok(publish.steps[0].uses.startsWith("actions/download-artifact@"));
assert.equal(publish.steps[1].id, "verify");
assert.ok(publish.steps[2].uses.startsWith("actions/attest-build-provenance@"));
assert.ok(publish.steps[3].uses.startsWith("softprops/action-gh-release@"));
const index = id => build.steps.findIndex(s => s.id === id);
assert.ok(index("regressions") >= 0 && index("regressions") < index("quality") && index("quality") < index("artifacts") && index("artifacts") < index("upload"));
assert.equal(build.steps[index("regressions")].run, "bun .github/scripts/release-tests.mjs");
assert.match(build.steps[index("quality")].run, /make check/);
assert.match(build.steps[index("quality")].run, /gofumpt goimports golangci-lint govulncheck gcc/);
assert.equal(build.steps[index("artifacts")].env.RELEASE_VERSION, "${{ github.ref_type == 'tag' && github.ref_name || '' }}");
const archives = ["darwin-amd64.tar.gz", "darwin-arm64.tar.gz", "linux-amd64.tar.gz", "linux-arm64.tar.gz", "windows-amd64.zip", "windows-arm64.zip"].map(s => `sayumi-${s}`);
const paths = text => text.trim().split(/\s+/).map(s => s.replace(/^dist-release\//, "")).sort();
const upload = build.steps[index("upload")];
assert.deepEqual(paths(upload.with.path), [...archives, "SHA256SUMS"].sort());
assert.equal(upload.with["if-no-files-found"], "error");
assert.equal(upload.with["compression-level"], 0);
assert.notEqual(upload.with.archive, false); // Six archives + manifest require the default bundle.
const download = publish.steps[0];
assert.equal(download.with.name, upload.with.name);
assert.equal(download.with.path, "dist-release");
assert.ok(!download.with["run-id"] && !download.with.repository && !download.with["github-token"]);
assert.deepEqual(paths(publish.steps[2].with["subject-path"]), archives);
assert.deepEqual(paths(publish.steps[3].with.files), [...archives, "SHA256SUMS"].sort());
assert.equal(publish.steps[3].with.fail_on_unmatched_files, true);

const manifest = JSON.parse(readFileSync("cmd/sayumi/winres/winres.json", "utf8"));
const settings = manifest.RT_MANIFEST["#1"]["0409"];
assert.equal(settings["execution-level"], "as invoker");
assert.equal(settings["ui-access"], false);
assert.equal(settings["auto-elevate"], false);
assert.equal(settings["minimum-os"], "win10");
assert.equal(settings["long-path-aware"], true);
assert.equal(settings["dpi-awareness"], "per monitor v2");
for (const name of manifest.RT_GROUP_ICON.APP["0000"]) {
  const bytes = readFileSync(join("cmd/sayumi/winres", name));
  assert.equal(bytes.subarray(0, 8).toString("hex"), "89504e470d0a1a0a");
  const size = Number(name.match(/^icon-(\d+)\.png$/)[1]);
  assert.equal(bytes.readUInt32BE(16), size);
  assert.equal(bytes.readUInt32BE(20), size);
}
const attributes = spawnSync("git", ["check-attr", "-z", "text", "eol", "--", "release.sh", "cmd/sayumi/winres/winres.json", "frontend/bun.lock", "cmd/sayumi/rsrc_windows_amd64.syso", "cmd/sayumi/winres/icon-256.png"], { encoding: "utf8" });
assert.ifError(attributes.error);
assert.equal(attributes.status, 0, attributes.stderr);
const fields = attributes.stdout.split("\0");
for (let i = 0; i + 2 < fields.length; i += 3) {
  assert.equal(fields[i + 2], fields[i + 1] === "eol" ? "lf" : /\.(syso|png)$/.test(fields[i]) ? "unset" : "set");
}

const source = readFileSync("release.sh", "utf8");
const bash = process.env.BASH || "bash";
const prelude = String.raw`
set -euo pipefail
cd "$1"; shift
command() {
  if [[ "$1" == -v && "$2" == "$MISSING_TOOL" ]]; then return 1; fi
  builtin command "$@"
}
git() { if [[ "$1" == describe ]]; then echo v1.2.3; else echo 1700000000; fi; }
bun() {
  if [[ "$1" == run ]]; then
    echo frontend >> "$LOG_FILE"
    [[ "$FAIL_FRONTEND" == 0 ]] || return 61
    if [[ "$DIRECTORY_FRONTEND" == 1 ]]; then mkdir "$FIXTURE/cmd/sayumi/dist/index.html"
    elif [[ "$EMPTY_FRONTEND" == 0 ]]; then printf '<html>fixture</html>' > "$FIXTURE/cmd/sayumi/dist/index.html"; fi
  else builtin command bun "$@"; fi
}
env() ( while [[ "$1" == *=* ]]; do export "$1"; shift; done; "$@"; )
go-winres() {
  echo resources >> "$LOG_FILE"
  [[ "$FAIL_RESOURCES" == 0 ]] || return 62
  [[ "$*" == *"--file-version=$EXPECTED_VERSION"* && "$*" == *"--product-version=$EXPECTED_VERSION"* && "$*" == *"--arch amd64,arm64"* ]] || return 94
  local out=""
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == --out ]]; then out="$2"; shift 2; else shift; fi
  done
  [[ -n "$out" ]] || return 90
  printf 'amd64 resource' > "$out"_windows_amd64.syso
  printf 'arm64 resource' > "$out"_windows_arm64.syso
}
go() {
  if [[ "$1" == env ]]; then
    case "$2" in GOHOSTOS) echo linux ;; GOHOSTARCH) echo amd64 ;; *) return 90 ;; esac
    return
  fi
  [[ "$1" == build && "$GOTOOLCHAIN" == local && "$GOWORK" == off && -z "$GOFLAGS" && -z "$GOEXPERIMENT" ]] || return 95
  [[ "$*" == *"-mod=readonly"* && "$*" == *"-trimpath"* && "$*" == *"-buildvcs=false"* ]] || return 96
  local out="" package="" flags="$*"
  while [[ $# -gt 0 ]]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) package="$1"; shift ;; esac
  done
  if [[ "$package" == ./tools/release-archive ]]; then
    echo packer-build >> "$LOG_FILE"
    [[ "$FAIL_PACKER" == 0 ]] || return 67
    [[ "$GOOS/$GOARCH/$CGO_ENABLED/$GOAMD64" == linux/amd64/0/v1 ]] || return 97
    cat > "$out" <<'PACKER'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == checksums ]]; then
  [[ "$FAIL_CHECKSUM" == 0 ]] || exit 66
else
  [[ "$FAIL_ARCHIVE" == 0 ]] || exit 64
fi
exec "$REAL_PACKER" "$@"
PACKER
    chmod +x "$out"
    return
  fi
  echo "$GOOS/$GOARCH cgo=$CGO_ENABLED amd64=$GOAMD64 arm64=$GOARM64" >> "$LOG_FILE"
  [[ "$FAIL_BUILD" != "$GOOS/$GOARCH" ]] || return 63
  [[ "$flags" == *"main.version=$EXPECTED_VERSION"* && "$flags" == *"main.buildDate="* ]] || return 98
  if [[ "$GOOS" == windows ]]; then
    [[ "$package" == ./dist-release/_stage/windows-src ]] || return 91
    [[ -s "$package/rsrc_windows_$GOARCH.syso" && -s "$package/dist/index.html" && -s "$package/main.go" ]] || return 92
    [[ ! -e "$package/main_test.go" ]] || return 93
  fi
  if [[ "$EMPTY_BINARY" == 1 ]]; then : > "$out"
  else printf 'binary %s/%s\n' "$GOOS" "$GOARCH" > "$out"; fi
}
rm() {
  [[ "$FAIL_CLEANUP" == 0 ]] || return 65
  if [[ "$FAIL_FINAL_CLEANUP" == 1 && "$*" == '-rf dist-release/_stage' && -f dist-release/SHA256SUMS ]]; then FAIL_FINAL_CLEANUP=0; return 65; fi
  builtin command rm "$@"
}
find() { [[ "$FAIL_FONT_SCAN" == 0 ]] || return 68; builtin command find "$@"; }
export -f command git bun env go-winres go rm find
bash --noprofile --norc ./release.sh "$@"
`;
let cases = 0;
const scratch = mkdtempSync(join(tmpdir(), "sayumi release helper "));
try {
  const packer = join(scratch, process.platform === "win32" ? "archive.exe" : "archive");
  const compiled = spawnSync("go", ["build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", packer, "./tools/release-archive"], {
    encoding: "utf8", timeout: 90000,
    env: { ...process.env, GOTOOLCHAIN: "local", GOWORK: "off", GOFLAGS: "", GOEXPERIMENT: "", GOOS: "", GOARCH: "", GOAMD64: "v1", GOARM64: "v8.0", CGO_ENABLED: "0", GOPROXY: "off", GOSUMDB: "off" },
  });
  assert.ifError(compiled.error);
  assert.equal(compiled.status, 0, compiled.stdout + compiled.stderr);
  const run = (args, env = {}, expected = 0, inspect = () => {}, prepare = () => {}) => {
    const dir = mkdtempSync(join(tmpdir(), "sayumi release "));
    try {
      for (const name of ["frontend", "cmd/sayumi/dist", "fonts-bundle/Fixture", "dist-release"]) mkdirSync(join(dir, name), { recursive: true });
      writeFileSync(join(dir, "release.sh"), source);
      writeFileSync(join(dir, "cmd/sayumi/main.go"), "package main\nfunc main() {}\n");
      writeFileSync(join(dir, "cmd/sayumi/main_test.go"), "test fixture: must not be staged");
      writeFileSync(join(dir, "cmd/sayumi/rsrc_windows_amd64.syso"), "original resource");
      writeFileSync(join(dir, "cmd/sayumi/dist/index.html"), "STALE frontend: must be replaced");
      writeFileSync(join(dir, "fonts-bundle/Fixture/Regular.woff2"), "fixture font");
      writeFileSync(join(dir, "dist-release/sayumi-obsolete.zip"), "old archive");
      writeFileSync(join(dir, "dist-release/SHA256SUMS"), "old manifest");
      prepare(dir);
      const r = spawnSync(bash, ["--noprofile", "--norc", "-c", prelude, "test", dir.replaceAll("\\", "/"), ...args], {
        env: { ...process.env, BASH_ENV: "", MISSING_TOOL: "", FAIL_FRONTEND: "0", EMPTY_FRONTEND: "0", DIRECTORY_FRONTEND: "0", EMPTY_BINARY: "0", FAIL_RESOURCES: "0", FAIL_BUILD: "", FAIL_PACKER: "0", FAIL_FONT_SCAN: "0", FAIL_ARCHIVE: "0", FAIL_CLEANUP: "0", FAIL_FINAL_CLEANUP: "0", FAIL_CHECKSUM: "0", FIXTURE: dir.replaceAll("\\", "/"), REAL_PACKER: packer.replaceAll("\\", "/"), LOG_FILE: join(dir, "events").replaceAll("\\", "/"), SOURCE_DATE_EPOCH: "1700000000", RELEASE_VERSION: "", EXPECTED_VERSION: "v1.2.3", GOTOOLCHAIN: "auto", GOWORK: "invalid-workspace", GOFLAGS: "-invalid-flag", GOEXPERIMENT: "invalid", GOAMD64: "v4", GOARM64: "v9.5", TAR_OPTIONS: "--invalid-option", ZIPOPT: "-j", GZIP: "--invalid-option", ...env },
        encoding: "utf8", timeout: 20000,
      });
      assert.ifError(r.error);
      assert.equal(r.status, expected, r.stdout + r.stderr);
      assert.equal(readFileSync(join(dir, "cmd/sayumi/rsrc_windows_amd64.syso"), "utf8"), "original resource");
      assert.ok(!existsSync(join(dir, "cmd/sayumi/rsrc_windows_arm64.syso")));
      if (expected !== 0 && env.FAIL_CLEANUP !== "1") assert.ok(!existsSync(join(dir, "dist-release/_stage")), "failed release left staging");
      inspect(dir, r);
      cases++;
    } finally { rmSync(dir, { recursive: true, force: true }); }
  };
  const noWork = dir => {
    assert.ok(!existsSync(join(dir, "events")));
    assert.equal(readFileSync(join(dir, "dist-release/SHA256SUMS"), "utf8"), "old manifest");
  };
  const noManifest = dir => assert.ok(!existsSync(join(dir, "dist-release/SHA256SUMS")));
  for (const args of [["linux/../../../outside"], ["windows/386"], ["linux"], [""], ["--help"], ["linux/amd64", "linux/amd64"], ["linux/amd64", "bad"]]) run(args, {}, 2, noWork);
  for (const epoch of ["-1", "1.5", "invalid", "01", "253402300800", "9999999999999999999999999999"]) run(["linux/amd64"], { SOURCE_DATE_EPOCH: epoch }, 1, noWork);
  for (const epoch of ["0", "315532799", "4294967296"]) run([], { SOURCE_DATE_EPOCH: epoch }, 1, noWork);
  for (const tool of ["bun", "go", "go-winres"]) run([], { MISSING_TOOL: tool }, 1, noWork);
  run(["linux/amd64"], { RELEASE_VERSION: "bad version" }, 1, noWork);
  run(["linux/amd64"], {}, 1, noWork, dir => rmSync(join(dir, "fonts-bundle"), { recursive: true }));
  run(["linux/amd64"], {}, 1, noWork, dir => symlinkSync(join(dir, "cmd/sayumi/main.go"), join(dir, "fonts-bundle/escape")));
  run(["linux/amd64"], { FAIL_FONT_SCAN: "1" }, 68, noWork);
  run(["linux/amd64"], { FAIL_CLEANUP: "1" }, 65, noWork);
  run(["linux/amd64"], { FAIL_PACKER: "1" }, 67, noManifest);
  run(["linux/amd64"], { FAIL_FRONTEND: "1" }, 61, noManifest);
  run(["linux/amd64"], { EMPTY_FRONTEND: "1" }, 1, noManifest);
  run(["linux/amd64"], { DIRECTORY_FRONTEND: "1" }, 1, noManifest);
  run(["linux/amd64"], { EMPTY_BINARY: "1" }, 1, noManifest);
  run(["windows/amd64"], { FAIL_RESOURCES: "1" }, 62, noManifest);
  run(["linux/amd64", "linux/arm64"], { FAIL_BUILD: "linux/arm64" }, 63, noManifest);
  run(["windows/arm64"], { FAIL_ARCHIVE: "1" }, 64, noManifest);
  run(["linux/amd64"], { FAIL_CHECKSUM: "1" }, 66, noManifest);
  run(["linux/amd64"], { FAIL_FINAL_CLEANUP: "1" }, 65, noManifest);
  run(["linux/amd64"], { MISSING_TOOL: "go-winres", SOURCE_DATE_EPOCH: "0" }, 0, dir => {
    const events = readFileSync(join(dir, "events"), "utf8");
    assert.ok(!events.includes("resources"));
    assert.match(events, /linux\/amd64 cgo=0 amd64=v1/);
  });
  run(["windows/arm64"], { RELEASE_VERSION: "v2.3.4-rc.1", EXPECTED_VERSION: "v2.3.4-rc.1" });
  run([], {}, 0, dir => {
    assert.deepEqual(readdirSync(join(dir, "dist-release")).sort(), [...archives, "SHA256SUMS"].sort());
    const events = readFileSync(join(dir, "events"), "utf8");
    assert.equal(events.split("\n").filter(s => s.includes(" cgo=")).length, 6);
    assert.equal(events.split("\n").filter(s => s === "resources").length, 1);
    for (const os of ["linux", "darwin", "windows"]) {
      assert.ok(events.includes(`${os}/amd64 cgo=0 amd64=v1`));
      assert.match(events, new RegExp(`${os}/arm64 cgo=0 .*arm64=v8\\.0`));
    }
    const verify = () => {
      const result = spawnSync(bash, ["--noprofile", "--norc", "-euo", "pipefail", "-c", publish.steps[1].run], { cwd: dir, env: { ...process.env, BASH_ENV: "" }, encoding: "utf8", timeout: 10000 });
      assert.ifError(result.error);
      return result;
    };
    const verified = verify();
    assert.equal(verified.status, 0, verified.stdout + verified.stderr);
    const original = new Map([...archives, "SHA256SUMS"].map(name => [name, readFileSync(join(dir, "dist-release", name))]));
    const corruptions = [
      () => writeFileSync(join(dir, "dist-release", archives[0]), "corrupt"),
      () => rmSync(join(dir, "dist-release", archives[1])),
      () => {
        writeFileSync(join(dir, "dist-release", archives[0]), "");
        const sums = archives.map(name => `${createHash("sha256").update(readFileSync(join(dir, "dist-release", name))).digest("hex")} *${name}`).join("\n") + "\n";
        writeFileSync(join(dir, "dist-release/SHA256SUMS"), sums);
      },
      () => {
        const lines = original.get("SHA256SUMS").toString().trim().split("\n");
        lines[1] = lines[0];
        writeFileSync(join(dir, "dist-release/SHA256SUMS"), lines.join("\n") + "\n");
      },
      () => writeFileSync(join(dir, "dist-release/.unexpected"), "hidden extra"),
      () => {
        rmSync(join(dir, "dist-release", archives[0]));
        symlinkSync(join(dir, "cmd/sayumi/main.go"), join(dir, "dist-release", archives[0]));
      },
    ];
    for (const corrupt of corruptions) {
      for (const [name, data] of original) {
        rmSync(join(dir, "dist-release", name), { force: true });
        writeFileSync(join(dir, "dist-release", name), data);
      }
      rmSync(join(dir, "dist-release/.unexpected"), { force: true });
      corrupt();
      const failed = verify();
      assert.notEqual(failed.status, 0, "publisher accepted incomplete or corrupt artifacts");
      cases++;
    }
  });
} finally { rmSync(scratch, { recursive: true, force: true }); }
console.log(`Release regressions passed (${cases} cases plus workflow, attributes and resource contracts).`);
