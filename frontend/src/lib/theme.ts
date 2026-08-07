// getTheme now lives in lib/themes (single Map-backed source of truth). It's
// re-exported here so existing importers using "~/lib/theme" keep working.
import {
  getTheme,
  themeSurface,
  deriveSurface,
  prefersBlackText,
  readableAccent,
} from "~/lib/themes";

export { getTheme };

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
export function getCachedThemeId(fallback = "light"): string {
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
    typeof v.scheme !== "string"
  ) {
    return false;
  }
  const root = document.documentElement;
  root.style.setProperty("--bg", v.bg);
  root.style.setProperty("--fg", v.fg);
  root.style.setProperty("--accent", v.accent);
  root.style.setProperty("--accent-fg", v.accentFg);
  // Older caches predate the elevated-surface / accent-ink tokens; derive then.
  root.style.setProperty(
    "--elevated",
    typeof v.elevated === "string" ? v.elevated : deriveSurface(v.bg, v.fg),
  );
  root.style.setProperty(
    "--accent-ink",
    typeof v.accentInk === "string"
      ? v.accentInk
      : readableAccent(v.accent, v.bg),
  );
  root.style.colorScheme = v.scheme;
  root.dataset.theme = id;
  return true;
}

/**
 * Applies a theme's tokens to the document root as CSS custom properties.
 * App chrome reads --bg / --fg / --accent; the reader iframe mirrors these
 * separately via its own override layer.
 */
export function applyTheme(id: string): void {
  const t = getTheme(id);
  // getTheme falls back to the light theme for an unknown id. A custom theme
  // whose definitions haven't loaded yet (cold boot, before customThemes.load)
  // is "unknown" here — painting the fallback would flash the shell to light
  // and overwrite the cached palette. Reuse the cached vars for that exact id
  // until a later applyTheme (after the registry loads) paints the real one.
  if (t.id !== id && applyCachedTheme(id)) return;
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
  // Cache the resolved tokens so the inline <head> bootstrap in index.html can
  // paint the saved theme before first paint, avoiding a flash of the default
  // light theme on reload (server settings arrive too late to prevent it).
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
