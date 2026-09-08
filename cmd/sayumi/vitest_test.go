package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Exercise the real runner and production config on disposable projects. A
// config-string assertion cannot prove that a diagnostic actually fails a run.
func TestVitestPolicy(t *testing.T) {
	frontend, err := filepath.Abs(filepath.Join("..", "..", "frontend"))
	if err != nil {
		t.Fatal(err)
	}
	module := strconv.Quote(filepath.ToSlash(filepath.Join(frontend, "node_modules", "vitest", "dist", "index.js")))
	imports := "import { afterAll, describe, expect, test, vi } from " + module + ";\n"
	for _, tc := range []struct {
		name, path, source, diagnostic string
	}{
		{name: "test_ts", path: "src/policy.test.ts", source: `test("storage", () => { localStorage.setItem("policy", "ok"); expect(localStorage.getItem("policy")).toBe("ok"); localStorage.removeItem("policy"); });`},
		{name: "spec_ts", path: "src/policy.spec.ts", source: `test("selected spec", () => expect(true).toBe(true));`},
		{name: "test_tsx", path: "src/policy.test.tsx", source: `test("selected tsx", () => expect(true).toBe(true));`},
		{name: "spec_tsx", path: "src/policy.spec.tsx", source: `test("selected spec tsx", () => expect(true).toBe(true));`},
		{name: "outside_src", path: "policy.test.ts", source: `test("not selected", () => {});`, diagnostic: "No test files found"},
		{name: "only_test", path: "src/policy.test.ts", source: `test.only("focused", () => expect(true).toBe(true));`, diagnostic: ".only"},
		{name: "only_suite", path: "src/policy.test.ts", source: `describe.only("focused suite", () => { test("focused", () => expect(true).toBe(true)); });`, diagnostic: ".only"},
		{name: "assertion", path: "src/policy.test.ts", source: `test("failed assertion", () => expect("actual").toBe("expected"));`, diagnostic: "expected"},
		{name: "swallowed_fetch", path: "src/policy.test.ts", source: `test("escaped request", async () => { await fetch("http://127.0.0.1:0/policy-test").catch(() => {}); });`, diagnostic: "Unmocked network call"},
		{name: "teardown_fetch", path: "src/policy.test.ts", source: `afterAll(async () => { await fetch("http://127.0.0.1:0/policy-teardown").catch(() => {}); }); test("clean body", () => expect(true).toBe(true));`, diagnostic: "Unmocked network call"},
		{name: "mocked_fetch", path: "src/policy.test.ts", source: `test("mocked request", async () => { vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("ok"))); try { expect(await (await fetch("/policy")).text()).toBe("ok"); } finally { vi.unstubAllGlobals(); } });`},
		{name: "restored_fetch_guard", path: "src/policy.test.ts", source: `test("restored guard", async () => { vi.stubGlobal("fetch", vi.fn()); vi.unstubAllGlobals(); await fetch(new URL("http://127.0.0.1:0/policy-restored")).catch(() => {}); });`, diagnostic: "Unmocked network call"},
		{name: "unawaited_assertion", path: "src/policy.test.ts", source: `test("unawaited assertion", () => { void expect(Promise.resolve(true)).resolves.toBe(true); });`, diagnostic: "not awaited"},
		{name: "unhandled_rejection", path: "src/policy.test.ts", source: `test("unhandled rejection", async () => { void Promise.reject(new Error("policy-unhandled")); await new Promise(resolve => setTimeout(resolve, 0)); });`, diagnostic: "policy-unhandled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Windows may put TempDir under an 8.3 alias. Vite compares module
			// IDs against real paths, so use one spelling throughout the fixture.
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			writeProvisionFile(t, dir, "package.json", []byte("{\"type\":\"module\"}\n"))
			writeProvisionFile(t, dir, tc.path, []byte(imports+tc.source+"\n"))
			// Only relocate the fixture root and setup path; keep real selection,
			// plugins, browser conditions, isolation, and failure policy intact.
			config := fmt.Sprintf(`import config from %q;
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
const require = createRequire(%q);
const jestDomPackage = require.resolve("@testing-library/jest-dom/package.json");
const jestDom = resolve(dirname(jestDomPackage), require(jestDomPackage).exports["./vitest"].import.default);
export default {
  ...config,
  root: %q,
  cacheDir: %q,
  // This temporary project imports the production setup and installed tools.
  server: { fs: { allow: [%q, %q] } },
  test: {
    ...config.test,
    // Resolve the Solid plugin's auto-injected matchers from the real install.
    setupFiles: [%q, jestDom],
    fileParallelism: false,
    maxWorkers: 1,
  },
};
`, filepath.ToSlash(filepath.Join(frontend, "vitest.config.ts")), filepath.ToSlash(filepath.Join(frontend, "package.json")), filepath.ToSlash(dir), filepath.ToSlash(filepath.Join(dir, "cache")), filepath.ToSlash(frontend), filepath.ToSlash(dir), filepath.ToSlash(filepath.Join(frontend, "src", "test-setup.ts")))
			writeProvisionFile(t, dir, "vitest.config.mjs", []byte(config))
			got := runProvisionCommand(t, frontend, map[string]string{"CI": "", "GITHUB_ACTIONS": ""}, "bun", "run", "test", "--config", filepath.Join(dir, "vitest.config.mjs"), "--reporter=verbose", "--no-color")
			if tc.diagnostic == "" {
				if got.code != 0 || !strings.Contains(got.output, "1 passed") {
					t.Fatalf("valid fixture did not run: exit=%d\n%s", got.code, got.output)
				}
			} else if got.code == 0 || !strings.Contains(got.output, tc.diagnostic) {
				t.Fatalf("expected failing %s diagnostic: exit=%d\n%s", tc.diagnostic, got.code, got.output)
			}
		})
	}
}
