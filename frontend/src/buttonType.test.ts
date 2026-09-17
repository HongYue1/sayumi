import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// A <button> without a type attribute is a submit button. Every button in the
// app happens to sit outside a <form>, where that default is inert -- which is
// why the omissions stayed invisible -- but a panel or dialog that later grows
// a form would start submitting it from every icon click inside it.
//
// oxlint cannot guard the convention: button-has-type ships only in its react
// plugin, and enabling that plugin over this codebase reports every Solid JSX
// element as missing React. So the suite enforces it instead.

function componentFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return componentFiles(path);
    const isComponent =
      entry.isFile() &&
      entry.name.endsWith(".tsx") &&
      !entry.name.endsWith(".test.tsx");
    return isComponent ? [path] : [];
  });
}

// An attribute list ends at the first ">" that is outside both JSX expressions
// and quoted values: an onClick={() => close()} arrow puts a ">" in the middle
// of the tag, so the plain /<button[^>]*>/ scan that suffices for simpler tags
// would cut this one short and miss a type declared after the handler.
function openingTag(source: string, start: number): string {
  let depth = 0;
  let quote = "";
  for (let i = start; i < source.length; i++) {
    const char = source.charAt(i);
    if (quote !== "") {
      if (char === quote) quote = "";
    } else if (char === '"' || char === "'") {
      quote = char;
    } else if (char === "{") {
      depth += 1;
    } else if (char === "}") {
      depth -= 1;
    } else if (char === ">" && depth === 0) {
      return source.slice(start, i + 1);
    }
  }
  return source.slice(start);
}

describe("button types", () => {
  it("declares an explicit type on every component button", () => {
    const tags = componentFiles(join(process.cwd(), "src")).flatMap((path) => {
      const source = readFileSync(path, "utf8");
      return Array.from(source.matchAll(/<button\b/gu), (match) => ({
        path,
        tag: openingTag(source, match.index),
      }));
    });

    // A floor rather than a census: it catches a walk that has stopped finding
    // buttons, while adding or removing one is not supposed to touch this test.
    expect(tags.length).toBeGreaterThan(100);
    for (const { path, tag } of tags) {
      expect(tag, `${path}\n${tag}`).toMatch(/type="(?:button|submit|reset)"/u);
    }
  });
});
