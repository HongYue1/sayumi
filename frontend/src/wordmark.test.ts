import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The wordmark is solid ink plus one accent pen stroke drawn by ::after.
// The stroke must stay decorative (no generated text, so the accessible name
// is exactly "Sayumi") and positioned against the wordmark's own box.
const BLOCK = /\.wordmark\s*\{([^}]*)\}/;
const STROKE = /\.wordmark::after\s*\{([^}]*)\}/;

function declaration(block: string, property: string): string {
  const declarations = block
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split(";")
    .map((entry) => entry.trim());
  const match = declarations.find((entry) => entry.startsWith(`${property}:`));
  expect(match, `${property} missing from .wordmark`).toBeDefined();
  return match!.slice(property.length + 1).trim();
}

describe("wordmark ink and pen stroke", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = BLOCK.exec(css)?.[1];
  const stroke = STROKE.exec(css)?.[1];

  // Gradient-clipped text sliced the italic descenders unless padded, and
  // turned muddy at bar sizes; the ink is solid now.
  it("sets the letters in solid ink, not clipped gradient text", () => {
    expect(block).toBeDefined();
    expect(declaration(block!, "color")).toBe("var(--fg)");
    expect(block).not.toContain("background-clip");
  });

  it("anchors the stroke to the wordmark's own box", () => {
    expect(declaration(block!, "position")).toBe("relative");
    expect(declaration(block!, "line-height")).toBe("1");
  });

  it("draws the stroke as decoration in the theme accent", () => {
    expect(stroke).toBeDefined();
    expect(declaration(stroke!, "content")).toBe('""');
    expect(declaration(stroke!, "position")).toBe("absolute");
    expect(declaration(stroke!, "background")).toBe("var(--accent-ink)");
    expect(declaration(stroke!, "pointer-events")).toBe("none");
    expect(stroke).toMatch(/(^|\s)mask:/);
    expect(stroke).toContain("-webkit-mask:");
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
