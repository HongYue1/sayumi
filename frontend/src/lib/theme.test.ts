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
