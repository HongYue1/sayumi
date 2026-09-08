package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the real check.sh in a disposable checkout with command doubles.
// Keeping these in the normal Go suite makes the local and CI gate contract
// testable without installing a second test runner or recursively running Go.
func TestCheckScriptRequiredTools(t *testing.T) {
	for _, tool := range []string{"go", "bun", "gofumpt", "goimports", "golangci-lint", "govulncheck"} {
		t.Run(tool, func(t *testing.T) {
			got := runCheckScript(t, true, map[string]string{"CHECK_TEST_MISSING": tool})
			if got.code == 0 || !strings.Contains(got.output, "missing required tool: "+tool) {
				t.Fatalf("missing %s must fail with an actionable diagnostic: exit=%d\n%s", tool, got.code, got.output)
			}
			if got.calls != "" {
				t.Fatalf("required-tool failure ran downstream commands:\n%s", got.calls)
			}
		})
	}
}

func TestCheckScriptFreshFrontend(t *testing.T) {
	for _, tc := range []struct {
		name string
		warm bool
		fast bool
	}{
		{name: "cold"},
		{name: "warm", warm: true},
		{name: "fast_warm", warm: true, fast: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var args []string
			if tc.fast {
				args = []string{"--fast"}
			}
			got := runCheckScript(t, tc.warm, nil, args...)
			if got.code != 0 {
				t.Fatalf("exit=%d\n%s", got.code, got.output)
			}
			if !strings.HasPrefix(got.calls, "bun run build\n") || strings.Count(got.calls, "bun run build\n") != 1 {
				t.Fatalf("build fresh frontend exactly once, before Go checks:\n%s", got.calls)
			}
			for _, call := range []string{
				"go mod tidy -diff", "gofumpt -l cmd internal", "goimports -l -local sayumi cmd internal",
				"bun run format:check", "go vet ./...", "golangci-lint run ./... --timeout=5m",
				"govulncheck ./...", "go test -race -shuffle=on ./...", "bun run lint", "bun run check", "bun run test",
			} {
				if !strings.Contains(got.calls, call+"\n") {
					t.Errorf("required gate %q did not run:\n%s", call, got.calls)
				}
			}
			built := strings.Contains(got.calls, "go build -o /dev/null ./cmd/sayumi\n")
			if built == tc.fast {
				t.Errorf("production build ran=%t with fast=%t", built, tc.fast)
			}
			if tc.fast && (!strings.Contains(got.output, "fast checks passed") || strings.Contains(got.output, "all checks passed")) {
				t.Errorf("fast mode claimed a full check:\n%s", got.output)
			}
		})
	}
}

func TestCheckScriptFailures(t *testing.T) {
	for _, call := range []string{
		"bun run build", "go mod tidy -diff", "gofumpt -l cmd internal", "goimports -l -local sayumi cmd internal",
		"bun run format:check", "go vet ./...", "golangci-lint run ./... --timeout=5m", "govulncheck ./...",
		"go env CGO_ENABLED", "go test -race -shuffle=on ./...", "bun run lint", "bun run check", "bun run test",
		"go build -o /dev/null ./cmd/sayumi",
	} {
		t.Run(call, func(t *testing.T) {
			got := runCheckScript(t, true, map[string]string{"CHECK_TEST_FAIL": call})
			if got.code == 0 || strings.Contains(got.output, "all checks passed") {
				t.Fatalf("failed %q produced success: exit=%d\n%s", call, got.code, got.output)
			}
			if call == "bun run build" && strings.Contains(got.calls, "go ") {
				t.Errorf("Go checks ran after a failed frontend prerequisite:\n%s", got.calls)
			}
			if call == "go env CGO_ENABLED" && strings.Contains(got.calls, "go test ") {
				t.Errorf("tests ran with unknown race configuration:\n%s", got.calls)
			}
		})
	}
	t.Run("successful_build_without_output", func(t *testing.T) {
		got := runCheckScript(t, false, map[string]string{"CHECK_TEST_NO_OUTPUT": "1"})
		if got.code == 0 || strings.Contains(got.calls, "go ") {
			t.Fatalf("missing frontend output must stop Go checks: exit=%d\n%s\n%s", got.code, got.output, got.calls)
		}
	})
}

func TestCheckScriptRacePolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
		code int
	}{
		{name: "configured_compiler", env: map[string]string{"CHECK_TEST_MISSING": "cc gcc clang"}, want: "go test -race -shuffle=on ./...\n"},
		{name: "cgo_disabled", env: map[string]string{"CHECK_TEST_CGO": "0"}, want: "go test -shuffle=on ./...\n"},
		{name: "invalid_cgo", env: map[string]string{"CHECK_TEST_CGO": "invalid"}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runCheckScript(t, false, tc.env)
			if got.code != tc.code || (tc.want != "" && !strings.Contains(got.calls, tc.want)) {
				t.Fatalf("race policy: exit=%d, want=%d\n%s\n%s", got.code, tc.code, got.output, got.calls)
			}
			if tc.code != 0 && strings.Contains(got.calls, "go test ") {
				t.Errorf("tests ran with invalid race configuration:\n%s", got.calls)
			}
			if tc.name == "cgo_disabled" && !strings.Contains(got.output, "race detector skipped") {
				t.Errorf("cgo-disabled run must disclose the missing race check:\n%s", got.output)
			}
		})
	}
}

func TestCheckScriptArguments(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		code int
	}{
		{arg: "--help"}, {arg: "-h"}, {arg: "--unknown", code: 2},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			got := runCheckScript(t, false, map[string]string{"CHECK_TEST_MISSING": "go bun"}, tc.arg)
			if got.code != tc.code || got.calls != "" {
				t.Fatalf("argument handling: exit=%d, want=%d\n%s\n%s", got.code, tc.code, got.output, got.calls)
			}
		})
	}
}

type checkScriptResult struct {
	output string
	calls  string
	code   int
}

func runCheckScript(t *testing.T, warm bool, env map[string]string, args ...string) checkScriptResult {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("check.sh contract tests require Bash: %v", err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, path := range []string{"frontend", "cmd/sayumi/dist"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"check.sh": script, "mock.sh": []byte(checkScriptMocks), "calls": {}}
	if warm {
		files["cmd/sayumi/dist/index.html"] = []byte("stale frontend")
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	argv := append([]string{"--noprofile", "--norc", "check.sh"}, args...)
	cmd := exec.CommandContext(ctx, bash, argv...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Env = append(cmd.Environ(),
		"BASH_ENV="+filepath.ToSlash(filepath.Join(dir, "mock.sh")),
		"CHECK_TEST_CALLS="+filepath.ToSlash(filepath.Join(dir, "calls")),
		"CHECK_TEST_CGO=1", "CHECK_TEST_MISSING=", "CHECK_TEST_FAIL=", "CHECK_TEST_NO_OUTPUT=0", "NO_COLOR=1")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run check.sh: %v\n%s", err, out)
	}
	if ctx.Err() != nil {
		t.Fatalf("check.sh timed out: %v\n%s", ctx.Err(), out)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return checkScriptResult{output: string(out), calls: string(calls), code: cmd.ProcessState.ExitCode()}
}

const checkScriptMocks = `
command() {
  if [[ "$1" == -v ]]; then
    case " $CHECK_TEST_MISSING " in *" $2 "*) return 1 ;; esac
    case "$2" in go|bun|npm|gofumpt|goimports|golangci-lint|govulncheck|cc|gcc|clang) printf '%s\n' "$2"; return 0 ;; esac
  fi
  builtin command "$@"
}
mock_tool() {
  printf '%s\n' "$*" >> "$CHECK_TEST_CALLS"
  case " $CHECK_TEST_MISSING " in *" $1 "*) echo "mock: $1 unavailable" >&2; return 127 ;; esac
  if [[ "$*" == "$CHECK_TEST_FAIL" ]]; then echo "mock: $* failed" >&2; return 1; fi
}
go() {
  mock_tool go "$@" || return
  if [[ "$*" == 'env CGO_ENABLED' ]]; then printf '%s\n' "$CHECK_TEST_CGO"; fi
  if [[ "$1" == build && "${CGO_ENABLED:-}" != 0 ]]; then echo 'production build needs CGO_ENABLED=0' >&2; return 1; fi
}
bun() {
  mock_tool bun "$@" || return
  if [[ "$*" == 'run build' && "$CHECK_TEST_NO_OUTPUT" != 1 ]]; then
    printf 'fresh frontend\n' > ../cmd/sayumi/dist/index.html
  fi
}
npm() { mock_tool npm "$@"; }
gofumpt() { mock_tool gofumpt "$@"; }
goimports() { mock_tool goimports "$@"; }
gofmt() { mock_tool gofmt "$@"; }
golangci-lint() { mock_tool golangci-lint "$@"; }
govulncheck() { mock_tool govulncheck "$@"; }
`
