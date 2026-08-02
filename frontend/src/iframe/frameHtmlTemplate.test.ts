import { describe, expect, it } from "vitest";
import { renderFrameSrcdoc } from "./frameHtmlTemplate";

const base = {
  nonce: "abc-123",
  theme: "catppuccin",
  frameCSS: "html { color: red }",
  frameScript: "console.log(1)",
};

describe("renderFrameSrcdoc", () => {
  it("emits the theme class frame.ts reads back", () => {
    expect(renderFrameSrcdoc(base)).toContain(
      '<html class="theme-catppuccin">',
    );
  });

  it("falls back to light for an id that is not a legal theme id", () => {
    // Stripping would have produced theme-epia: well-formed, matches no rule.
    const html = renderFrameSrcdoc({ ...base, theme: "Sepia" });
    expect(html).toContain('class="theme-light"');
    expect(html).not.toContain("theme-epia");
  });

  it("inlines a custom palette so the first paint is not the light default", () => {
    const html = renderFrameSrcdoc({
      ...base,
      theme: "ct-9f2",
      themeVars: "--bg-primary: #101014; --text-primary: #e6e6e6;",
    });
    expect(html).toContain(
      '<style id="initial-theme-css">html { --bg-primary: #101014; --text-primary: #e6e6e6; }</style>',
    );
  });

  it("leaves the initial-theme slot empty for a built-in theme", () => {
    expect(renderFrameSrcdoc(base)).toContain(
      '<style id="initial-theme-css"></style>',
    );
  });

  it("keeps the style slots in cascade order", () => {
    const html = renderFrameSrcdoc(base);
    const at = [
      "base-css",
      "initial-theme-css",
      "font-face-css",
      "book-css",
      "override-css",
    ].map((id) => html.indexOf(`id="${id}"`));
    expect(at.every((i) => i > -1)).toBe(true);
    expect(at).toEqual([...at].sort((a, b) => a - b));
  });

  it("neutralizes a raw-text terminator in the script payload", () => {
    const html = renderFrameSrcdoc({
      ...base,
      frameScript: 'const s = "</script><img src=x>";',
    });
    expect(html).toContain('"<\\/script><img src=x>"');
    // Only the terminator this template wrote is a real end tag.
    expect(html.match(/<\/script>/g)).toHaveLength(1);
  });

  it("neutralizes a raw-text terminator in both css payloads", () => {
    const html = renderFrameSrcdoc({
      ...base,
      frameCSS: 'a::after { content: "</style>" }',
      themeVars: '--x: "</style>"',
    });
    // Five slots, five end tags: neither payload contributed one.
    expect(html.match(/<\/style>/g)).toHaveLength(5);
  });

  it("locks behavior down in the CSP", () => {
    const html = renderFrameSrcdoc(base);
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("script-src 'nonce-abc-123'");
    expect(html).toContain('<script nonce="abc-123">');
    expect(html).toContain("connect-src 'none'");
    expect(html).toContain("style-src 'unsafe-inline';");
  });

  it("keeps asset sources permissive so real EPUBs render", () => {
    const html = renderFrameSrcdoc(base);
    expect(html).toContain("font-src * data: blob:");
    expect(html).toContain("img-src * data: blob:");
    expect(html).toContain("media-src * data: blob:");
  });

  it("refuses a nonce with no usable characters", () => {
    expect(() => renderFrameSrcdoc({ ...base, nonce: "!!!" })).toThrow(/nonce/);
  });

  it("emits the skeleton frame.ts resolves by id", () => {
    const html = renderFrameSrcdoc(base);
    expect(html).toContain('<div id="paged-clip">');
    expect(html).toContain('<div id="content">');
    expect(html).toContain('<div id="content-inner">');
  });

  it("sets a sanitized lang, and omits the attribute when absent", () => {
    expect(renderFrameSrcdoc({ ...base, language: "en-GB" })).toContain(
      '<html lang="en-GB"',
    );
    expect(renderFrameSrcdoc({ ...base, language: 'e"n' })).toContain(
      '<html lang="en"',
    );
    expect(renderFrameSrcdoc(base)).toContain('<html class="theme-');
  });
});
