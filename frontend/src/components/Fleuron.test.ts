import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// U+2767 (ROTATED FLORAL HEART BULLET). No bundled face carries it and the UI
// stack has no system fallback to lean on, so a literal one renders as tofu on
// a stock Linux box. The ornament ships as an SVG path instead; this guards
// the character from creeping back into the UI.
const FLEURON_CHAR = "\u2767";
const UI_SOURCE = /\.(tsx|ts|css|html)$/;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) return sourceFiles(full);
    return UI_SOURCE.test(entry.name) ? [full] : [];
  });
}

describe("fleuron ornament", () => {
  it("is drawn as geometry, not as a font glyph", () => {
    const svg = readFileSync("src/components/Fleuron.tsx", "utf8");
    expect(svg).toContain("<svg");
    expect(svg).toContain("currentColor");
  });

  it("scales with the font-size of whatever mark hosts it", () => {
    const css = readFileSync("src/app.css", "utf8");
    const block = /\.fleuron-svg\s*\{([^}]*)\}/.exec(css)?.[1] ?? "";
    expect(block).toMatch(/width:\s*[\d.]+em/);
    expect(block).toContain("fill: currentColor");
  });

  it("leaves no literal U+2767 in the UI sources", () => {
    const offenders = sourceFiles("src").filter(
      (file) =>
        !file.endsWith("Fleuron.test.ts") &&
        readFileSync(file, "utf8").includes(FLEURON_CHAR),
    );
    expect(offenders).toEqual([]);
  });
});
