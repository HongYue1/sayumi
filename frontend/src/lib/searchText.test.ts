import { describe, expect, it } from "vitest";
import {
  codePointLength,
  findFoldedCodePointRange,
  foldSearchCodePoint,
  foldSearchText,
  splitFoldedCodePointMatch,
  toCodePoints,
} from "~/lib/searchText";

describe("search text code-point doctrine", () => {
  it("folds one code point even when JavaScript lowercase expands", () => {
    expect("İ".toLowerCase()).toBe("i̇");
    expect(foldSearchCodePoint("İ")).toBe("i");
  });

  it("counts astral characters once", () => {
    expect(codePointLength("a🙂b")).toBe(3);
    expect(toCodePoints("🙂🙂")).toEqual(["🙂", "🙂"]);
  });

  it("finds and slices a folded match only at code-point boundaries", () => {
    expect(
      findFoldedCodePointRange("Lead 🙂X tail", foldSearchText("🙂x")),
    ).toEqual({ start: 5, end: 7 });
    expect(
      splitFoldedCodePointMatch("Lead 🙂X tail", foldSearchText("🙂x")),
    ).toEqual({
      before: "Lead ",
      match: "🙂X",
      after: " tail",
    });
  });

  it("returns null for empty, missing, or overlong queries", () => {
    expect(findFoldedCodePointRange("abc", foldSearchText(""))).toBeNull();
    expect(findFoldedCodePointRange("abc", foldSearchText("z"))).toBeNull();
    expect(findFoldedCodePointRange("abc", foldSearchText("abcd"))).toBeNull();
  });
});
