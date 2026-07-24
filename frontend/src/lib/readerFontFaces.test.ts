import { describe, expect, it } from "vitest";
import { buildAllFontFaces } from "~/lib/readerFontFaces";
import type { UserFontFamily } from "~/api/client";

describe("buildAllFontFaces", () => {
  it("derives the format hint from the path before the access token", () => {
    const family: UserFontFamily = {
      id: "user:TestFamily",
      label: "Test Family",
      category: "serif",
      files: ["Regular.ttf"],
      variable: false,
      detected: {
        regular: "Regular.ttf",
        italic: "",
        bold: "",
        boldItalic: "",
      },
    };

    expect(buildAllFontFaces([family], undefined)).toContain(
      "format('truetype')",
    );
  });

  it("uses the escaped directory name for the CSS family", () => {
    const family: UserFontFamily = {
      id: "user:O'Brien",
      label: "O'Brien",
      category: "serif",
      files: ["Regular.woff2"],
      variable: false,
      detected: {
        regular: "Regular.woff2",
        italic: "",
        bold: "",
        boldItalic: "",
      },
    };

    expect(buildAllFontFaces([family], undefined)).toContain(
      String.raw`font-family: 'O\'Brien';`,
    );
  });
});
