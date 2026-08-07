// Data-integrity suite for the icon geometry table. icons.ts is pure data, so
// what needs pinning is not what it does but what it is allowed to contain.
//
// The load-bearing invariant: Icon.tsx concatenates these attribute names and
// values straight into innerHTML with no escaping. One double quote in a value
// breaks out of its attribute and injects a new one. That was measured, not
// assumed -- feeding markup() a d of  M0 0" data-injected="yes  renders a path
// carrying a readable data-injected attribute. The header used to credit a
// generator with asserting this. No generator exists: glyphs are copied in by
// hand off lucide.dev, and neither tsc, oxlint nor the build inspects string
// contents. This suite is that assertion.
//
// Deliberately no assertion on the glyph or node COUNT. Those numbers were
// both wrong in the header -- it claimed 74 nodes against an actual 80, and 27
// glyphs against an actual 28 -- precisely because a count rots the moment an
// icon is added.

import { describe, expect, it } from "vitest";
import * as icons from "~/lib/icons";

// Every element that means something inside the 24x24 shell Icon.tsx renders.
const SHAPES = new Set([
  "circle",
  "ellipse",
  "line",
  "path",
  "polygon",
  "polyline",
  "rect",
]);

// Characters that would let a name or value escape the attribute pair that
// Icon.tsx builds by concatenation.
//
// BOTH quote characters are rejected, not just the double quote Icon.tsx
// currently delimits with. A mutation swapping markup() to single quotes
// survives this entire suite, because the DOM re-serialises innerHTML back to
// double quotes -- no assertion on rendered output can observe which delimiter
// was emitted. Rejecting both makes that unobservable difference harmless
// instead of a silent desync between the delimiter and its guard.
const BREAKOUT = /["'<>&]/;

const exported: Array<[string, unknown]> = Object.entries(icons);

function isPair(v: unknown): v is [string, Record<string, unknown>] {
  return (
    Array.isArray(v) &&
    v.length === 2 &&
    typeof v[0] === "string" &&
    typeof v[1] === "object" &&
    v[1] !== null
  );
}

describe("icon geometry", () => {
  it("exports glyphs at all", () => {
    expect(exported.length).toBeGreaterThan(0);
  });

  it("every export is a non-empty node list", () => {
    const bad = exported
      .filter(([, v]) => !Array.isArray(v) || v.length === 0)
      .map(([name]) => name);
    expect(bad).toEqual([]);
  });

  it("every node is a tag/attrs pair using a known SVG shape", () => {
    const bad: string[] = [];
    for (const [name, value] of exported) {
      if (!Array.isArray(value)) continue;
      for (const entry of value) {
        if (!isPair(entry)) {
          bad.push(name + " has a malformed node");
          continue;
        }
        if (!SHAPES.has(entry[0])) {
          bad.push(name + " uses unknown tag " + entry[0]);
        }
      }
    }
    expect(bad).toEqual([]);
  });

  it("no attribute can break out of the markup Icon.tsx builds", () => {
    const offenders: string[] = [];
    for (const [name, value] of exported) {
      if (!Array.isArray(value)) continue;
      for (const entry of value) {
        if (!isPair(entry)) continue;
        for (const [attr, raw] of Object.entries(entry[1])) {
          if (typeof raw !== "string") {
            offenders.push(name + " " + attr + " is not a string");
            continue;
          }
          if (BREAKOUT.test(attr) || BREAKOUT.test(raw)) {
            offenders.push(name + " <" + entry[0] + "> " + attr);
          }
        }
      }
    }
    expect(offenders).toEqual([]);
  });

  it("Tag is the only glyph that carries its own fill", () => {
    // Icon.tsx's shell sets fill=none. If a second glyph starts overriding it,
    // either that glyph or the shell default is wrong.
    const filled: string[] = [];
    for (const [name, value] of exported) {
      if (!Array.isArray(value)) continue;
      for (const entry of value) {
        if (isPair(entry) && "fill" in entry[1]) {
          filled.push(name);
          break;
        }
      }
    }
    expect(filled).toEqual(["Tag"]);
  });

  it("Tag's dot fills with currentColor so it follows the theme", () => {
    const dot = icons.Tag.find(([tag]) => tag === "circle");
    expect(dot).toBeDefined();
    expect(dot?.[1].fill).toBe("currentColor");
  });
});
