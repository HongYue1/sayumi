import { describe, expect, it } from "vitest";
import {
  extractBookFontFamilies,
  filterReaderFontFaces,
  splitBookCSS,
  stripColorsFromCSS,
} from "./cssText";

// The host DOM's CSS parser is not Chromium's: happy-dom keeps text-decoration
// un-expanded, expands font/border/background, and drops an @import along with
// whatever rule follows it. So these assertions are written against behaviour
// every engine agrees on, and wherever the contract is "hand back what the
// engine serialized", the expectation is taken from the engine itself instead
// of being hard-coded.
function ruleText(cssText: string, index = 0): string {
  const sheet = new CSSStyleSheet();
  sheet.replaceSync(cssText);
  const rule = sheet.cssRules[index];
  if (!rule) throw new Error("fixture produced no rule: " + cssText);
  return rule.cssText;
}

function parses(cssText: string): boolean {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(cssText);
    return sheet.cssRules.length > 0;
  } catch {
    return false;
  }
}

const pageRuleParses = parses("@page :first { margin: 1cm; color: red; }");

describe("stripColorsFromCSS", () => {
  it("drops color declarations and keeps the layout ones", () => {
    const out = stripColorsFromCSS(".a { color: red; margin: 2px; }");
    expect(out).toContain("margin");
    expect(out).not.toContain("red");
  });

  it("drops a rule whose declarations were all colors", () => {
    expect(stripColorsFromCSS(".c { color: red; }").trim()).toBe("");
  });

  it("hands back the engine's own serialization for a color-free rule", () => {
    const css = ".b { margin: 4px 5px; padding: 2px 3px; }";
    expect(stripColorsFromCSS(css)).toBe(ruleText(css) + "\n");
  });

  it("removes the color from a shorthand the engine kept un-expanded", () => {
    const out = stripColorsFromCSS(".d { text-decoration: underline red; }");
    expect(out).not.toContain("red");
  });

  it("keeps !important on the declarations it keeps", () => {
    const out = stripColorsFromCSS(
      ".e { margin: 2px !important; color: red; }",
    );
    expect(out).toContain("!important");
    expect(out).not.toContain("red");
  });

  it("keeps custom properties", () => {
    expect(
      stripColorsFromCSS(".f { --brand: #abcdef; margin: 1px; }"),
    ).toContain("--brand");
  });

  it("strips inside a grouping rule and keeps its prelude", () => {
    const out = stripColorsFromCSS(
      "@media screen { .g { color: red; margin: 1px; } }",
    );
    expect(out).toContain("@media screen");
    expect(out).toContain("margin");
    expect(out).not.toContain("red");
  });

  it.skipIf(!pageRuleParses)(
    "keeps the at-keyword when rewriting a page rule",
    () => {
      const out = stripColorsFromCSS(
        "@page :first { margin: 1cm; color: red; }",
      );
      expect(out).toContain("@page");
      expect(out).not.toContain("red");
    },
  );

  // The frame strips two different inputs back to back, so a stale single-entry
  // memo here would serve one chapter's colors to the other.
  it("never serves an earlier result for a different input", () => {
    const first = stripColorsFromCSS(".h { color: red; margin: 1px; }");
    const second = stripColorsFromCSS(".i { color: blue; padding: 2px; }");
    expect(second).not.toBe(first);
    expect(second).toContain("padding");
    expect(stripColorsFromCSS(".h { color: red; margin: 1px; }")).toBe(first);
  });

  it("drops an @import, which the frame can never honour", () => {
    expect(stripColorsFromCSS('@import url("other.css");').trim()).toBe("");
  });
});

describe("splitBookCSS", () => {
  it("lifts font-family into the font arm and leaves the rest as layout", () => {
    const { fontCSS, layoutCSS } = splitBookCSS(
      ".a { font-family: Georgia; margin: 2px; }",
    );
    expect(fontCSS).toContain("font-family");
    expect(fontCSS).not.toContain("margin");
    expect(layoutCSS).toContain("margin");
    expect(layoutCSS).not.toContain("font-family");
  });

  // Colors are still present at this stage on purpose: the frame runs the strip
  // pass over each arm afterwards.
  it("passes a rule with no font declaration through verbatim", () => {
    const css = ".b { margin: 4px 5px; color: red; }";
    const { fontCSS, layoutCSS } = splitBookCSS(css);
    expect(fontCSS).toBe("");
    expect(layoutCSS).toBe(ruleText(css) + "\n");
  });

  it("keeps the grouping prelude on both arms", () => {
    const { fontCSS, layoutCSS } = splitBookCSS(
      "@media print { .c { font-family: Times; margin: 1px; } }",
    );
    expect(fontCSS).toContain("@media print");
    expect(fontCSS).toContain("font-family");
    expect(layoutCSS).toContain("@media print");
    expect(layoutCSS).toContain("margin");
  });

  it("emits no layout arm for a font-only rule", () => {
    const { fontCSS, layoutCSS } = splitBookCSS(".d { font-family: Georgia; }");
    expect(fontCSS).toContain("font-family");
    expect(layoutCSS.trim()).toBe("");
  });
});

describe("book font families", () => {
  const bookFaces = [
    '@font-face { font-family: "Book  Sans"; src: url(/api/a.woff2); }',
    "@font-face { font-family: Serifed; src: url(/api/b.woff2); }",
  ].join("\n");

  it("normalizes case and internal whitespace", () => {
    const names = extractBookFontFamilies(bookFaces);
    expect(names.has("book sans")).toBe(true);
    expect(names.has("serifed")).toBe(true);
  });

  it("drops the reader faces the book overrides, whatever the spacing", () => {
    const readerFaces = [
      '@font-face { font-family: "BOOK   SANS"; src: url(/fonts/x.woff2); }',
      '@font-face { font-family: "Reader Face"; src: url(/fonts/y.woff2); }',
    ].join("\n");
    const filtered = filterReaderFontFaces(
      readerFaces,
      extractBookFontFamilies(bookFaces),
    );
    expect(filtered).not.toContain("BOOK   SANS");
    expect(filtered).toContain("Reader Face");
  });

  it("returns the reader faces untouched when the book declares none", () => {
    const readerFaces =
      '@font-face { font-family: "Reader Face"; src: url(/fonts/y.woff2); }';
    expect(filterReaderFontFaces(readerFaces, new Set())).toBe(readerFaces);
  });
});
