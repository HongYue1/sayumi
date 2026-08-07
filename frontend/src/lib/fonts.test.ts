import { describe, expect, it } from "vitest";
import { getFontById, getFontFamily, READER_FONTS } from "~/lib/fonts";

describe("reader font catalogue", () => {
  it("lists only the two families embedded in the binary", () => {
    // Everything else is a drop-in ./Fonts/ family surfaced through the user
    // font registry, never through this constant.
    expect(READER_FONTS.map((f) => f.id)).toEqual([
      "literata",
      "atkinson-next",
    ]);
  });

  it("resolves a known id to its catalogue entry", () => {
    expect(getFontById("atkinson-next")?.label).toBe(
      "Atkinson Hyperlegible Next",
    );
  });

  it("returns undefined for an id outside the catalogue", () => {
    expect(getFontById("user:MinionPro")).toBeUndefined();
  });

  it("maps every catalogue id to its own family", () => {
    for (const font of READER_FONTS) {
      expect(getFontFamily(font.id)).toBe(font.family);
    }
  });

  it("falls back to the first family for an unknown id", () => {
    // A stored setting can name a family that no longer exists, e.g. a user
    // font whose ./Fonts/ folder was deleted between sessions. The reader
    // still needs a usable CSS value rather than undefined.
    expect(getFontFamily("user:Deleted")).toBe(READER_FONTS[0].family);
    expect(getFontFamily("")).toBe(READER_FONTS[0].family);
  });

  it("gives every entry a quoted name and a matching generic fallback", () => {
    for (const font of READER_FONTS) {
      expect(font.family.startsWith("'")).toBe(true);
      expect(font.family.endsWith(font.category)).toBe(true);
    }
  });
});
