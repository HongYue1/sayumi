// Tests for the ink-measurement fallback. FontFace and canvas are stubbed:
// happy-dom has no layout engine, so every assertion runs against scripted
// ascents. The module caches the reference ink across calls, so each test
// imports a fresh copy after installing its stubs.

import { afterEach, describe, expect, it, vi } from "vitest";
import type { FontRoleMap, UserFontFamily } from "~/api/client";

let ascents: number[] = [];
let profiles: (Record<string, number> | undefined)[] = [];
let createdSources: string[] = [];
let descriptors: FontFaceDescriptors[] = [];
let rejectSourcesContaining = "";
let loadHook: ((source: string) => Promise<unknown> | undefined) | undefined;
let bounds: "native" | "missing" | "zero" = "native";
let readback: "normal" | "blank" | "clipped" | "denied" = "normal";
let noContext = false;
let controlWidths = [45, 60, 55];
let fontsDescriptor: PropertyDescriptor | undefined;
const registered = new Set<unknown>();
const fontSet = {
  add: vi.fn((face: unknown) => registered.add(face)),
  delete: vi.fn((face: unknown) => registered.delete(face)),
};

function installStubs() {
  ascents = [];
  profiles = [];
  createdSources = [];
  descriptors = [];
  rejectSourcesContaining = "";
  loadHook = undefined;
  bounds = "native";
  readback = "normal";
  noContext = false;
  controlWidths = [45, 60, 55];
  registered.clear();
  fontSet.add.mockClear();
  fontSet.delete.mockClear();
  fontsDescriptor = Object.getOwnPropertyDescriptor(document, "fonts");

  vi.stubGlobal(
    "FontFace",
    class {
      constructor(
        readonly family: string,
        readonly source: string,
        descriptor: FontFaceDescriptors,
      ) {
        createdSources.push(source);
        descriptors.push(descriptor);
      }
      load(): Promise<unknown> {
        if (
          rejectSourcesContaining &&
          this.source.includes(rejectSourcesContaining)
        )
          return Promise.reject(new Error("load failed"));
        return loadHook?.(this.source) ?? Promise.resolve(this);
      }
    },
  );
  Object.defineProperty(document, "fonts", {
    value: fontSet,
    configurable: true,
  });
  const realCreateElement = document.createElement.bind(document);
  vi.spyOn(document, "createElement").mockImplementation(((
    tagName: string,
    options?: ElementCreationOptions,
  ) => {
    if (tagName !== "canvas")
      return realCreateElement(tagName as keyof HTMLElementTagNameMap, options);
    // One profile per loaded face, not per measureText call: generic controls
    // and coverage probes must not consume the next font's expected height.
    const height = ascents.shift() ?? 50;
    const profile = profiles.shift();
    let font = "";
    let drawn = "";
    const glyphHeight = (g: string) => profile?.[g] ?? height;
    const ctx = {
      set font(value: string) {
        font = value;
      },
      measureText(g: string) {
        const supported = !profile || profile[g] !== undefined;
        const probe = font.includes("__sayumi_measure_");
        const fallback =
          controlWidths[
            font.endsWith("monospace") ? 1 : font.endsWith("sans-serif") ? 2 : 0
          ];
        return {
          width: probe && supported ? 52 : fallback,
          actualBoundingBoxAscent:
            bounds === "missing"
              ? undefined
              : bounds === "zero"
                ? 0
                : glyphHeight(g),
        };
      },
      clearRect() {},
      fillText(g: string) {
        drawn = g;
      },
      getImageData() {
        if (readback === "denied") throw new Error("readback denied");
        const data = new Uint8ClampedArray(300 * 300 * 4);
        if (readback === "clipped") data[3] = 255;
        if (readback === "normal") {
          const y = 200 - Math.round(glyphHeight(drawn));
          data[(y * 300 + 50) * 4 + 3] = 255;
        }
        return { data };
      },
    };
    return {
      getContext: () => (noContext ? null : ctx),
    } as unknown as HTMLCanvasElement;
  }) as typeof document.createElement);
}

async function drain() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

function deferred() {
  let resolve!: (value: unknown) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<unknown>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
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
  vi.useRealTimers();
  if (fontsDescriptor)
    Object.defineProperty(document, "fonts", fontsDescriptor);
  else Reflect.deleteProperty(document, "fonts");
  expect(registered.size).toBe(0);
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

  it("retries a reference that failed before connectivity recovered", async () => {
    installStubs();
    rejectSourcesContaining = "Literata";
    const measure = await freshMeasure();
    await expect(measure([fam()], undefined)).resolves.toEqual({});

    rejectSourcesContaining = "";
    ascents = [50.7, 45.65];
    await expect(measure([fam()], undefined)).resolves.toEqual({
      "user:Minion": 50.7 / 45.65,
    });
  });

  it("does not load the reference for an empty request", async () => {
    installStubs();
    const measure = await freshMeasure();
    await expect(measure([], undefined)).resolves.toEqual({});
    expect(createdSources).toEqual([]);
  });

  it("degrades without a window instead of rejecting the caller", async () => {
    installStubs();
    const measure = await freshMeasure();
    vi.stubGlobal("window", undefined);
    await expect(measure([fam()], undefined)).resolves.toEqual({});
  });

  it("does not publish an extreme ratio as a successful calibration", async () => {
    installStubs();
    ascents = [50.7, 1];
    const measure = await freshMeasure();
    await expect(measure([fam()], undefined)).resolves.toEqual({});
  });

  it.each([
    ["missing x", { z: 40, v: 40, w: 40 }, 1.25],
    ["caps only", { H: 40, I: 40, E: 40, F: 40 }, 1.25],
    ["no shared letters", {}, undefined],
    ["one shared letter", { x: 40 }, undefined],
    ["one misleading outline", { x: 25, z: 40, v: 40, w: 40 }, 1.25],
    ["conflicting outlines", { x: 50, z: 50, v: 35.714, w: 35.714 }, undefined],
    [
      "extreme lowercase with normal caps",
      { x: 10, z: 10, v: 10, w: 10, H: 50, I: 50, E: 50, F: 50 },
      undefined,
    ],
  ] as const)(
    "handles %s without measuring a substitute",
    async (_, profile, ratio) => {
      installStubs();
      profiles = [undefined, profile];
      const measure = await freshMeasure();
      expect(await measure([fam()], undefined)).toEqual(
        ratio === undefined ? {} : { "user:Minion": ratio },
      );
      expect(fontSet.add).toHaveBeenCalledTimes(2);
      expect(fontSet.delete.mock.calls).toEqual(fontSet.add.mock.calls);
    },
  );

  it.each(["missing", "zero"] as const)(
    "rasterizes when extended bounds are %s",
    async (kind) => {
      installStubs();
      ascents = [50, 40];
      bounds = kind;
      const measure = await freshMeasure();
      expect(await measure([fam()], undefined)).toEqual({
        "user:Minion": 1.25,
      });
    },
  );

  it.each(["blank", "clipped", "denied"] as const)(
    "declines %s pixel readback",
    async (kind) => {
      installStubs();
      bounds = "missing";
      readback = kind;
      const measure = await freshMeasure();
      expect(await measure([fam()], undefined)).toEqual({});
      expect(fontSet.delete.mock.calls).toEqual(fontSet.add.mock.calls);
    },
  );

  it("declines inconclusive generic controls", async () => {
    installStubs();
    controlWidths = [60, 60, 60];
    const measure = await freshMeasure();
    expect(await measure([fam()], undefined)).toEqual({});
  });

  it("tries a third control when the first two generic advances coincide", async () => {
    installStubs();
    controlWidths = [60, 60, 55];
    const measure = await freshMeasure();
    expect(await measure([fam()], undefined)).toEqual({ "user:Minion": 1 });
  });

  it("retries after a context becomes available", async () => {
    installStubs();
    noContext = true;
    const measure = await freshMeasure();
    expect(await measure([fam()], undefined)).toEqual({});
    noContext = false;
    expect(await measure([fam()], undefined)).toEqual({ "user:Minion": 1 });
  });

  it("uses the same static/variable weight descriptors as reading", async () => {
    installStubs();
    const measure = await freshMeasure();
    await measure(
      [fam(), fam({ id: "user:Variable", variable: true })],
      undefined,
    );
    expect(descriptors.map((d) => d.weight)).toEqual([
      "100 900",
      "400",
      "100 900",
    ]);
    expect(descriptors.every((d) => d.style === "normal")).toBe(true);
  });

  it("snapshots role selection before the reference resolves", async () => {
    installStubs();
    const gate = deferred();
    loadHook = (source) =>
      source.includes("Literata") ? gate.promise : undefined;
    const measure = await freshMeasure();
    const roles: Record<string, FontRoleMap> = {
      "user:Minion": { regular: "Chosen.woff2" },
    };
    const pending = measure([fam()], roles);
    roles["user:Minion"].regular = "Changed.woff2";
    gate.resolve({});
    expect(await pending).toEqual({ "user:Minion": 1 });
    expect(createdSources.at(-1)).toContain("Chosen.woff2");
    expect(createdSources.at(-1)).not.toContain("Changed.woff2");
    expect(createdSources.at(-1)).toMatch(/\?token=[^&]*"\)$/);
  });

  it("does not probe an explicitly cleared regular role", async () => {
    installStubs();
    const measure = await freshMeasure();
    expect(await measure([fam()], { "user:Minion": { regular: "" } })).toEqual(
      {},
    );
    expect(createdSources).toEqual([]);
  });

  it("caches successes by registry object and file, not family id alone", async () => {
    installStubs();
    const measure = await freshMeasure();
    const family = fam();
    await measure([family], undefined);
    await measure([family], undefined);
    expect(createdSources).toHaveLength(2);
    await measure([family], { "user:Minion": { regular: "Chosen.woff2" } });
    expect(createdSources).toHaveLength(3);
    await measure([fam()], undefined);
    expect(createdSources).toHaveLength(4);
  });

  it("bounds cached files and retries failed family loads", async () => {
    installStubs();
    const measure = await freshMeasure();
    const family = fam();
    rejectSourcesContaining = "Regular";
    expect(await measure([family], undefined)).toEqual({});
    rejectSourcesContaining = "";
    expect(await measure([family], undefined)).toEqual({ "user:Minion": 1 });
    for (let i = 0; i < 9; i++)
      await measure([family], { "user:Minion": { regular: `File${i}.woff2` } });
    const before = createdSources.length;
    await measure([family], undefined);
    expect(createdSources).toHaveLength(before + 1);
  });

  it("times out a reference, observes late failure, and retries", async () => {
    installStubs();
    vi.useFakeTimers();
    const gate = deferred();
    loadHook = () => gate.promise;
    const measure = await freshMeasure();
    const pending = measure([fam()], undefined);
    await vi.advanceTimersByTimeAsync(5001);
    expect(await pending).toEqual({});
    gate.reject(new Error("late network failure"));
    await drain();
    expect(fontSet.add).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    loadHook = undefined;
    expect(await measure([fam()], undefined)).toEqual({ "user:Minion": 1 });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("times out a user load without registering a late face", async () => {
    installStubs();
    vi.useFakeTimers();
    const gate = deferred();
    loadHook = (source) =>
      source.includes("/fonts/user/") ? gate.promise : undefined;
    const measure = await freshMeasure();
    const pending = measure([fam()], undefined);
    await drain();
    await vi.advanceTimersByTimeAsync(5001);
    expect(await pending).toEqual({});
    gate.resolve({});
    await drain();
    expect(fontSet.add).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("bounds concurrent loads and cancels the remaining catalogue", async () => {
    installStubs();
    vi.useFakeTimers();
    const gate = deferred();
    loadHook = (source) =>
      source.includes("/fonts/user/") ? gate.promise : undefined;
    const measure = await freshMeasure();
    const controller = new AbortController();
    const pending = measure(
      Array.from({ length: 12 }, (_, i) => fam({ id: `user:Face${i}` })),
      undefined,
      controller.signal,
    );
    await drain();
    expect(
      createdSources.filter((s) => s.includes("/fonts/user/")),
    ).toHaveLength(4);
    controller.abort();
    expect(await pending).toEqual({});
    gate.resolve({});
    await drain();
    expect(fontSet.add).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("shares reference work without one caller cancelling another", async () => {
    installStubs();
    const gate = deferred();
    loadHook = (source) =>
      source.includes("Literata") ? gate.promise : undefined;
    const measure = await freshMeasure();
    const controller = new AbortController();
    const first = measure([fam()], undefined, controller.signal);
    const second = measure([fam({ id: "user:Second" })], undefined);
    controller.abort();
    expect(await first).toEqual({});
    gate.resolve({});
    expect(await second).toEqual({ "user:Second": 1 });
    expect(createdSources.filter((s) => s.includes("Literata"))).toHaveLength(
      1,
    );
  });

  it.each(["document", "FontFace"])("degrades without %s", async (name) => {
    installStubs();
    const measure = await freshMeasure();
    vi.stubGlobal(name, undefined);
    expect(await measure([fam()], undefined)).toEqual({});
  });

  it("degrades without a font set", async () => {
    installStubs();
    const measure = await freshMeasure();
    Object.defineProperty(document, "fonts", {
      value: undefined,
      configurable: true,
    });
    expect(await measure([fam()], undefined)).toEqual({});
  });

  it("observes failure even when starting the load synchronously aborts", async () => {
    installStubs();
    const controller = new AbortController();
    loadHook = (source) => {
      if (!source.includes("/fonts/user/")) return undefined;
      controller.abort();
      return Promise.reject(new Error("aborted native load"));
    };
    const measure = await freshMeasure();
    expect(await measure([fam()], undefined, controller.signal)).toEqual({});
    await drain();
    expect(fontSet.add).toHaveBeenCalledTimes(1);
  });

  it("does no work for a pre-aborted caller", async () => {
    installStubs();
    const measure = await freshMeasure();
    const controller = new AbortController();
    controller.abort();
    expect(await measure([fam()], undefined, controller.signal)).toEqual({});
    expect(createdSources).toEqual([]);
  });
});
