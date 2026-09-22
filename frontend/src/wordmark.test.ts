import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The wordmark is gradient text (background-clip: text), which paints only
// inside the padding box. With a tight line-height at display sizes the
// italic descender of the "y" hangs below that box and gets sliced flat, so
// the block needs bottom padding past the overhang -- cancelled by an equal
// negative margin so surrounding layout does not shift.
const BLOCK = /\.wordmark\s*\{([^}]*)\}/;
const MIN_DESCENDER_ROOM_EM = 0.2;

function declaration(block: string, property: string): string {
  const declarations = block
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split(";")
    .map((entry) => entry.trim());
  const match = declarations.find((entry) => entry.startsWith(`${property}:`));
  expect(match, `${property} missing from .wordmark`).toBeDefined();
  return match!.slice(property.length + 1).trim();
}

function sides(value: string): { top: number; bottom: number } {
  const parts = value.split(/\s+/).map((part) => Number.parseFloat(part));
  return { top: parts[0], bottom: parts[2] ?? parts[0] };
}

describe("wordmark gradient text", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = BLOCK.exec(css)?.[1];

  it("clips the gradient to text", () => {
    expect(block).toBeDefined();
    expect(block).toContain("background-clip: text");
  });

  it("pads past the italic descender so it is not sliced", () => {
    const padding = sides(declaration(block!, "padding"));
    expect(padding.bottom).toBeGreaterThanOrEqual(MIN_DESCENDER_ROOM_EM);
  });

  it("cancels that padding with matching negative margins", () => {
    const padding = sides(declaration(block!, "padding"));
    const margin = sides(declaration(block!, "margin"));
    expect(margin.top).toBeCloseTo(-padding.top);
    expect(margin.bottom).toBeCloseTo(-padding.bottom);
  });
});

// Two serifs on purpose: headings are Newsreader, the wordmark stays
// Fraunces italic. Collapsing them onto one token is the easy mistake, so
// the split is asserted rather than left to a comment.
describe("display and wordmark serifs are separate", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = BLOCK.exec(css)?.[1];

  function token(name: string): string {
    const match = new RegExp(`--${name}:\\s*([^;]+);`).exec(css);
    expect(match, `--${name} missing from app.css`).not.toBeNull();
    return match![1].trim();
  }

  it("sets the wordmark from its own token", () => {
    expect(declaration(block!, "font-family")).toBe("var(--font-wordmark)");
    expect(declaration(block!, "font-style")).toBe("italic");
  });

  it("keeps Fraunces for the wordmark and Newsreader for headings", () => {
    expect(token("font-wordmark")).toMatch(/^"Fraunces"/);
    expect(token("font-display")).toMatch(/^"Newsreader"/);
  });

  // Newsreader ships both styles; Fraunces ships italic only, because the
  // wordmark is the sole user of the family and it is always italic. The
  // upright cut is not embedded, so declaring it would only invite a rule
  // that silently falls back to Georgia.
  it("embeds exactly the shell serif faces that are used", () => {
    for (const file of [
      "Fraunces-Italic-VariableFont.woff2",
      "Newsreader-VariableFont.woff2",
      "Newsreader-Italic-VariableFont.woff2",
    ]) {
      expect(css).toContain(`url("/fonts/${file}")`);
    }
    expect(css).not.toContain("Fraunces-VariableFont.woff2");
  });
});
