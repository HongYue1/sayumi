import { beforeEach, describe, expect, it, vi } from "vitest";
import type { UserFontFamily } from "~/api/client";

const mocks = vi.hoisted(() => ({
  getFonts: vi.fn<() => Promise<UserFontFamily[]>>(),
  rescanFonts: vi.fn<() => Promise<UserFontFamily[]>>(),
}));

vi.mock("~/api/client", () => mocks);

const { fontRegistry, userFamilyCSSName, userFamilyCSSValue } =
  await import("~/lib/fontRegistry.svelte");

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
  beforeEach(() => {
    vi.resetAllMocks();
    fontRegistry.families = [];
    fontRegistry.loaded = false;
  });

  it("serializes a rescan behind an in-flight initial load", async () => {
    const initial = deferred<UserFontFamily[]>();
    const stale = family("user:Stale");
    const fresh = family("user:Fresh");
    mocks.getFonts.mockReturnValueOnce(initial.promise);
    mocks.rescanFonts.mockResolvedValueOnce([fresh]);

    const load = fontRegistry.load();
    await vi.waitFor(() => expect(mocks.getFonts).toHaveBeenCalledTimes(1));
    const rescan = fontRegistry.rescan();
    expect(mocks.rescanFonts).not.toHaveBeenCalled();

    initial.resolve([stale]);
    await load;
    await expect(rescan).resolves.toBe(true);

    expect(mocks.rescanFonts).toHaveBeenCalledTimes(1);
    expect(fontRegistry.families).toEqual([fresh]);
  });

  it("deduplicates concurrent initial loads", async () => {
    const initial = deferred<UserFontFamily[]>();
    mocks.getFonts.mockReturnValueOnce(initial.promise);

    const first = fontRegistry.load();
    const second = fontRegistry.load();
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
});
