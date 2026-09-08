package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run the installed tools on disposable projects, not synthetic CLI mocks or
// files planted in frontend/src. No symlinks, downloads, or admin rights needed.
func TestFrontendTypePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, path, source, diagnostic string
	}{
		{name: "alias", path: "src/main.ts", source: "import { value } from '~/value'; export const result: number = value;\n"},
		{name: "source", path: "src/main.ts", source: "export const value: number = 'wrong';\n", diagnostic: "TS2322"},
		{name: "tsx", path: "src/view.tsx", source: "export const value: number = 'wrong';\n", diagnostic: "TS2322"},
		{name: "vite_config", path: "vite.config.ts", source: "export const value: number = 'wrong';\n", diagnostic: "TS2322"},
		{name: "vitest_config", path: "vitest.config.ts", source: "export const value: number = 'wrong';\n", diagnostic: "TS2322"},
		{name: "fallthrough", path: "src/main.ts", source: "export function check(value: number) { switch (value) { case 0: console.log(value); case 1: return 1; default: return 2; } }\n", diagnostic: "TS7029"},
		{name: "override", path: "src/main.ts", source: "class Base { value() { return 1; } } export class Derived extends Base { value() { return 2; } }\n", diagnostic: "TS4114"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := frontendToolingFixture(t)
			writeProvisionFile(t, dir, "src/value.ts", []byte("export const value = 42;\n"))
			writeProvisionFile(t, dir, tc.path, []byte(tc.source))
			got := runProvisionCommand(t, dir, nil, "bun", frontendToolBin(t, "typescript", "tsc"), "--project", "tsconfig.json", "--pretty", "false")
			if tc.diagnostic == "" {
				if got.code != 0 {
					t.Fatalf("valid project rejected: exit=%d\n%s", got.code, got.output)
				}
			} else if got.code == 0 || !strings.Contains(got.output, tc.diagnostic) || !strings.Contains(filepath.ToSlash(got.output), tc.path) {
				t.Fatalf("expected %s in %s: exit=%d\n%s", tc.diagnostic, tc.path, got.code, got.output)
			}
			if _, err := os.Stat(filepath.Join(dir, "src", "value.js")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("type checking must not emit JavaScript: %v", err)
			}
		})
	}
}

func TestFrontendLintPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source, diagnostic string
	}{
		{name: "clean", source: "export const value = 42;\n"},
		{name: "unused_suppression", source: "// eslint-disable-next-line no-debugger -- Deliberately stale directive.\nexport const value = 42;\n", diagnostic: "Unused eslint-disable directive"},
		{name: "promise", source: "Promise.resolve(42);\n", diagnostic: "no-floating-promises"},
		{name: "alt_text", source: "export const Cover = () => <img src='/cover.png' />;\n", diagnostic: "alt-text"},
		{name: "invalid_aria", source: "export const View = () => <div aria-labeledby='title' />;\n", diagnostic: "aria-props"},
		{name: "redundant_role", source: "export const List = () => <ul role='list' />;\n", diagnostic: "no-redundant-roles"},
		{name: "scoped_suppression", source: "// eslint-disable-next-line jsx-a11y/no-redundant-roles -- CSS list-style:none needs explicit semantics in Safari/VoiceOver.\nexport const List = () => <ul role='list' />;\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := frontendToolingFixture(t)
			writeProvisionFile(t, dir, "src/view.tsx", []byte(tc.source))
			// Resolve the installed type-aware engine from the real package root,
			// while both lint rules and TypeScript inputs come from the fixture.
			// Allow the real type-aware engine to start on cold Windows runners;
			// every lint diagnostic and expected exit status remains mandatory.
			got := runProvisionCommandWithTimeout(t, filepath.Join("..", "..", "frontend"), nil, 90*time.Second, "bun", frontendToolBin(t, "oxlint", "oxlint"),
				"--config", filepath.Join(dir, ".oxlintrc.json"), "--tsconfig", filepath.Join(dir, "tsconfig.json"),
				"--format", "json", filepath.Join(dir, "src"))
			if tc.diagnostic == "" {
				if got.code != 0 {
					t.Fatalf("valid source rejected: exit=%d\n%s", got.code, got.output)
				}
			} else if got.code == 0 || !strings.Contains(got.output, tc.diagnostic) {
				t.Fatalf("expected %s: exit=%d\n%s", tc.diagnostic, got.code, got.output)
			}
		})
	}
}

func TestFrontendFormatPolicy(t *testing.T) {
	dir := frontendToolingFixture(t)
	writeProvisionFile(t, dir, "src/view.tsx", []byte("export const view=<div class='card'>Hello</div>\n"))
	bin := frontendToolBin(t, "oxfmt", "oxfmt")
	got := runProvisionCommand(t, dir, nil, "bun", bin, "--check", "src")
	if got.code == 0 || !strings.Contains(got.output, "view.tsx") {
		t.Fatalf("unformatted TSX must fail: exit=%d\n%s", got.code, got.output)
	}
	got = runProvisionCommand(t, dir, nil, "bun", bin, "src")
	if got.code != 0 {
		t.Fatalf("format TSX: exit=%d\n%s", got.code, got.output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "src", "view.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "export const view = <div class=\"card\">Hello</div>;\n" {
		t.Fatalf("unexpected spacing, quotes or semicolons: %q", data)
	}
	got = runProvisionCommand(t, dir, nil, "bun", bin, "--check", "src")
	if got.code != 0 {
		t.Fatalf("formatted TSX is not a fixed point: exit=%d\n%s", got.code, got.output)
	}
}

func TestGoFormatterImportComment(t *testing.T) {
	dir := t.TempDir()
	// gofumpt must not move fmt away from the comment that documents it.
	writeProvisionFile(t, dir, "sample.go", []byte("package sample\n\nimport (\n\t\"example.com/first\"\n\n\t// keep-import\n\t\"fmt\"\n)\n"))
	got := runProvisionCommand(t, dir, nil, "gofumpt", "sample.go")
	if got.code != 0 || !strings.Contains(got.output, "// keep-import\n\t\"fmt\"") {
		t.Fatalf("formatter detached the import comment: exit=%d\n%s", got.code, got.output)
	}
	writeProvisionFile(t, dir, "sample.go", []byte(got.output))
	again := runProvisionCommand(t, dir, nil, "gofumpt", "sample.go")
	if again.code != 0 || again.output != got.output {
		t.Fatalf("formatting is not idempotent: exit=%d\n%s", again.code, again.output)
	}
}

func frontendToolingFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{".oxlintrc.json", ".oxfmtrc.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "frontend", name))
		if err != nil {
			t.Fatal(err)
		}
		writeProvisionFile(t, dir, name, data)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	// The real compiler supplies standard libraries from its installed package.
	// Only ambient package types are irrelevant to these dependency-free fixtures;
	// retain the production includes, aliases, strictness, and noEmit settings.
	config["compilerOptions"].(map[string]any)["types"] = []string{}
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	writeProvisionFile(t, dir, "tsconfig.json", data)
	writeProvisionFile(t, dir, "package.json", []byte("{\"type\":\"module\"}\n"))
	return dir
}

func frontendToolBin(t *testing.T, pkg, bin string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "frontend", "node_modules", pkg, "bin", bin))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("missing frontend tool %s; run make deps: %v", bin, err)
	}
	return path
}
