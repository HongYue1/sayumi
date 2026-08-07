import { beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import type { UserFontFamily } from "~/api/client";

const mocks = vi.hoisted(() => ({
  getFonts: vi.fn<() => Promise<UserFontFamily[]>>(),
  rescanFonts: vi.fn<() => Promise<UserFontFamily[]>>(),
}));

vi.mock("~/api/client", () => mocks);

const {
  FontRegistry,
  isUserFamilyId,
  userFamilyCSSName,
  userFamilyCSSValue,
  userFamilyDir,
} = await import("~/lib/fontRegistry");

function family(id: string): UserFontFamily {
  return {
    id,
    label: id,
    category: "sans-serif",
    files: ["Regular.woff2"],
    variable: false,
    detected: {
      regular: "Regular.woff2",
      italic: "",
      bold: "",
      boldItalic: "",
    },
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("font registry", () => {
  // Each case builds its own registry rather than resetting the singleton:
  // the read surface is store-backed and readonly, so `families = []` is a
  // compile error now.
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("serializes a rescan behind an in-flight initial load", async () => {
    const initial = deferred<UserFontFamily[]>();
    const stale = family("user:Stale");
    const fresh = family("user:Fresh");
    mocks.getFonts.mockReturnValueOnce(initial.promise);
    mocks.rescanFonts.mockResolvedValueOnce([fresh]);

    const registry = new FontRegistry();
    const load = registry.load();
    await vi.waitFor(() => expect(mocks.getFonts).toHaveBeenCalledTimes(1));
    const rescan = registry.rescan();
    expect(mocks.rescanFonts).not.toHaveBeenCalled();

    initial.resolve([stale]);
    await load;
    await expect(rescan).resolves.toBe(true);

    flush();
    expect(mocks.rescanFonts).toHaveBeenCalledTimes(1);
    expect([...registry.families]).toEqual([fresh]);
  });

  it("deduplicates concurrent initial loads", async () => {
    const initial = deferred<UserFontFamily[]>();
    mocks.getFonts.mockReturnValueOnce(initial.promise);

    const registry = new FontRegistry();
    const first = registry.load();
    const second = registry.load();
    await vi.waitFor(() => expect(mocks.getFonts).toHaveBeenCalledTimes(1));
    initial.resolve([]);
    await Promise.all([first, second]);

    expect(mocks.getFonts).toHaveBeenCalledTimes(1);
  });

  it("escapes quotes, backslashes, and controls in CSS family names", () => {
    const id = "user:O'Brien\\\nFont";
    const cssName = String.raw`'O\'Brien\\\a Font'`;

    expect(userFamilyCSSName(id)).toBe(cssName);
    expect(userFamilyCSSValue(family(id))).toBe(`${cssName}, sans-serif`);
  });

  it("recognizes a user family id and unwraps its directory name", () => {
    expect(isUserFamilyId("user:Minion")).toBe(true);
    expect(isUserFamilyId("literata")).toBe(false);
    expect(userFamilyDir("user:Minion")).toBe("Minion");
  });

  it("reports not-loaded until a load resolves", async () => {
    mocks.getFonts.mockResolvedValueOnce([family("user:A")]);

    const registry = new FontRegistry();
    expect(registry.loaded).toBe(false);

    await registry.load();
    flush();

    expect(registry.loaded).toBe(true);
  });

  it("stays unloaded after a failed load so the next call retries", async () => {
    // The silent catch keeps a fonts outage from breaking the reader, but it
    // must not latch: leaving `loaded` false is what lets the next caller
    // re-request instead of inheriting an empty list for the whole session.
    mocks.getFonts.mockRejectedValueOnce(new Error("offline"));
    mocks.getFonts.mockResolvedValueOnce([family("user:Late")]);

    const registry = new FontRegistry();
    await registry.load();
    flush();
    expect(registry.loaded).toBe(false);
    expect([...registry.families]).toEqual([]);

    await registry.load();
    flush();

    expect(registry.loaded).toBe(true);
    expect([...registry.families]).toEqual([family("user:Late")]);
    expect(mocks.getFonts).toHaveBeenCalledTimes(2);
  });

  it("keeps the previous list when a rescan fails", async () => {
    const kept = family("user:Kept");
    mocks.getFonts.mockResolvedValueOnce([kept]);
    mocks.rescanFonts.mockRejectedValueOnce(new Error("scan failed"));

    const registry = new FontRegistry();
    await registry.load();
    await expect(registry.rescan()).resolves.toBe(false);
    flush();

    expect([...registry.families]).toEqual([kept]);
    expect(registry.loaded).toBe(true);
  });

  it("resolves a known id to its entry and its CSS value", async () => {
    const minion = family("user:Minion");
    const spectral = family("user:Spectral");
    mocks.getFonts.mockResolvedValueOnce([minion, spectral]);

    const registry = new FontRegistry();
    await registry.load();
    flush();

    // Two families, and the lookup targets the second: against a one-entry
    // registry a get() that ignored its argument and always handed back
    // families[0] would pass just as happily.
    expect(registry.get("user:Spectral")).toEqual(spectral);
    expect(registry.cssValue("user:Spectral")).toBe("'Spectral', sans-serif");
  });

  it("returns undefined and null for an id it does not know", async () => {
    // cssValue answers null rather than a bare generic so callers can tell an
    // id that no longer resolves from one that does. Asked of a POPULATED
    // registry: against an empty one, a lookup that ignored the id entirely
    // would return undefined for the right answer for the wrong reason.
    mocks.getFonts.mockResolvedValueOnce([family("user:Minion")]);

    const registry = new FontRegistry();
    await registry.load();
    flush();

    expect(registry.get("user:Ghost")).toBeUndefined();
    expect(registry.cssValue("user:Ghost")).toBeNull();
  });
});
