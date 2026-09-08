package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Check the assets actually linked by go:embed, not another copy of dist.
func TestEmbeddedBuildEntrypoints(t *testing.T) {
	index, err := frontendDist.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(string(index), -1)
	kinds := map[string]bool{}
	for _, ref := range refs {
		path := "dist" + ref[1]
		info, err := fs.Stat(frontendDist, path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("invalid embedded asset %q", path)
		}
		kinds[filepath.Ext(path)] = true
	}
	if !kinds[".js"] || !kinds[".css"] || strings.Contains(string(index), "/@vite/client") {
		t.Fatal("embedded index must reference production JavaScript and CSS")
	}
}

// Use the real entry points and package scripts, with only tool executables
// doubled. The checkout has spaces and non-executable scripts, like an archive
// extracted on Windows; Make must not depend on preserved executable bits.
func TestBuildScript(t *testing.T) {
	for _, tc := range []struct {
		name, index, want string
		args              []string
		env               map[string]string
		code              int
		build, web        bool
	}{
		{name: "cold", build: true, web: true},
		{name: "warm", index: "stale", build: true, web: true},
		{name: "help", args: []string{"--help"}, env: map[string]string{"CHECK_TEST_MISSING": "go bun"}, want: "Usage:"},
		{name: "short_help", args: []string{"-h"}, env: map[string]string{"CHECK_TEST_MISSING": "go bun"}, want: "Usage:"},
		{name: "bad_option", args: []string{"--wat"}, code: 2, want: "unknown option"},
		{name: "missing_go", env: map[string]string{"CHECK_TEST_MISSING": "go"}, code: 1, want: "missing required tool: go"},
		{name: "missing_bun_with_npm", env: map[string]string{"CHECK_TEST_MISSING": "bun"}, code: 1, want: "missing required tool: bun"},
		{name: "wrong_bun", env: map[string]string{"CHECK_TEST_BUN_REVISION": "1.2.4+wrong"}, code: 1, want: "expected bun@1.2.3"},
		{name: "canary_bun", env: map[string]string{"CHECK_TEST_BUN_REVISION": "1.2.3-canary.1+wrong"}, code: 1, want: "expected bun@1.2.3"},
		{name: "failed_frontend", index: "stale", env: map[string]string{"CHECK_TEST_FAIL": "bun node_modules/vite/bin/vite.js build"}, code: 1, web: true, want: "failed"},
		{name: "missing_output", env: map[string]string{"CHECK_TEST_NO_OUTPUT": "1"}, code: 1, web: true, want: "missing or empty"},
		{name: "empty_output", index: "empty", env: map[string]string{"CHECK_TEST_NO_OUTPUT": "1"}, code: 1, web: true, want: "missing or empty"},
		{name: "skip_missing", args: []string{"--skip-web"}, code: 1, want: "missing or empty"},
		{name: "skip_empty", index: "empty", args: []string{"--skip-web"}, code: 1, want: "missing or empty"},
		{name: "skip_directory", index: "directory", args: []string{"--skip-web"}, code: 1, want: "missing or empty"},
		{name: "skip_without_bun", index: "stale", args: []string{"--skip-web"}, env: map[string]string{"CHECK_TEST_MISSING": "bun"}, build: true, want: "potentially stale"},
		{name: "explicit_cpu", env: map[string]string{"GOAMD64": "v2"}, build: true, web: true, want: "GOAMD64=v2"},
		{name: "windows_suffix", env: map[string]string{"BUILD_TEST_EXE": ".exe"}, build: true, web: true, want: "built ./sayumi.exe"},
		{name: "cross_build", env: map[string]string{"BUILD_TEST_ARCH": "arm64", "GOAMD64": "v4"}, build: true, web: true},
		{name: "cross_run", args: []string{"--run"}, env: map[string]string{"BUILD_TEST_OS": "windows"}, code: 1, want: "--run requires a native target"},
		{name: "failed_go_never_runs", args: []string{"--run"}, env: map[string]string{"BUILD_TEST_GO_EXIT": "23"}, code: 23, build: true, web: true},
		{name: "run_exit", args: []string{"--run"}, env: map[string]string{"BUILD_TEST_RUN_EXIT": "37"}, code: 37, build: true, web: true, want: "mock binary ran"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, env := buildFixture(t, tc.index, tc.env)
			args := append([]string{"../build.sh"}, tc.args...)
			got := runProvisionCommand(t, filepath.Join(dir, "elsewhere"), env, "bash", args...)
			calls := provisionCalls(t, dir)
			if got.code != tc.code || !strings.Contains(got.output, tc.want) {
				t.Fatalf("exit=%d, want=%d, diagnostic=%q\n%s\n%s", got.code, tc.code, tc.want, got.output, calls)
			}
			if strings.Contains(calls, "go build ") != tc.build || strings.Contains(calls, "bun node_modules/vite/bin/vite.js build\n") != tc.web {
				t.Fatalf("wrong downstream work (build=%t, web=%t):\n%s", tc.build, tc.web, calls)
			}
			if strings.Contains(calls, "npm ") || strings.Contains(calls, "install ") {
				t.Fatalf("build used a fallback or installed tools:\n%s", calls)
			}
			if tc.build && (!strings.Contains(calls, "go build -trimpath -ldflags -s -w -X main.version=dev ") || !strings.Contains(calls, "-X main.buildDate=")) {
				t.Fatalf("lost reproducible-path or source-archive version metadata flags:\n%s", calls)
			}
			if tc.build && tc.web && strings.Index(calls, "go build ") < strings.Index(calls, "bun node_modules/vite/bin/vite.js build\n") {
				t.Fatalf("Go compiled before the fresh frontend:\n%s", calls)
			}
			if tc.name == "failed_go_never_runs" && strings.Contains(got.output, "mock binary ran") {
				t.Fatal("ran an old binary after the build failed")
			}
			if tc.name == "skip_without_bun" && strings.Contains(calls, "bun ") {
				t.Fatalf("explicit reuse required Bun:\n%s", calls)
			}
		})
	}
}

func TestBuildMakeEntrypoints(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	// Native Windows Make needs the absolute path to select the POSIX shell.
	shell := "SHELL=" + filepath.ToSlash(bash)
	for _, target := range []string{"build", "web", "version"} {
		t.Run(target, func(t *testing.T) {
			dir, env := buildFixture(t, "", nil)
			got := runProvisionCommand(t, dir, env, "make", shell, target)
			calls := provisionCalls(t, dir)
			if got.code != 0 || strings.Count(calls, "bun node_modules/vite/bin/vite.js build\n") != 1 {
				t.Fatalf("Make target must build the frontend exactly once: exit=%d\n%s\n%s", got.code, got.output, calls)
			}
		})
	}
	t.Run("web_without_bun", func(t *testing.T) {
		dir, env := buildFixture(t, "", map[string]string{"CHECK_TEST_MISSING": "bun"})
		got := runProvisionCommand(t, dir, env, "make", shell, "web")
		calls := provisionCalls(t, dir)
		if got.code == 0 || !strings.Contains(calls, "bun run build") || strings.Contains(calls, "npm ") {
			t.Fatalf("make web must fail without Bun, not fall back to npm: exit=%d\n%s", got.code, got.output)
		}
	})
}

func TestVitePackageEntrypoints(t *testing.T) {
	for _, name := range []string{"dev", "build", "preview"} {
		for _, wrong := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/pinned", true: "/wrong_revision"}[wrong], func(t *testing.T) {
				dir, env := buildFixture(t, "", nil)
				if wrong {
					env["CHECK_TEST_BUN_REVISION"] = "1.2.3-canary.1+wrong"
				}
				got := runProvisionCommand(t, filepath.Join(dir, "frontend"), env, "bash", "-c", frontendBuildScripts(t)[name])
				calls := provisionCalls(t, dir)
				if wrong {
					if got.code == 0 || !strings.Contains(got.output, "expected bun@") || strings.Contains(calls, "node_modules/vite/") {
						t.Fatalf("Vite ran under an unselected revision: exit=%d\n%s\n%s", got.code, got.output, calls)
					}
				} else if got.code != 0 || !strings.Contains(calls, "bun node_modules/vite/bin/vite.js") {
					t.Fatalf("Vite did not run under Bun: exit=%d\n%s\n%s", got.code, got.output, calls)
				}
			})
		}
	}
}

func frontendBuildScripts(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg.Scripts
}

func buildFixture(t *testing.T, index string, extra map[string]string) (string, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkout with spaces")
	for _, path := range []string{"build.sh", "Makefile", "provision.sh"} {
		data, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		writeProvisionFile(t, dir, path, data)
	}
	for _, path := range []string{"elsewhere", "frontend", "cmd/sayumi/dist"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if index == "directory" {
		if err := os.Mkdir(filepath.Join(dir, "cmd", "sayumi", "dist", "index.html"), 0o700); err != nil {
			t.Fatal(err)
		}
	} else if index != "" {
		data := []byte(index)
		if index == "empty" {
			data = nil
		}
		writeProvisionFile(t, dir, "cmd/sayumi/dist/index.html", data)
	}
	for _, name := range []string{"sayumi", "sayumi.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\necho 'mock binary ran'\nexit \"${BUILD_TEST_RUN_EXIT:-0}\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := provisionMocks(t, dir, extra)
	env["BUILD_TEST_WEB_SCRIPT"] = frontendBuildScripts(t)["build"]
	writeProvisionFile(t, dir, "mock.sh", []byte(checkScriptMocks+buildScriptMocks))
	return dir, env
}

const buildScriptMocks = `
go() {
  mock_tool go "$@" || return
  [[ "${GOTOOLCHAIN:-}" == local ]] || { echo 'automatic compiler selection enabled' >&2; return 90; }
  case "$*" in
    'env GOEXE') printf '%s\n' "${BUILD_TEST_EXE:-}" ;;
    'env GOARCH') printf '%s\n' "${BUILD_TEST_ARCH:-amd64}" ;;
    'env GOAMD64') printf '%s\n' "${GOAMD64:-v1}" ;;
    'env GOOS') printf '%s\n' "${BUILD_TEST_OS:-linux}" ;;
    'env GOHOSTOS') echo linux ;;
    'env GOHOSTARCH') echo amd64 ;;
  esac
  if [[ "$1" == build ]]; then
    [[ "${CGO_ENABLED:-}" == 0 ]] || { echo 'production build enabled cgo' >&2; return 91; }
    return "${BUILD_TEST_GO_EXIT:-0}"
  fi
}
bun() {
  mock_tool bun "$@" || return
  case "$*" in
    '-p require("./frontend/package.json").packageManager') printf '%s\n' "$CHECK_TEST_BUN_PIN" ;;
    --revision) printf '%s\n' "$CHECK_TEST_BUN_REVISION" ;;
    'run build') bash -c "$BUILD_TEST_WEB_SCRIPT" ;;
    'node_modules/vite/bin/vite.js build')
      if [[ "$CHECK_TEST_NO_OUTPUT" != 1 ]]; then printf 'fresh frontend\n' > ../cmd/sayumi/dist/index.html; fi
      ;;
  esac
}
`
