import { afterEach, describe, expect, it, vi } from "vitest";
import { applyTheme, getCachedThemeId, onAccentColor } from "~/lib/theme";
import { DEFAULT_THEME_ID } from "~/lib/themes";

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

  it("falls back to the shared default for a fresh visitor", () => {
    vi.stubGlobal("localStorage", {
      getItem: vi.fn(() => null),
    });

    // Not a local literal: a visitor with no cache has to be painted the same
    // palette app.css draws and the saved settings default to, or applyTheme
    // caches one theme and the settings load immediately repaints another.
    expect(getCachedThemeId()).toBe(DEFAULT_THEME_ID);
  });

  it("falls back to the shared default when storage access throws", () => {
    vi.stubGlobal("localStorage", {
      getItem: vi.fn(() => {
        throw new DOMException("blocked", "SecurityError");
      }),
    });

    expect(getCachedThemeId()).toBe(DEFAULT_THEME_ID);
  });
});

describe("applyTheme for a cached custom palette", () => {
  const CACHE: Record<string, unknown> = {
    id: "custom-abc",
    bg: "#101010",
    fg: "#f0f0f0",
    accent: "#ff8800",
    accentFg: "#000000",
    scheme: "dark",
  };

  let setItem = vi.fn();

  function stubCache(cache: Record<string, unknown>): void {
    setItem = vi.fn();
    vi.stubGlobal("localStorage", {
      getItem: (key: string) =>
        key === "sayumi:theme-vars" ? JSON.stringify(cache) : String(cache.id),
      setItem,
    });
  }

  afterEach(() => {
    document.documentElement.removeAttribute("style");
    delete document.documentElement.dataset.theme;
  });

  it("leaves the derived tokens to :root for a legacy cache", () => {
    // An id the custom registry has not loaded yet paints from the cache
    // rather than flashing the fallback theme. A cache written before
    // --elevated and --accent-ink existed must not derive them here: the
    // pre-paint bootstrap in index.html leaves both to app.css :root, and one
    // cache cannot paint two ways one tick apart.
    stubCache(CACHE);

    applyTheme("custom-abc");

    const style = document.documentElement.style;
    expect(style.getPropertyValue("--bg")).toBe("#101010");
    expect(style.getPropertyValue("--accent-fg")).toBe("#000000");
    expect(style.getPropertyValue("--elevated")).toBe("");
    expect(style.getPropertyValue("--accent-ink")).toBe("");
    expect(document.documentElement.dataset.theme).toBe("custom-abc");
    // Reused, never re-cached: caching the fallback palette here would erase
    // the real one before the registry arrives.
    expect(setItem).not.toHaveBeenCalled();
  });

  it("paints the derived tokens a current cache carries", () => {
    stubCache({ ...CACHE, elevated: "#1d1d1d", accentInk: "#ffa733" });

    applyTheme("custom-abc");

    const style = document.documentElement.style;
    expect(style.getPropertyValue("--elevated")).toBe("#1d1d1d");
    expect(style.getPropertyValue("--accent-ink")).toBe("#ffa733");
  });
});
