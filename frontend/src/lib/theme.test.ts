import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getCachedThemeId, onAccentColor, themeReady } from "~/lib/theme";

// themeReady reads the two store singletons; these minimal fakes cover
// exactly what theme.ts imports them for. The other describes never touch
// the stores, so the fakes' narrow surface is all the graph needs.
const world = vi.hoisted(() => ({
  settingsLoaded: false,
  registryLoaded: false,
}));

vi.mock("~/lib/settings", () => ({
  settings: {
    get loaded() {
      return world.settingsLoaded;
    },
  },
}));

vi.mock("~/lib/customThemes", () => ({
  customThemes: {
    get loaded() {
      return world.registryLoaded;
    },
  },
}));

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

describe("themeReady", () => {
  beforeEach(() => {
    world.settingsLoaded = false;
    world.registryLoaded = false;
  });

  it("is false when neither store has loaded", () => {
    expect(themeReady()).toBe(false);
  });

  it("is false with settings loaded but the registry still out", () => {
    world.settingsLoaded = true;
    expect(themeReady()).toBe(false);
  });

  it("is false with the registry loaded but settings still out", () => {
    world.registryLoaded = true;
    expect(themeReady()).toBe(false);
  });

  it("is true only when both stores have loaded", () => {
    world.settingsLoaded = true;
    world.registryLoaded = true;
    expect(themeReady()).toBe(true);
  });
});
