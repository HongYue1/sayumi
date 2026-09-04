// Tests for the ink-measurement fallback. FontFace and canvas are stubbed:
// happy-dom has no layout engine, so every assertion runs against scripted
// ascents. The module caches the reference ink across calls, so each test
// imports a fresh copy after installing its stubs.

import { afterEach, describe, expect, it, vi } from "vitest";
import type { UserFontFamily } from "~/api/client";

let ascents: number[] = [];
let createdSources: string[] = [];
let rejectSourcesContaining = "";

function installStubs() {
  ascents = [];
  createdSources = [];
  rejectSourcesContaining = "";

  vi.stubGlobal(
    "FontFace",
    class {
      family: string;
      source: string;
      constructor(family: string, source: string) {
        this.family = family;
        this.source = source;
        createdSources.push(source);
      }
      load(): Promise<unknown> {
        if (
          rejectSourcesContaining &&
          this.source.includes(rejectSourcesContaining)
        ) {
          return Promise.reject(new Error("load failed"));
        }
        return Promise.resolve(this);
      }
    },
  );

  Object.defineProperty(document, "fonts", {
    value: {
      add() {},
      delete() {},
    },
    configurable: true,
  });

  const realCreateElement = document.createElement.bind(document);
  vi.spyOn(document, "createElement").mockImplementation(((
    tagName: string,
    options?: ElementCreationOptions,
  ) => {
    if (tagName !== "canvas") {
      return realCreateElement(tagName as keyof HTMLElementTagNameMap, options);
    }
    return {
      getContext: () => ({
        set font(_: string) {},
        measureText: () => ({
          actualBoundingBoxAscent: ascents.shift() ?? 0,
        }),
      }),
    } as unknown as HTMLCanvasElement;
  }) as typeof document.createElement);
}

async function freshMeasure() {
  vi.resetModules();
  return (await import("~/lib/fontMeasure")).measureFamilyAdjusts;
}

function fam(over: Partial<UserFontFamily> = {}): UserFontFamily {
  return {
    id: "user:Minion",
    label: "Minion",
    category: "serif",
    files: ["Regular.woff2"],
    variable: false,
    detected: {
      regular: "Regular.woff2",
      italic: "",
      bold: "",
      boldItalic: "",
    },
    ...over,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("measureFamilyAdjusts", () => {
  it("returns reference-over-face ink ratios by family id", async () => {
    installStubs();
    // The reference measures first, then each family in order.
    ascents = [50.7, 45.65];
    const measure = await freshMeasure();

    await expect(measure([fam()], undefined)).resolves.toEqual({
      "user:Minion": 50.7 / 45.65,
    });
  });

  it("measures the role override instead of the detected file", async () => {
    installStubs();
    ascents = [50.7, 50];
    const measure = await freshMeasure();

    await measure([fam()], { "user:Minion": { regular: "Chosen.woff2" } });
    expect(createdSources.some((s) => s.includes("Chosen.woff2"))).toBe(true);
    expect(createdSources.some((s) => s.includes("Regular.woff2"))).toBe(false);
  });

  it("skips a family with no regular file", async () => {
    installStubs();
    ascents = [50.7];
    const measure = await freshMeasure();

    const bare = fam({
      detected: { regular: "", italic: "", bold: "", boldItalic: "" },
    });
    await expect(measure([bare], undefined)).resolves.toEqual({});
  });

  it("yields nothing when the reference will not measure", async () => {
    installStubs();
    rejectSourcesContaining = "Literata";
    const measure = await freshMeasure();

    await expect(measure([fam()], undefined)).resolves.toEqual({});
  });

  it("skips a family that will not load", async () => {
    installStubs();
    ascents = [50.7];
    rejectSourcesContaining = "Broken.woff2";
    const measure = await freshMeasure();

    const broken = fam({
      id: "user:Broken",
      files: ["Broken.woff2"],
      detected: {
        regular: "Broken.woff2",
        italic: "",
        bold: "",
        boldItalic: "",
      },
    });
    await expect(measure([broken], undefined)).resolves.toEqual({});
  });
});
