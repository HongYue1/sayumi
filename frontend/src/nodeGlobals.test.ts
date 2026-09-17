// tsconfig.json puts @types/node on everything under src/**, because the Vite
// config and the suites that read sources off disk genuinely need it. Nothing
// in the compiler then stops an application module from reaching for `process`
// or `node:fs`: tsc accepts it, the bundle ships it, and the browser throws at
// runtime. Splitting the project would cost the ~/* aliases and type-aware
// lint in tests, so the browser boundary is pinned here instead.
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const SOURCE_ROOT = join(process.cwd(), "src");

// Vitest owns these: the setup file and the shared harness run under Node by
// construction, as do the suites that read sources off disk.
function isTestOnly(path: string): boolean {
  const rel = relative(SOURCE_ROOT, path).replaceAll("\\", "/");
  return (
    /\.(?:test|spec)\.tsx?$/u.test(rel) ||
    rel === "test-setup.ts" ||
    rel.startsWith("test/")
  );
}

function appFiles(directory: string): string[] {
  const found: string[] = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      found.push(...appFiles(path));
      continue;
    }
    if (!/\.tsx?$/u.test(entry.name)) continue;
    if (isTestOnly(path)) continue;
    found.push(path);
  }
  return found;
}

// Prose and DOM types talk about nodes constantly (`{ node: Node }`, "a bare
// text node:"), so match against code only rather than chasing comments.
function stripComments(source: string): string {
  return source
    .replaceAll(/\/\*[\s\S]*?\*\//gu, "")
    .replaceAll(/\/\/[^\n]*/gu, "");
}

const NODE_ONLY: Array<{ label: string; pattern: RegExp }> = [
  { label: "a node: import", pattern: /["']node:/u },
  { label: "require()", pattern: /\brequire\s*\(/u },
  { label: "process", pattern: /\bprocess\s*\./u },
  { label: "Buffer", pattern: /\bBuffer\b/u },
  { label: "__dirname / __filename", pattern: /\b__(?:dirname|filename)\b/u },
  { label: "the NodeJS namespace", pattern: /\bNodeJS\./u },
];

describe("application sources stay browser-only", () => {
  const files = appFiles(SOURCE_ROOT);

  it("finds the application sources to scan", () => {
    // A broken walk would pass the scan below by scanning nothing.
    expect(files.length).toBeGreaterThan(50);
  });

  it("never reaches for a Node-only API", () => {
    const offenders: string[] = [];
    for (const file of files) {
      const source = stripComments(readFileSync(file, "utf8"));
      for (const { label, pattern } of NODE_ONLY) {
        if (pattern.test(source)) {
          offenders.push(`${relative(SOURCE_ROOT, file)}: ${label}`);
        }
      }
    }
    // Timer handles are the near miss worth naming: the portable
    // `ReturnType<typeof setTimeout>` is fine, `NodeJS.Timeout` is not.
    expect(offenders).toEqual([]);
  });
});
