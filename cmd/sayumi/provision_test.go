package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestProvisionScriptArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "help", args: []string{"--help"}},
		{name: "short_help", args: []string{"-h"}},
		{name: "missing", code: 2},
		{name: "unknown", args: []string{"all"}, code: 2},
		{name: "extra", args: []string{"frontend", "--no-save"}, code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := provisionFixture(t)
			env := provisionMocks(t, dir, map[string]string{"CHECK_TEST_MISSING": "go bun"})
			got := runProvisionCommand(t, dir, env, "bash", append([]string{"./provision.sh"}, tc.args...)...)
			if got.code != tc.code || !strings.Contains(got.output, "Usage:") || provisionCalls(t, dir) != "" {
				t.Fatalf("arguments: exit=%d, want=%d\n%s", got.code, tc.code, got.output)
			}
		})
	}
}

func TestProvisionScriptFrontend(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		lock string
		want string
		code int
	}{
		{name: "frozen", lock: "fixture"},
		{name: "missing_lock", want: "lockfile", code: 1},
		{name: "empty_lock", lock: "", want: "lockfile", code: 1},
		{name: "missing_bun", lock: "fixture", env: map[string]string{"CHECK_TEST_MISSING": "bun"}, want: "missing required tool: bun", code: 1},
		{name: "wrong_bun", lock: "fixture", env: map[string]string{"CHECK_TEST_BUN_REVISION": "1.2.4"}, want: "expected bun@1.2.3", code: 1},
		{name: "install_failed", lock: "fixture", env: map[string]string{"CHECK_TEST_FAIL": "bun install --frozen-lockfile --ignore-scripts"}, want: "mock:", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := provisionFixture(t)
			if tc.name != "missing_lock" {
				writeProvisionFile(t, dir, "frontend/bun.lock", []byte(tc.lock))
			}
			env := provisionMocks(t, dir, tc.env)
			// Invoke from a child directory too: provisioning owns its root.
			got := runProvisionCommand(t, filepath.Join(dir, "frontend"), env, "bash", "../provision.sh", "frontend")
			calls := provisionCalls(t, dir)
			if got.code != tc.code || !strings.Contains(got.output, tc.want) {
				t.Fatalf("frontend: exit=%d, want=%d\n%s", got.code, tc.code, got.output)
			}
			if tc.code == 0 {
				want := "bun -p require(\"./frontend/package.json\").packageManager\nbun --revision\nbun install --frozen-lockfile --ignore-scripts\n"
				if calls != want {
					t.Fatalf("unexpected provisioning commands:\n%s", calls)
				}
			} else if tc.name != "install_failed" && strings.Contains(calls, " install ") {
				t.Fatalf("prerequisite failure still installed dependencies:\n%s", calls)
			}
		})
	}
}

func TestProvisionScriptGoTools(t *testing.T) {
	quality := []string{
		"github.com/golangci/golangci-lint/v2/cmd/golangci-lint",
		"golang.org/x/vuln/cmd/govulncheck",
		"mvdan.cc/gofumpt",
		"golang.org/x/tools/cmd/goimports",
	}
	for _, mode := range []string{"quality-tools", "release-tools", "workflow-tools"} {
		t.Run(mode, func(t *testing.T) {
			dir := provisionFixture(t)
			got := runProvisionCommand(t, dir, provisionMocks(t, dir, nil), "bash", "./provision.sh", mode)
			if got.code != 0 {
				t.Fatalf("tool setup: exit=%d\n%s", got.code, got.output)
			}
			want := quality
			switch mode {
			case "release-tools":
				want = []string{"github.com/tc-hib/go-winres"}
			case "workflow-tools":
				want = []string{"github.com/rhysd/actionlint/cmd/actionlint"}
			}
			calls := strings.Split(strings.TrimSpace(provisionCalls(t, dir)), "\n")
			if len(calls) != len(want) {
				t.Fatalf("got %d installs, want %d: %v", len(calls), len(want), calls)
			}
			// The implementation owns versions; assert exact pins, not a second
			// copy of those versions that would drift during an intentional bump.
			for i, name := range want {
				pattern := "^go install " + regexp.QuoteMeta(name) + `@v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
				if !regexp.MustCompile(pattern).MatchString(calls[i]) {
					t.Fatalf("tool must use an isolated, exact stable install: %q", calls[i])
				}
			}
			for _, call := range calls {
				t.Run("failure/"+strings.TrimPrefix(call, "go install "), func(t *testing.T) {
					failedDir := provisionFixture(t)
					env := provisionMocks(t, failedDir, map[string]string{"CHECK_TEST_FAIL": call})
					failed := runProvisionCommand(t, failedDir, env, "bash", "./provision.sh", mode)
					if failed.code == 0 || !strings.HasSuffix(provisionCalls(t, failedDir), call+"\n") {
						t.Fatalf("failed install must stop the sequence: exit=%d\n%s", failed.code, failed.output)
					}
				})
			}
		})
	}
	t.Run("missing_go", func(t *testing.T) {
		dir := provisionFixture(t)
		env := provisionMocks(t, dir, map[string]string{"CHECK_TEST_MISSING": "go"})
		got := runProvisionCommand(t, dir, env, "bash", "./provision.sh", "quality-tools")
		if got.code == 0 || !strings.Contains(got.output, "missing required tool: go") || provisionCalls(t, dir) != "" {
			t.Fatalf("missing Go must fail before installing: exit=%d\n%s", got.code, got.output)
		}
	})
}

// Local file dependencies exercise the real Bun installer without a registry,
// a second toolchain, or the developer's node_modules/cache. The control case
// proves these hooks really execute when the policy is absent.
func TestProvisionScriptRealBun(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "..", "frontend", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var project struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(manifest, &project); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"canonical", "config_default", "hook_control", "missing_lock", "empty_lock", "stale_lock"} {
		t.Run(name, func(t *testing.T) {
			dir := provisionFixture(t)
			frontend := filepath.Join(dir, "frontend")
			pkg := map[string]any{
				"name": "provision-fixture", "private": true, "packageManager": project.PackageManager,
				"dependencies":        map[string]string{"provision-dep": "file:./dep"},
				"trustedDependencies": []string{"provision-dep"},
				"scripts":             map[string]string{"postinstall": `bun -e 'require("node:fs").writeFileSync("root-hook", "ran")'`},
			}
			writeJSON := func(path string, value any) {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				writeProvisionFile(t, dir, path, data)
			}
			writeJSON("frontend/package.json", pkg)
			writeJSON("frontend/dep/package.json", map[string]any{
				"name": "provision-dep", "version": "1.0.0",
				"scripts": map[string]string{"postinstall": `bun -e 'require("node:fs").writeFileSync("dep-hook", "ran")'`},
			})
			env := map[string]string{"BUN_INSTALL_CACHE_DIR": filepath.Join(dir, "cache")}
			seed := runProvisionCommand(t, frontend, env, "bun", "install", "--lockfile-only", "--ignore-scripts", "--offline")
			if seed.code != 0 {
				t.Fatalf("seed local lockfile: %s", seed.output)
			}
			lockPath := filepath.Join(frontend, "bun.lock")
			lock, err := os.ReadFile(lockPath)
			if err != nil || len(lock) == 0 {
				t.Fatalf("fixture lockfile missing: %v", err)
			}
			switch name {
			case "missing_lock":
				if err := os.Remove(lockPath); err != nil {
					t.Fatal(err)
				}
			case "empty_lock":
				writeProvisionFile(t, dir, "frontend/bun.lock", nil)
			case "stale_lock":
				writeJSON("frontend/second/package.json", map[string]string{"name": "second-dep", "version": "1.0.0"})
				pkg["dependencies"].(map[string]string)["second-dep"] = "file:./second"
				writeJSON("frontend/package.json", pkg)
			case "hook_control":
				if err := os.Remove(filepath.Join(frontend, "bunfig.toml")); err != nil {
					t.Fatal(err)
				}
			}
			var got checkScriptResult
			if name == "config_default" || name == "hook_control" {
				got = runProvisionCommand(t, frontend, env, "bun", "install", "--frozen-lockfile", "--offline")
			} else {
				got = runProvisionCommand(t, dir, env, "bash", "./provision.sh", "frontend")
			}
			success := name == "canonical" || name == "config_default" || name == "hook_control"
			if (got.code == 0) != success {
				t.Fatalf("real install: exit=%d, success=%t\n%s", got.code, success, got.output)
			}
			if !success && !strings.Contains(got.output, "lockfile") {
				t.Fatalf("expected a lockfile diagnostic:\n%s", got.output)
			}
			for _, path := range []string{"root-hook", "node_modules/provision-dep/dep-hook"} {
				_, err := os.Stat(filepath.Join(frontend, path))
				if name == "hook_control" {
					if err != nil {
						t.Fatalf("control hook did not execute (%s): %v\n%s", path, err, got.output)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("install hook was not blocked (%s): %v", path, err)
				}
			}
			if name != "missing_lock" && name != "empty_lock" {
				after, err := os.ReadFile(lockPath)
				if err != nil || !bytes.Equal(lock, after) {
					t.Fatalf("frozen install changed the lockfile: %v", err)
				}
			}
		})
	}
}

func provisionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, path := range []string{"provision.sh", "frontend/bunfig.toml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		writeProvisionFile(t, dir, path, data)
	}
	return dir
}

func writeProvisionFile(t *testing.T, dir, path string, data []byte) {
	t.Helper()
	file := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func provisionMocks(t *testing.T, dir string, extra map[string]string) map[string]string {
	t.Helper()
	writeProvisionFile(t, dir, "calls", nil)
	writeProvisionFile(t, dir, "mock.sh", []byte(checkScriptMocks+`
go() {
  mock_tool go "$@" || return
  [[ "${GOTOOLCHAIN:-}" == local ]] || { echo 'tool install allowed automatic toolchain switching' >&2; return 90; }
}
`))
	env := map[string]string{
		"BASH_ENV": filepath.ToSlash(filepath.Join(dir, "mock.sh")), "GOTOOLCHAIN": "auto",
		"CHECK_TEST_CALLS":   filepath.ToSlash(filepath.Join(dir, "calls")),
		"CHECK_TEST_MISSING": "", "CHECK_TEST_FAIL": "", "CHECK_TEST_NO_OUTPUT": "0",
		"CHECK_TEST_BUN_PIN": "bun@1.2.3", "CHECK_TEST_BUN_REVISION": "1.2.3+abcdef123",
	}
	maps.Copy(env, extra)
	return env
}

func provisionCalls(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runProvisionCommand(t *testing.T, dir string, env map[string]string, program string, args ...string) checkScriptResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Env = append(cmd.Environ(), "BASH_ENV=", "NO_COLOR=1")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %s: %v\n%s", program, err, out)
	}
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v\n%s", program, ctx.Err(), out)
	}
	return checkScriptResult{output: string(out), code: cmd.ProcessState.ExitCode()}
}
