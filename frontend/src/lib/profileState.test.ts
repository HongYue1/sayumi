import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const SOURCE_ROOT = join(process.cwd(), "src");

function productionSources(dir = SOURCE_ROOT): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...productionSources(path));
    } else if (
      /\.(?:ts|tsx)$/.test(entry.name) &&
      !entry.name.endsWith(".test.ts") &&
      !entry.name.endsWith(".test.tsx")
    ) {
      out.push(path);
    }
  }
  return out.sort();
}

function filesMatching(pattern: RegExp): string[] {
  // Normalize to POSIX separators: node:path returns backslashes on Windows,
  // and the census assertions below are written in the repo's portable form.
  return productionSources()
    .filter((path) => pattern.test(readFileSync(path, "utf8")))
    .map((path) => relative(process.cwd(), path).replaceAll("\\", "/"));
}

describe("profile-owned state boundaries", () => {
  it("keeps authentication teardown independent from profile stores", () => {
    const session = readFileSync(join(SOURCE_ROOT, "lib/session.ts"), "utf8");
    expect(session).not.toMatch(/~\/lib\/(?:settings|customThemes|library)/);
  });

  it("routes production settings loading through profile activation", () => {
    const direct = filesMatching(/\bsettings\.(?:load|reset)\s*\(/);
    expect(direct).toEqual([]);
    expect(filesMatching(/\bsettings\.activate\s*\(/)).toEqual([
      "src/App.tsx",
      "src/routes/Library.tsx",
      "src/routes/Read.tsx",
    ]);
  });

  it("gives App sole ownership of theme side effects", () => {
    expect(
      filesMatching(/import\s*\{[^}]*\bapplyTheme\b[^}]*\}\s*from/),
    ).toEqual(["src/App.tsx"]);
    expect(filesMatching(/\bthemeReady\b/)).toEqual([]);
  });

  it("routes both sign-out controls through shared feedback", () => {
    expect(filesMatching(/import\s*\{\s*signOutWithFeedback\s*\}/)).toEqual([
      "src/components/CommandPalette.tsx",
      "src/components/library/ProfileMenu.tsx",
    ]);
  });
});
