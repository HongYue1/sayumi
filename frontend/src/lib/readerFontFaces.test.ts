import { describe, expect, it } from "vitest";
import {
  embeddedMetrics,
  type FontMetrics,
  type UserFontFamily,
} from "~/api/client";
import {
  buildAllFontFaces,
  buildReaderFontFaces,
  normalizeToReference,
} from "~/lib/readerFontFaces";

type Face = { family: string; url: string; weight: string; style: string };

/** Parses emitted CSS back into faces so each weight/style/url pairing is
 *  asserted on its own. A substring check cannot tell a 400 face from a 700
 *  one when both weights appear somewhere in the sheet. */
function parseFaces(css: string): Face[] {
  return css
    .split("@font-face")
    .slice(1)
    .map((block) => ({
      family: /font-family: ([^;]+);/.exec(block)?.[1] ?? "",
      url: /src: url\('([^']+)'\)/.exec(block)?.[1] ?? "",
      weight: /font-weight: ([^;]+);/.exec(block)?.[1] ?? "",
      style: /font-style: ([^;]+);/.exec(block)?.[1] ?? "",
    }));
}

const EMBEDDED_FACE_COUNT = parseFaces(buildReaderFontFaces()).length;

/** The faces a user family contributed, with the embedded ones dropped. */
function userFaces(css: string): Face[] {
  return parseFaces(css).slice(EMBEDDED_FACE_COUNT);
}

/** The format keyword each user face declared, in order. Asserting against the
 *  whole sheet would prove nothing: the embedded faces hardcode
 *  format('woff2') and never consult formatHint at all. */
function userFormats(css: string): string[] {
  return css
    .split("@font-face")
    .slice(1 + EMBEDDED_FACE_COUNT)
    .map((block) => /format\('([^']+)'\)/.exec(block)?.[1] ?? "");
}

function fam(over: Partial<UserFontFamily> = {}): UserFontFamily {
  return {
    id: "user:Minion",
    label: "Minion",
    category: "serif",
    files: ["Regular.otf", "Italic.otf", "Bold.otf"],
    variable: false,
    detected: {
      regular: "Regular.otf",
      italic: "Italic.otf",
      bold: "Bold.otf",
      boldItalic: "",
    },
    ...over,
  };
}

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

  it("puts the four embedded faces ahead of every user face", () => {
    expect(EMBEDDED_FACE_COUNT).toBe(4);
    expect(
      buildAllFontFaces([fam()], undefined).startsWith(buildReaderFontFaces()),
    ).toBe(true);
  });

  it("returns only the embedded faces when there are no user families", () => {
    expect(buildAllFontFaces([], undefined)).toBe(buildReaderFontFaces());
  });

  it("skips a family with no assigned or detected roles", () => {
    const bare = fam({
      id: "user:Bare",
      files: [],
      detected: { regular: "", italic: "", bold: "", boldItalic: "" },
    });

    expect(buildAllFontFaces([bare], undefined)).toBe(buildReaderFontFaces());
  });

  it("gives a variable family one full-axis face per axis and no faux bold", () => {
    // The upright file carries the whole weight axis, so a separate 700 face
    // is exactly what would make the browser synthesize a faux bold.
    const faces = userFaces(
      buildAllFontFaces([fam({ variable: true })], undefined),
    );

    expect(faces).toEqual([
      {
        family: "'Minion'",
        url: expect.stringContaining("Regular.otf"),
        weight: "100 900",
        style: "normal",
      },
      {
        family: "'Minion'",
        url: expect.stringContaining("Italic.otf"),
        weight: "100 900",
        style: "italic",
      },
    ]);
  });

  it("gives a static family one fixed-weight face per assigned role", () => {
    const faces = userFaces(buildAllFontFaces([fam()], undefined));

    expect(faces).toEqual([
      {
        family: "'Minion'",
        url: expect.stringContaining("Regular.otf"),
        weight: "400",
        style: "normal",
      },
      {
        family: "'Minion'",
        url: expect.stringContaining("Bold.otf"),
        weight: "700",
        style: "normal",
      },
      {
        family: "'Minion'",
        url: expect.stringContaining("Italic.otf"),
        weight: "400",
        style: "italic",
      },
    ]);
  });

  it("emits a bold-italic face only once that role resolves", () => {
    expect(userFaces(buildAllFontFaces([fam()], undefined))).toHaveLength(3);

    const faces = userFaces(
      buildAllFontFaces(
        [
          fam({
            detected: {
              regular: "Regular.otf",
              italic: "Italic.otf",
              bold: "Bold.otf",
              boldItalic: "BoldItalic.otf",
            },
          }),
        ],
        undefined,
      ),
    );

    expect(faces).toHaveLength(4);
    expect(faces[3]).toEqual({
      family: "'Minion'",
      url: expect.stringContaining("BoldItalic.otf"),
      weight: "700",
      style: "italic",
    });
  });

  it("prefers an explicit role override over the detected file", () => {
    const faces = userFaces(
      buildAllFontFaces([fam()], { "user:Minion": { regular: "Chosen.otf" } }),
    );

    expect(faces[0].url).toContain("Chosen.otf");
    expect(faces[0].url).not.toContain("Regular.otf");
  });

  it("falls back to detected files for roles the override leaves unset", () => {
    // The client half of the absent-not-empty contract. A role map is
    // routinely PARTIAL: SettingsPanel deletes a cleared key rather than
    // storing "", and Go's fontRoleEntry tags every role omitempty, so unset
    // roles arrive absent and must resolve to the detected file. The server
    // half is pinned by TestFontRoleEntryOmitsEmptyRoles.
    const faces = userFaces(
      buildAllFontFaces([fam()], { "user:Minion": { regular: "Chosen.otf" } }),
    );

    expect(faces).toHaveLength(3);
    expect(faces[1].url).toContain("Bold.otf");
    expect(faces[2].url).toContain("Italic.otf");
  });

  it("ignores a role map keyed for a different family", () => {
    const faces = userFaces(
      buildAllFontFaces([fam()], {
        "user:Someone Else": { regular: "Chosen.otf" },
      }),
    );

    expect(faces[0].url).toContain("Regular.otf");
    expect(faces[0].url).not.toContain("Chosen.otf");
  });

  it("emits each family in the order it was given", () => {
    const faces = userFaces(
      buildAllFontFaces(
        [fam(), fam({ id: "user:Second", label: "Second" })],
        undefined,
      ),
    );

    expect(faces.map((f) => f.family)).toEqual([
      "'Minion'",
      "'Minion'",
      "'Minion'",
      "'Second'",
      "'Second'",
      "'Second'",
    ]);
  });

  it("maps every extension the scanner accepts to a CSS format keyword", () => {
    // internal/fonts accepts exactly these four, so they are the whole
    // reachable surface of the format hint.
    const cases: Array<[string, string]> = [
      ["A.woff2", "woff2"],
      ["A.woff", "woff"],
      ["A.otf", "opentype"],
      ["A.ttf", "truetype"],
    ];

    for (const [file, hint] of cases) {
      const css = buildAllFontFaces(
        [
          fam({
            files: [file],
            detected: { regular: file, italic: "", bold: "", boldItalic: "" },
          }),
        ],
        undefined,
      );
      expect(userFormats(css)).toEqual([hint]);
    }
  });
});

// The Literata upright the client normalizes every family against.
const REFERENCE: FontMetrics = {
  unitsPerEm: 1000,
  xHeight: 0.507,
  capHeight: 0.7,
  ascent: 1.177,
  descent: 0.308,
  lineGap: 0,
};

function faceMetrics(xHeight: number): FontMetrics {
  return { ...REFERENCE, xHeight };
}

describe("normalizeToReference", () => {
  it("leaves the family alone when either side is unknown", () => {
    expect(normalizeToReference(undefined, REFERENCE)).toBe("");
    expect(normalizeToReference(faceMetrics(0.5), undefined)).toBe("");
    expect(normalizeToReference(faceMetrics(0), REFERENCE)).toBe("");
  });

  it("matches a sane face against the reference", () => {
    // Atkinson at 0.496 against Literata at 0.507: a 2.2% upscale.
    const css = normalizeToReference(faceMetrics(0.496), REFERENCE);
    expect(css).toContain("size-adjust: 102.218%;");
    expect(css).toContain("ascent-override:");
  });

  it("refuses a lying x-height instead of blowing up", () => {
    // Ovo reports 356/2048 and Rosarivo 170/1000 while inking more than
    // twice that; trusting either sized them near 300% until lines
    // overlapped. Natural size is the safe degradation.
    expect(normalizeToReference(faceMetrics(356 / 2048), REFERENCE)).toBe("");
    expect(normalizeToReference(faceMetrics(0.17), REFERENCE)).toBe("");
    expect(normalizeToReference(faceMetrics(0.217), REFERENCE)).toBe("");
  });

  it("prefers an ink measurement over lying server metrics", () => {
    // Ovo's tables claim ~0.17 (a 292% blowup) while its ink measures 1.11.
    const css = normalizeToReference(faceMetrics(356 / 2048), REFERENCE, 1.11);
    expect(css).toContain("size-adjust: 111%;");
  });

  it("measures a family the server could not read at all", () => {
    const css = normalizeToReference(undefined, REFERENCE, 1.1);
    expect(css).toContain("size-adjust: 110%;");
  });

  it("falls back to sane server metrics past a bad measurement", () => {
    const css = normalizeToReference(faceMetrics(0.496), REFERENCE, 3);
    expect(css).toContain("size-adjust: 102.218%;");
  });

  it("leaves the family alone when both sources are insane", () => {
    expect(normalizeToReference(faceMetrics(0.17), REFERENCE, 3)).toBe("");
  });

  it("needs the reference verticals even with a measurement", () => {
    expect(normalizeToReference(undefined, undefined, 1.1)).toBe("");
  });
});

describe("buildAllFontFaces with measurements", () => {
  it("sizes a metrics-less family from its ink ratio", () => {
    embeddedMetrics()["Literata-VariableFont.woff2"] = REFERENCE;
    try {
      const css = buildAllFontFaces([fam()], undefined, {
        "user:Minion": 1.1,
      });
      expect(css).toContain("size-adjust: 110%;");
    } finally {
      delete embeddedMetrics()["Literata-VariableFont.woff2"];
    }
  });
});
