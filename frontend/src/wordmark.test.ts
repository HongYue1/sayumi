import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// The wordmark is Cinzel in solid ink, tracked out and set as caps. Cinzel
// ships no lowercase, so the uppercase transform is load-bearing — dropping
// it paints glyph fallbacks, not the name. The transform leaves the DOM text
// exactly "Sayumi", which is what assistive tech and tests read.
const BLOCK = /\.wordmark\s*\{([^}]*)\}/;

function declaration(block: string, property: string): string {
  const declarations = block
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split(";")
    .map((entry) => entry.trim());
  const match = declarations.find((entry) => entry.startsWith(`${property}:`));
  expect(match, `${property} missing from .wordmark`).toBeDefined();
  return match!.slice(property.length + 1).trim();
}

describe("wordmark ink and imprint proportions", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = BLOCK.exec(css)?.[1];

  // No decoration: the name itself is the identity. A stroke, underline, or
  // clipped-gradient fill would each need its own guard, so the absence of
  // the pseudo-element is asserted outright.
  it("draws the name with no ornament at all", () => {
    expect(css).not.toMatch(/\.wordmark::(after|before)/);
  });

  it("sets the letters in solid ink, not clipped gradient text", () => {
    expect(block).toBeDefined();
    expect(declaration(block!, "color")).toBe("var(--fg)");
    expect(block).not.toContain("background-clip");
  });

  it("carries no ornament on the lockup itself", () => {
    // The login/library/about locks show the name alone; the press mark
    // (Fleuron) is reserved for empty states and cover placeholders.
    const sheet = readFileSync("src/app.css", "utf8");
    for (const dead of [".login-mark", ".lib-lockup-mark", ".about-mark"]) {
      expect(sheet).not.toContain(dead);
    }
  });
});

describe("wordmark is set as an imprint", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = BLOCK.exec(css)?.[1];

  function token(name: string): string {
    const match = new RegExp(`--${name}:\\s*([^;]+);`).exec(css);
    expect(match, `--${name} missing from app.css`).not.toBeNull();
    return match![1].trim();
  }

  it("uppercases the name and tracks it out", () => {
    expect(declaration(block!, "text-transform")).toBe("uppercase");
    expect(declaration(block!, "letter-spacing")).toBe("0.2em");
    // The trailing letter-space is cancelled so centered contexts center the
    // ink rather than the tracking slot after the last letter.
    expect(declaration(block!, "margin-right")).toBe("-0.2em");
    // Cinzel is all-caps, so the box can hug the glyphs at any size.
    expect(declaration(block!, "line-height")).toBe("1");
  });

  it("sets the wordmark from its own token", () => {
    expect(declaration(block!, "font-family")).toBe("var(--font-wordmark)");
    expect(token("font-wordmark")).toMatch(/^"Cinzel"/);
  });

  // Two serifs on purpose: headings are Newsreader, the wordmark stays
  // Cinzel. Collapsing them onto one token is the easy mistake, so the split
  // is asserted rather than left to a comment.
  it("keeps Cinzel for the wordmark and Newsreader for headings", () => {
    expect(token("font-display")).toMatch(/^"Newsreader"/);
  });

  // Cinzel ships only the shapes the wordmark paints: one variable face,
  // roman, 400..900. An italic face would be dead weight in the binary, and
  // the shell no longer embeds Fraunces at all.
  it("embeds exactly the shell serif faces that are used", () => {
    expect(css).toContain(`url("/fonts/Cinzel-VariableFont.woff2")`);
    expect(css).not.toContain("Fraunces");
    for (const file of [
      "Newsreader-VariableFont.woff2",
      "Newsreader-Italic-VariableFont.woff2",
      "HankenGrotesk-VariableFont.woff2",
      "HankenGrotesk-Italic-VariableFont.woff2",
    ]) {
      expect(css).toContain(`url("/fonts/${file}")`);
    }
  });
});
