/**
 * Suite for the theme registry and its color math. Nothing is mocked: these are
 * pure functions over the static catalogue. Contrast is recomputed locally so an
 * assertion cannot inherit the bug it is meant to catch.
 */
import { afterEach, describe, expect, it } from "vitest";
import { createEffect, createRoot, flush } from "solid-js";
import { readFileSync } from "node:fs";
import {
  autoAccent,
  DEFAULT_THEME_ID,
  deriveSurface,
  getTheme,
  isBuiltInTheme,
  prefersBlackText,
  readableAccent,
  readerThemeVars,
  sameThemeList,
  setCustomThemes,
  THEMES,
  type ThemeDef,
  themeGroupFor,
  themeSurface,
} from "~/lib/themes";

/** WCAG contrast ratio between two hex colors, computed independently. */
function contrast(a: string, b: string): number {
  const lum = (hex: string): number => {
    const h = hex.slice(1);
    const full =
      h.length === 3
        ? h
            .split("")
            .map((d) => d + d)
            .join("")
        : h;
    const chan = (i: number): number => {
      const v = Number.parseInt(full.slice(i, i + 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * chan(0) + 0.7152 * chan(2) + 0.0722 * chan(4);
  };
  const la = lum(a);
  const lb = lum(b);
  return la > lb ? (la + 0.05) / (lb + 0.05) : (lb + 0.05) / (la + 0.05);
}

const CUSTOM: ThemeDef = {
  id: "test-custom",
  label: "Test Custom",
  group: "dark",
  bg: "#101010",
  fg: "#f0f0f0",
  accent: "#ff8800",
};

afterEach(() => {
  setCustomThemes([]);
});

describe("getTheme", () => {
  it("invalidates a tracked lookup when the custom registry changes", () => {
    const seen: string[] = [];
    const dispose = createRoot((stop) => {
      createEffect(
        () => getTheme(CUSTOM.id),
        (theme) => {
          seen.push(theme.bg);
          return undefined;
        },
      );
      return stop;
    });

    flush();
    setCustomThemes([CUSTOM]);
    flush();
    setCustomThemes([{ ...CUSTOM, bg: "#202020" }]);
    flush();
    setCustomThemes([]);
    flush();
    dispose();

    expect(seen).toEqual(["#ffffff", "#101010", "#202020", "#ffffff"]);
  });

  it("leaves a tracked lookup alone when a reload re-registers the same themes", () => {
    // A profile refresh or a load retry re-maps fresh ThemeDef objects for
    // themes that did not change. The registry is the one layer with no equals
    // option of its own, so an unconditional revision bump would re-run the
    // chrome resolver and rebuild the reader payload for nothing.
    let runs = 0;
    const dispose = createRoot((stop) => {
      createEffect(
        () => getTheme(CUSTOM.id),
        () => {
          runs += 1;
          return undefined;
        },
      );
      return stop;
    });

    flush();
    expect(runs).toBe(1);

    setCustomThemes([CUSTOM]);
    flush();
    expect(runs).toBe(2);

    setCustomThemes([{ ...CUSTOM }]);
    flush();
    expect(runs).toBe(2);

    setCustomThemes([{ ...CUSTOM, label: "Renamed" }]);
    flush();
    dispose();

    expect(runs).toBe(3);
  });

  it("resolves a built-in id", () => {
    expect(getTheme("dark").id).toBe("dark");
  });

  it("falls back to the light theme for an unknown or empty id", () => {
    // Resolved by id rather than by position, so reordering the catalogue
    // cannot silently repoint every unknown-id lookup at a different theme.
    expect(getTheme("no-such-theme").id).toBe("light");
    expect(getTheme("").id).toBe("light");
  });

  it("resolves a registered custom theme and drops it on replacement", () => {
    setCustomThemes([CUSTOM]);
    expect(getTheme(CUSTOM.id)).toEqual(CUSTOM);
    setCustomThemes([]);
    expect(getTheme(CUSTOM.id).id).toBe("light");
  });
});

describe("isBuiltInTheme", () => {
  it("is true for every catalogue id and false for anything else", () => {
    for (const t of THEMES) expect(isBuiltInTheme(t.id)).toBe(true);
    expect(isBuiltInTheme("not-a-theme")).toBe(false);
  });

  it("stays false for a custom theme even once it is registered", () => {
    setCustomThemes([CUSTOM]);
    expect(isBuiltInTheme(CUSTOM.id)).toBe(false);
  });
});

describe("the frame.css contract", () => {
  it("backs every built-in theme with a static html.theme class", () => {
    // isBuiltInTheme() promises a frame.css class and readerThemeVars() sends
    // no payload on the strength of that promise, so a theme added without a
    // matching rule would paint the reader frame unstyled. Pin both directions.
    const css = readFileSync("src/iframe/frame.css", "utf8");
    const classes = new Set(
      css
        .split("html.theme-")
        .slice(1)
        .map((part) => /^[a-z0-9-]+/.exec(part)?.[0] ?? ""),
    );
    const ids = new Set(THEMES.map((t) => t.id));
    expect([...ids].filter((id) => !classes.has(id))).toEqual([]);
    expect([...classes].filter((c) => !ids.has(c))).toEqual([]);
  });

  it("matches every built-in theme bg to its reader --bg-primary", () => {
    // The shell paints chrome with theme.bg while the frame paints the page
    // with --bg-primary; a drift splits the reader across two backgrounds.
    // The light theme rides the bare html rule, the rest html.theme-<id>.
    const css = readFileSync("src/iframe/frame.css", "utf8");
    const readerBg = (id: string): string | null => {
      const rule = new RegExp(`html\\.theme-${id}\\s*\\{([^}]*)\\}`).exec(css);
      const bg = rule && /--bg-primary:\s*([^;]+);/.exec(rule[1]);
      return bg ? bg[1].trim().toLowerCase() : null;
    };
    for (const t of THEMES) {
      expect(readerBg(t.id)).toBe(t.bg.toLowerCase());
    }
  });
});

describe("the default theme", () => {
  it("resolves to a built-in theme", () => {
    expect(isBuiltInTheme(DEFAULT_THEME_ID)).toBe(true);
    expect(getTheme(DEFAULT_THEME_ID).id).toBe(DEFAULT_THEME_ID);
  });

  it("matches app.css's first-paint tokens", () => {
    // Four sources need a default: this constant, app.css's :root tokens,
    // getCachedThemeId's fallback, and DEFAULT_USER_SETTINGS.theme. The other
    // three import the constant; the stylesheet is the one that cannot, so it
    // is pinned here. A light :root under a dark default painted a fresh
    // profile white, cached that palette, then repainted dark the moment the
    // saved settings arrived -- the flash the pre-paint cache exists to stop.
    const css = readFileSync("src/app.css", "utf8");
    const root = /:root\s*\{([\s\S]*?)\n\}/.exec(css);
    expect(root).not.toBeNull();
    const block = root?.[1] ?? "";
    const token = (name: string): string | null => {
      const m = new RegExp(`--${name}:\\s*([^;]+);`).exec(block);
      return m ? m[1].trim().toLowerCase() : null;
    };
    const theme = getTheme(DEFAULT_THEME_ID);
    expect(token("bg")).toBe(theme.bg.toLowerCase());
    expect(token("fg")).toBe(theme.fg.toLowerCase());
    expect(token("accent")).toBe(theme.accent.toLowerCase());
    // color-scheme drives the light-dark() shadows, veils and grain, so it has
    // to agree with the palette's own side.
    expect(/color-scheme:\s*(\w+);/.exec(block)?.[1]).toBe(theme.group);
  });

  it("shares one legacy-cache fallback with app.css", () => {
    // Two readers restore the pre-paint cache: the inline bootstrap in
    // index.html and applyCachedTheme (lib/theme.ts). Neither can paint
    // --elevated or --accent-ink for a cache written before those tokens
    // existed, so both leave them to :root -- which is only ONE answer while
    // the stylesheet's wash is the one deriveSurface computes. --accent-ink is
    // the knowing exception: CSS cannot run the contrast search, so the raw
    // accent stands in until the settings load paints the corrected value.
    const css = readFileSync("src/app.css", "utf8");
    const block = /:root\s*\{([\s\S]*?)\n\}/.exec(css)?.[1] ?? "";
    const washRe = /--elevated:\s*color-mix\([^;]*?([\d.]+)%[^;]*\);/;
    const wash = washRe.exec(block);
    expect(wash).not.toBeNull();
    const pct = wash?.[1] ?? "";
    // Pinned in full so the wash cannot silently swap paper and ink.
    expect(block).toContain(
      `--elevated: color-mix(in srgb, var(--fg) ${pct}%, var(--bg));`,
    );
    // Recomputed here so the assertion cannot inherit the mixer's own bug.
    const chan = Math.round(0x10 + (0xf0 - 0x10) * (Number(pct) / 100))
      .toString(16)
      .padStart(2, "0");
    expect(deriveSurface("#101010", "#f0f0f0")).toBe(`#${chan}${chan}${chan}`);
    expect(/--accent-ink:\s*var\(--accent\);/.test(block)).toBe(true);
  });
});

describe("prefersBlackText", () => {
  it("picks black on light paper and white on dark", () => {
    expect(prefersBlackText("#ffffff", false)).toBe(true);
    expect(prefersBlackText("#000000", true)).toBe(false);
  });

  it("switches at the contrast crossover, not the light/dark pivot", () => {
    // Black overtakes white as the readable ink at luminance 0.179, but
    // themeGroupFor still calls both of these night themes. That disagreement
    // is exactly what used to send mid-tone backgrounds toward white.
    expect(prefersBlackText("#808080", false)).toBe(true);
    expect(themeGroupFor("#808080")).toBe("dark");
    expect(prefersBlackText("#606060", false)).toBe(false);
  });

  it("returns the caller's fallback for a color it cannot parse", () => {
    expect(prefersBlackText("not-a-color", true)).toBe(true);
    expect(prefersBlackText("not-a-color", false)).toBe(false);
  });
});

describe("readableAccent", () => {
  it("keeps every built-in accent at AA on its own background", () => {
    for (const t of THEMES) {
      const ink = readableAccent(t.accent, t.bg);
      expect(contrast(ink, t.bg)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("reaches AA on the mid-tones that used to return bare white", () => {
    // Luminance 0.18 to 0.40: white is already under 4.5:1 here, yet the
    // light/dark pivot at 0.4 still calls these dark. Darkening is the only
    // way out, and the result must keep some hue rather than collapse.
    for (const bg of ["#787878", "#808080", "#909090", "#a0a0a0", "#a8a8a8"]) {
      const ink = readableAccent("#2563eb", bg);
      expect(ink).not.toBe("#ffffff");
      expect(contrast(ink, bg)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("returns an accent that already clears AA untouched", () => {
    expect(readableAccent("#000000", "#ffffff")).toBe("#000000");
  });

  it("passes a malformed color straight through", () => {
    expect(readableAccent("not-a-color", "#ffffff")).toBe("not-a-color");
    expect(readableAccent("#2563eb", "not-a-color")).toBe("#2563eb");
  });
});

describe("themeSurface", () => {
  it("prefers the scheme's official surface", () => {
    const dark = getTheme("dark");
    expect(dark.surface).toBeDefined();
    expect(themeSurface(dark)).toBe(dark.surface);
  });

  it("derives a wash for an officially flat scheme", () => {
    const flat = getTheme("night-owl");
    expect(flat.surface).toBeUndefined();
    expect(themeSurface(flat)).toBe(deriveSurface(flat.bg, flat.fg));
  });
});

describe("readerThemeVars", () => {
  it("sends no payload for a built-in id", () => {
    for (const t of THEMES) expect(readerThemeVars(t.id)).toBeNull();
  });

  it("sends the full token set for a custom id", () => {
    const vars = readerThemeVars(CUSTOM.id, [CUSTOM]);
    expect(vars).toContain("color-scheme: dark;");
    expect(vars).toContain("--bg-primary: #101010;");
    expect(vars).toContain("--text-primary: #f0f0f0;");
    expect(vars).toContain("--accent: #ff8800;");
  });

  it("falls back to the registry when no list is passed", () => {
    setCustomThemes([CUSTOM]);
    expect(readerThemeVars(CUSTOM.id)).toContain("--accent: #ff8800;");
  });

  it("returns null for an id it has never seen", () => {
    expect(readerThemeVars("no-such-theme")).toBeNull();
  });

  it("substitutes an auto accent for a theme stored without one", () => {
    const blank: ThemeDef = { ...CUSTOM, accent: "" };
    expect(readerThemeVars(blank.id, [blank])).toContain("--accent: #");
  });

  it("lets a built-in id win over a custom theme that collides with it", () => {
    // getTheme resolves built-ins first; the reader payload has to agree, or a
    // custom theme saved under a built-in id would send declarations that lose
    // to the static html.theme-<id> rule anyway and desync the two surfaces.
    const collide: ThemeDef = { ...CUSTOM, id: "dark" };
    setCustomThemes([collide]);
    expect(readerThemeVars("dark")).toBeNull();
    expect(readerThemeVars("dark", [collide])).toBeNull();
    expect(getTheme("dark").bg).not.toBe(CUSTOM.bg);
  });
});

describe("themeGroupFor and autoAccent", () => {
  it("groups by background lightness", () => {
    expect(themeGroupFor("#ffffff")).toBe("light");
    expect(themeGroupFor("#000000")).toBe("dark");
  });

  it("derives a concrete accent from paper and ink", () => {
    expect(autoAccent("#ffffff", "#000000")).toMatch(/^#[0-9a-f]{6}$/);
    expect(autoAccent("#101010", "#f0f0f0")).toMatch(/^#[0-9a-f]{6}$/);
  });

  it("treats a background it cannot read as light", () => {
    // luminance() answers 1 for unparseable input, so unknown paper is grouped
    // with the day themes. Pinned because nothing else exercises that policy.
    expect(themeGroupFor("not-a-color")).toBe("light");
    expect(themeGroupFor("")).toBe("light");
  });
});

describe("sameThemeList", () => {
  const base: ThemeDef = {
    id: "custom-1",
    label: "Mine",
    group: "dark",
    bg: "#101010",
    fg: "#f0f0f0",
    accent: "#8ab4f8",
  };

  it("treats field-identical lists as equal across rebuilds", () => {
    // This is the whole point of the comparator: the swatch memos allocate a
    // new array on every customThemes write, and a reload re-maps fresh
    // ThemeDef objects, so reference equality would report a change with no
    // rendered consequence and re-run every subscriber.
    expect(sameThemeList([base], [{ ...base }])).toBe(true);
    expect(sameThemeList([], [])).toBe(true);
  });

  it("reports a rename, a regroup, a recolor, or a new surface", () => {
    expect(sameThemeList([base], [{ ...base, label: "Renamed" }])).toBe(false);
    expect(sameThemeList([base], [{ ...base, group: "light" }])).toBe(false);
    expect(sameThemeList([base], [{ ...base, accent: "#ff0000" }])).toBe(false);
    expect(sameThemeList([base], [{ ...base, bg: "#000000" }])).toBe(false);
    expect(sameThemeList([base], [{ ...base, fg: "#ffffff" }])).toBe(false);
    expect(sameThemeList([base], [{ ...base, surface: "#1c1c1c" }])).toBe(
      false,
    );
  });

  it("reports added, removed, and reordered themes", () => {
    const other: ThemeDef = { ...base, id: "custom-2", label: "Other" };
    expect(sameThemeList([base], [base, other])).toBe(false);
    expect(sameThemeList([base, other], [base])).toBe(false);
    expect(sameThemeList([base, other], [other, base])).toBe(false);
  });
});
