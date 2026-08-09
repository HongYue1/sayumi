import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

const frontendRoot = process.cwd();

interface SourceImport {
  specifier: string;
  typeOnly: boolean;
}

function sourceImports(file: string): SourceImport[] {
  const source = readFileSync(file, "utf8");
  return Array.from(
    source.matchAll(/\bimport\s+(type\s+)?[\s\S]*?\sfrom\s+["']([^"']+)["'];/g),
    (match) => ({ specifier: match[2], typeOnly: match[1] !== undefined }),
  );
}

function withTsExtension(file: string): string {
  return file.endsWith(".ts") || file.endsWith(".tsx") ? file : file + ".ts";
}

function frameLibGraph(): string[] {
  const pending = [join(frontendRoot, "src/iframe/frame.ts")];
  const visited = new Set<string>();
  const libs = new Set<string>();

  while (pending.length > 0) {
    const file = pending.pop()!;
    if (visited.has(file)) continue;
    visited.add(file);

    for (const { specifier, typeOnly } of sourceImports(file)) {
      let target: string | null = null;
      if (specifier.startsWith("./") || specifier.startsWith("../")) {
        target = withTsExtension(resolve(dirname(file), specifier));
      } else if (specifier.startsWith("~/lib/")) {
        const relative = `src/${specifier.slice(2)}.ts`;
        libs.add(relative);
        target = join(frontendRoot, relative);
      }
      if (target && !typeOnly) pending.push(target);
    }
  }

  return [...libs].sort();
}

describe("frame HMR graph", () => {
  it("watches every lib module reachable from the iframe entry", () => {
    const config = readFileSync(join(frontendRoot, "vite.config.ts"), "utf8");
    const libs = frameLibGraph();

    expect(libs).toEqual([
      "src/lib/cfi.ts",
      "src/lib/frameMessages.ts",
      "src/lib/href.ts",
      "src/lib/keyboard.ts",
      "src/lib/searchMarks.ts",
      "src/lib/searchText.ts",
    ]);
    for (const lib of libs) expect(config).toContain(`"${lib}"`);
  });
});
