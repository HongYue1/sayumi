import { describe, expect, it } from "vitest";
import { normalizeLangTag } from "~/lib/langTag";

describe("normalizeLangTag", () => {
  it("passes BCP-47 tags through", () => {
    expect(normalizeLangTag("en-GB")).toBe("en-GB");
    expect(normalizeLangTag("zh-Hant")).toBe("zh-Hant");
  });

  it("folds the metadata underscore to the BCP-47 hyphen", () => {
    // Stripping alone would fuse this into "zhHant", a subtag that matches
    // nothing; the hyphen keeps language and script matchable.
    expect(normalizeLangTag("zh_Hant")).toBe("zh-Hant");
  });

  it("strips what cannot appear in a tag, and trims edge hyphens", () => {
    expect(normalizeLangTag('e"n')).toBe("en");
    expect(normalizeLangTag("_en_")).toBe("en");
    expect(normalizeLangTag("")).toBe("");
    expect(normalizeLangTag(null)).toBe("");
    expect(normalizeLangTag(undefined)).toBe("");
  });

  it("caps the length the DOM ever sees", () => {
    expect(normalizeLangTag("a".repeat(100))).toHaveLength(35);
  });
});
