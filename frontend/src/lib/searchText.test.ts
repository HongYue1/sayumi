import { describe, expect, it } from "vitest";
import {
  codePointLength,
  findCaseInsensitiveCodePointRange,
  findFoldedCodePointRange,
  foldSearchCodePoint,
  foldSearchText,
  splitCaseInsensitiveCodePointMatch,
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
    expect(findCaseInsensitiveCodePointRange("Lead 🙂X tail", "🙂x")).toEqual({
      start: 5,
      end: 7,
    });
    expect(
      findFoldedCodePointRange("Lead 🙂X tail", foldSearchText("🙂x")),
    ).toEqual({ start: 5, end: 7 });
    expect(splitCaseInsensitiveCodePointMatch("Lead 🙂X tail", "🙂x")).toEqual({
      before: "Lead ",
      match: "🙂X",
      after: " tail",
    });
  });

  it("returns null for empty, missing, or overlong queries", () => {
    expect(findCaseInsensitiveCodePointRange("abc", "")).toBeNull();
    expect(findCaseInsensitiveCodePointRange("abc", "z")).toBeNull();
    expect(findCaseInsensitiveCodePointRange("abc", "abcd")).toBeNull();
  });
});
