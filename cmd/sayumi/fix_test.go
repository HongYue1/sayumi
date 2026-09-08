package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the entry points with command doubles, never auto-fix the checkout.
func TestFixScriptPreflight(t *testing.T) {
	for _, tool := range []string{"go", "bun", "goimports", "gofumpt", "golangci-lint"} {
		t.Run(tool, func(t *testing.T) {
			got := runFixScript(t, map[string]string{"CHECK_TEST_MISSING": tool})
			if got.code == 0 || !strings.Contains(got.output, tool) || got.calls != "" {
				t.Fatalf("missing tool must stop before mutation: exit=%d\n%s\n%s", got.code, got.output, got.calls)
			}
		})
	}
	for _, revision := range []string{"1.2.4+abcdef123", "1.2.3-canary.1+abcdef123"} {
		t.Run(revision, func(t *testing.T) {
			got := runFixScript(t, map[string]string{"CHECK_TEST_BUN_REVISION": revision})
			if got.code == 0 || !strings.Contains(got.output, "expected bun@1.2.3") || strings.Contains(got.calls, "go ") || strings.Contains(got.calls, "bun run ") {
				t.Fatalf("Bun policy must precede mutation: exit=%d\n%s\n%s", got.code, got.output, got.calls)
			}
		})
	}
}

func TestFixScriptOrder(t *testing.T) {
	got := runFixScript(t, nil)
	if got.code != 0 {
		t.Fatalf("fix failed: exit=%d\n%s\n%s", got.code, got.output, got.calls)
	}
	last := -1
	for _, call := range []string{
		"bun run build", "go fix ./...", "golangci-lint run ./... --fix --timeout=5m",
		"goimports -w -local sayumi cmd internal", "gofumpt -w cmd internal",
		"bun run lint:fix", "bun run format", "go mod tidy",
	} {
		pos := strings.Index(got.calls, call+"\n")
		if pos <= last || strings.Count(got.calls, call+"\n") != 1 {
			t.Fatalf("%q must run once in order:\n%s", call, got.calls)
		}
		last = pos
	}
	if strings.Contains(got.calls, "npm ") || strings.Contains(got.calls, "install ") {
		t.Fatalf("fix must not install or fall back to npm:\n%s", got.calls)
	}
}

func TestFixScriptFailures(t *testing.T) {
	for _, call := range []string{
		"bun run build", "go fix ./...", "golangci-lint run ./... --fix --timeout=5m",
		"goimports -w -local sayumi cmd internal", "gofumpt -w cmd internal",
		"bun run lint:fix", "bun run format", "go mod tidy",
	} {
		t.Run(call, func(t *testing.T) {
			got := runFixScript(t, map[string]string{"CHECK_TEST_FAIL": call})
			if got.code == 0 {
				t.Fatalf("failure reported as success:\n%s\n%s", got.output, got.calls)
			}
			if call == "bun run build" && strings.Contains(got.calls, "go ") {
				t.Fatalf("Go mutation ran after failed embed prerequisite:\n%s", got.calls)
			}
		})
	}
	t.Run("missing_embed", func(t *testing.T) {
		got := runFixScript(t, map[string]string{"CHECK_TEST_NO_OUTPUT": "1"})
		if got.code == 0 || strings.Contains(got.calls, "go ") {
			t.Fatalf("missing embed must stop Go mutation: exit=%d\n%s\n%s", got.code, got.output, got.calls)
		}
	})
}

func TestFixScriptArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{args: []string{"--help"}},
		{args: []string{"-h"}},
		{args: []string{"--unknown"}, code: 2},
		{args: []string{"--go-format", "extra"}, code: 2},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			got := runFixScript(t, nil, tc.args...)
			if got.code != tc.code || got.calls != "" {
				t.Fatalf("arguments: exit=%d, want=%d\n%s\n%s", got.code, tc.code, got.output, got.calls)
			}
		})
	}
}

func TestFmtTarget(t *testing.T) {
	for _, tc := range []struct {
		name, missing, fail, calls string
		success                    bool
	}{
		{name: "go_only", success: true, calls: "goimports -w -local sayumi cmd internal\ngofumpt -w cmd internal\n"},
		{name: "missing_goimports", missing: "goimports"},
		{name: "missing_gofumpt", missing: "gofumpt"},
		{name: "failed_goimports", fail: "goimports -w -local sayumi cmd internal", calls: "goimports -w -local sayumi cmd internal\n"},
		{name: "failed_gofumpt", fail: "gofumpt -w cmd internal", calls: "goimports -w -local sayumi cmd internal\ngofumpt -w cmd internal\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixFixture(t)
			env := provisionMocks(t, dir, map[string]string{
				"CHECK_TEST_MISSING": "go bun npm golangci-lint govulncheck " + tc.missing,
				"CHECK_TEST_FAIL":    tc.fail,
			})
			got := runProvisionCommand(t, dir, env, "make", "fmt", "SHELL=bash")
			calls := provisionCalls(t, dir)
			if (got.code == 0) != tc.success || calls != tc.calls {
				t.Fatalf("fmt: exit=%d, success=%t\n%s\ncalls: %s", got.code, tc.success, got.output, calls)
			}
		})
	}
}

func fixFixture(t *testing.T) string {
	t.Helper()
	dir := provisionFixture(t)
	for _, path := range []string{"fix.sh", "Makefile"} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		writeProvisionFile(t, dir, path, data)
	}
	writeProvisionFile(t, dir, "cmd/sayumi/dist/.gitkeep", nil)
	return dir
}

func runFixScript(t *testing.T, extra map[string]string, args ...string) checkScriptResult {
	t.Helper()
	dir := fixFixture(t)
	env := provisionMocks(t, dir, extra)
	got := runProvisionCommand(t, dir, env, "bash", append([]string{"./fix.sh"}, args...)...)
	got.calls = provisionCalls(t, dir)
	return got
}
