import { afterEach, describe, expect, it, vi } from "vitest";
import { getCachedThemeId, onAccentColor } from "~/lib/theme";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("onAccentColor", () => {
  it("supports the API's three-digit hex colors", () => {
    expect(onAccentColor("#fff")).toBe("#000000");
    expect(onAccentColor("#000")).toBe("#ffffff");
  });

  it("uses black for the Flexoki Dark accent to preserve AA contrast", () => {
    expect(onAccentColor("#4385be")).toBe("#000000");
  });

  it("falls back to white for malformed colors", () => {
    expect(onAccentColor("not-a-color")).toBe("#ffffff");
    expect(onAccentColor("")).toBe("#ffffff");
  });

  it("accepts the hash-less and padded forms the flair badge rejects", () => {
    // Shared contrast helper, deliberately different input tolerance: the
    // custom-theme API allows both spellings here, while a flair badge only
    // ever receives a canonical server color.
    expect(onAccentColor("fff")).toBe("#000000");
    expect(onAccentColor(" #fff ")).toBe("#000000");
  });

  it("keeps white as its fallback where the flair badge uses black", () => {
    expect(onAccentColor("#GGG")).toBe("#ffffff");
  });
});

describe("getCachedThemeId", () => {
  it("returns the cached id when storage is available", () => {
    vi.stubGlobal("localStorage", {
      getItem: vi.fn(() => "sepia"),
    });

    expect(getCachedThemeId()).toBe("sepia");
  });

  it("falls back when storage access throws", () => {
    vi.stubGlobal("localStorage", {
      getItem: vi.fn(() => {
        throw new DOMException("blocked", "SecurityError");
      }),
    });

    expect(getCachedThemeId()).toBe("light");
  });
});
