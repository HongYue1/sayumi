import {
  DEFAULT_THEME_ID,
  getTheme,
  themeSurface,
  prefersBlackText,
  readableAccent,
  type ThemeDef,
} from "~/lib/themes";

/**
 * Picks the higher-contrast pure black or white for text/icons sitting on the
 * accent color. Accepts both hex forms allowed by the custom-theme API and
 * falls back to white for malformed colors. The contrast decision itself lives
 * in lib/themes so this and the flair badge cannot drift apart.
 */
export function onAccentColor(hex: string): string {
  return prefersBlackText(hex, false) ? "#000000" : "#ffffff";
}

/** Reads the pre-paint theme cache without letting blocked storage break boot. */
export function getCachedThemeId(fallback = DEFAULT_THEME_ID): string {
  try {
    return localStorage.getItem("sayumi:theme") ?? fallback;
  } catch {
    return fallback;
  }
}

/**
 * Applies a previously cached palette (written by applyTheme below) for an id
 * that no longer resolves to a known theme, without re-caching it. Returns
 * false when there's no cache entry for that exact id, so the caller can fall
 * back to the normal resolution path.
 */
function applyCachedTheme(id: string): boolean {
  let raw: string | null;
  try {
    raw = localStorage.getItem("sayumi:theme-vars");
  } catch {
    return false;
  }
  if (!raw) return false;
  let cached: unknown;
  try {
    cached = JSON.parse(raw);
  } catch {
    return false;
  }
  if (typeof cached !== "object" || cached === null) return false;
  const v = cached as Record<string, unknown>;
  if (
    v.id !== id ||
    typeof v.bg !== "string" ||
    typeof v.fg !== "string" ||
    typeof v.accent !== "string" ||
    typeof v.accentFg !== "string" ||
    // colorScheme is a real CSS property, not a custom one: a value outside
    // the two paintTheme can write is not a palette this cache ever held, and
    // painting it would leave the controls and scrollbars on the opposite
    // scheme from the tokens above. Refuse the entry whole instead.
    (v.scheme !== "light" && v.scheme !== "dark")
  ) {
    return false;
  }
  const root = document.documentElement;
  root.style.setProperty("--bg", v.bg);
  root.style.setProperty("--fg", v.fg);
  root.style.setProperty("--accent", v.accent);
  root.style.setProperty("--accent-fg", v.accentFg);
  // Older caches predate the elevated-surface and accent-ink tokens. Leave
  // them unset instead of deriving them here: app.css :root defines both in
  // terms of --bg / --fg / --accent, which the properties above have just
  // overridden, so CSS resolves them from this very palette. Deriving would
  // give one cache two answers, because the pre-paint bootstrap in index.html
  // cannot run the contrast search and leans on those same :root fallbacks.
  // Either way the gap lasts one load: the next applyTheme caches all of them.
  if (typeof v.elevated === "string") {
    root.style.setProperty("--elevated", v.elevated);
  }
  if (typeof v.accentInk === "string") {
    root.style.setProperty("--accent-ink", v.accentInk);
  }
  root.style.colorScheme = v.scheme;
  root.dataset.theme = id;
  return true;
}

/**
 * Paints one palette's tokens onto the document root and hands back the derived
 * tokens the caller may want to cache. Shared by applyTheme and previewTheme so
 * a live preview can never drift from what saving that theme actually paints.
 */
function paintTheme(t: ThemeDef): {
  accentFg: string;
  elevated: string;
  accentInk: string;
  scheme: "light" | "dark";
} {
  const accentFg = onAccentColor(t.accent);
  const scheme = t.group === "dark" ? "dark" : "light";
  const elevated = themeSurface(t);
  // Text-safe accent: several official palettes (ayu light, solarized light,
  // rosé pine dawn) tune their accent for fills, not 4.5:1 text on paper.
  const accentInk = readableAccent(t.accent, t.bg);
  const root = document.documentElement;
  root.style.setProperty("--bg", t.bg);
  root.style.setProperty("--fg", t.fg);
  root.style.setProperty("--accent", t.accent);
  root.style.setProperty("--accent-fg", accentFg);
  root.style.setProperty("--elevated", elevated);
  root.style.setProperty("--accent-ink", accentInk);
  root.style.colorScheme = scheme;
  root.dataset.theme = t.id;
  return { accentFg, elevated, accentInk, scheme };
}

/**
 * Paints an UNSAVED palette -- the custom-theme dialog's draft, published
 * through lib/themePreview.ts -- without touching the pre-paint cache. Skipping
 * the cache is the whole point: a tab closed mid-edit must reopen on the saved
 * theme, never on a palette the user never created.
 */
export function previewTheme(def: ThemeDef): void {
  paintTheme(def);
}

/**
 * Applies a theme's tokens to the document root as CSS custom properties.
 * App chrome reads --bg / --fg / --accent; the reader iframe mirrors these
 * separately via its own override layer.
 */
export function applyTheme(id: string, resolved?: ThemeDef): void {
  const t = resolved ?? getTheme(id);
  // getTheme falls back to the light theme for an unknown id. A custom theme
  // whose definitions haven't loaded yet (cold boot, before customThemes.load)
  // is "unknown" here — painting the fallback would flash the shell to light
  // and overwrite the cached palette. Reuse the cached vars for that exact id
  // until a later applyTheme (after the registry loads) paints the real one.
  if (t.id !== id && applyCachedTheme(id)) return;
  const { accentFg, elevated, accentInk, scheme } = paintTheme(t);
  // Cache the resolved tokens so the inline <head> bootstrap in index.html can
  // paint the saved theme before first paint, avoiding a flash of the default
  // theme on reload (server settings arrive too late to prevent it).
  try {
    localStorage.setItem("sayumi:theme", t.id);
    localStorage.setItem(
      "sayumi:theme-vars",
      JSON.stringify({
        id: t.id,
        bg: t.bg,
        fg: t.fg,
        accent: t.accent,
        accentFg,
        elevated,
        accentInk,
        scheme,
      }),
    );
  } catch {
    // Private-mode / disabled storage: the theme still applies, just no cache.
  }
}
