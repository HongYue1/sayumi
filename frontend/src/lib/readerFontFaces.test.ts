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
});
