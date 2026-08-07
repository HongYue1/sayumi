/**
 * Suite for the flair catalogue helpers. flairTextColor delegates its contrast
 * decision to lib/themes but keeps its own input contract and default, so both
 * halves are pinned here.
 */
import { describe, expect, it } from "vitest";
import {
  DEFAULT_FLAIRS,
  findFlair,
  flairTextColor,
  getNextPaletteColor,
} from "~/lib/flairs";

describe("flairTextColor", () => {
  it("picks black on a light badge and white on a dark one", () => {
    expect(flairTextColor("#ffffff")).toBe("#000");
    expect(flairTextColor("#000000")).toBe("#fff");
  });

  it("accepts the three-digit form", () => {
    expect(flairTextColor("#fff")).toBe("#000");
    expect(flairTextColor("#000")).toBe("#fff");
  });

  it("keeps every built-in flair legible", () => {
    for (const f of DEFAULT_FLAIRS) {
      expect(flairTextColor(f.color)).toBe("#000");
    }
  });

  it("defaults to black for input it cannot read", () => {
    // Deliberately the opposite of the shell's onAccentColor, which defaults to
    // white. Both now share one contrast helper, so the asymmetry is a value
    // each caller passes in rather than an accident of two implementations.
    expect(flairTextColor("not-a-color")).toBe("#000");
    expect(flairTextColor("")).toBe("#000");
  });

  it("requires the leading hash, unlike onAccentColor", () => {
    // "000" is black, but a badge color always arrives as "#rgb"/"#rrggbb", so
    // anything else is unknown input and stays on the black default instead of
    // being coerced into a parse that would flip the text to white.
    expect(flairTextColor("000")).toBe("#000");
    expect(flairTextColor(" #ffffff ")).toBe("#000");
  });
});

describe("getNextPaletteColor", () => {
  it("cycles the palette indefinitely", () => {
    const first = getNextPaletteColor(0);
    expect(first).toMatch(/^#[0-9a-f]{6}$/);
    expect(getNextPaletteColor(1)).not.toBe(first);
    expect(getNextPaletteColor(8)).toBe(first);
  });
});

describe("findFlair", () => {
  it("finds a built-in flair", () => {
    expect(findFlair("finished", [])?.label).toBe("Finished");
  });

  it("finds a custom flair", () => {
    const custom = { id: "own", label: "Own", color: "#123456" };
    expect(findFlair("own", [custom])).toEqual(custom);
  });

  it("returns undefined without an id or a match", () => {
    expect(findFlair(undefined, [])).toBeUndefined();
    expect(findFlair("nope", [])).toBeUndefined();
  });
});
