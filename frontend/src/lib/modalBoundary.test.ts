import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return entry.isFile() && entry.name.endsWith(".tsx") ? [path] : [];
  });
}

describe("modal boundaries", () => {
  it("routes every modal dialog through the shared focus and scroll owner", () => {
    const dialogTags = sourceFiles(join(process.cwd(), "src")).flatMap((path) =>
      Array.from(
        readFileSync(path, "utf8").matchAll(
          /<[A-Za-z][^>]*\brole="dialog"[^>]*>/gu,
        ),
        (match) => ({ path, tag: match[0] }),
      ),
    );

    expect(dialogTags).toHaveLength(10);
    for (const { path, tag } of dialogTags) {
      expect(tag, path).toContain('aria-modal="true"');
      expect(tag, path).toContain("ref={trap()}");
    }
  });
});
