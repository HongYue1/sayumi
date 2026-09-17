import { readFileSync } from "node:fs";
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

describe("offset unit shared with internal/epub/search.go", () => {
  // The unit is a cross-language contract with no shared type: the Go search
  // emits RUNE offsets (charOffset / snippetStart / snippetLen) and this
  // module is how the frontend consumes them. The frontend half is pinned
  // above, but the Go half could move to byte offsets without a single
  // frontend test failing -- and then one astral character earlier in a
  // chapter would slide every <mark> by a unit per surrogate pair, with a
  // boundary inside a pair rendering a lone surrogate. Read the Go source the
  // way flairs.test.ts and profileName.test.ts do.
  const go = readFileSync("../internal/epub/search.go", "utf8");

  it("measures the query and the match length in runes", () => {
    expect(go).toContain("qLen := utf8.RuneCountInString(query)");
    expect(go).toMatch(/MatchLen:\s+qLen,/u);
    expect(go).toMatch(/SnippetLen:\s+qLen,/u);
  });

  it("slices the snippet and its start offset in runes", () => {
    expect(go).toContain("origRunes = []rune(orig)");
    expect(go).toContain("snippet := string(origRunes[snippetFrom:snippetTo])");
    expect(go).toContain("snippetStart := matchStart - snippetFrom");
    expect(go).toMatch(/SnippetStart:\s+snippetStart,/u);
  });

  it("advances the match position in runes", () => {
    expect(go).toContain(
      "runePos += utf8.RuneCountInString(textLower[bytePos:matchByteStart])",
    );
    expect(go).toContain("matchStart := runePos");
    expect(go).toMatch(/CharOffset:\s+matchStart,/u);
  });
});
