import { THEMES, type ThemeDef } from "~/lib/themes";

const FALLBACK: ThemeDef = THEMES[0];

export function getTheme(id: string): ThemeDef {
  return THEMES.find((t) => t.id === id) ?? FALLBACK;
}

/**
 * Picks a readable foreground (near-black or white) for text/icons sitting on
 * the accent color, using the WCAG relative-luminance crossover (~0.179).
 * Keeps accent buttons legible on light accents (e.g. Gruvbox/Ayu) where
 * white-on-accent would fail contrast. Falls back to white for odd hexes.
 */
function onAccentColor(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return "#ffffff";
  const n = parseInt(m[1], 16);
  const toLinear = (c: number): number => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  const lum =
    0.2126 * toLinear((n >> 16) & 0xff) +
    0.7152 * toLinear((n >> 8) & 0xff) +
    0.0722 * toLinear(n & 0xff);
  return lum > 0.179 ? "#1c1917" : "#ffffff";
}

/**
 * Applies a theme's tokens to the document root as CSS custom properties.
 * App chrome reads --bg / --fg / --accent; the reader iframe mirrors these
 * separately via its own override layer.
 */
export function applyTheme(id: string): void {
  const t = getTheme(id);
  const accentFg = onAccentColor(t.accent);
  const scheme = t.group === "dark" ? "dark" : "light";
  const root = document.documentElement;
  root.style.setProperty("--bg", t.bg);
  root.style.setProperty("--fg", t.fg);
  root.style.setProperty("--accent", t.accent);
  root.style.setProperty("--accent-fg", accentFg);
  root.style.colorScheme = scheme;
  root.dataset.theme = t.id;
  // Cache the resolved tokens so the inline <head> bootstrap in index.html can
  // paint the saved theme before first paint, avoiding a flash of the default
  // light theme on reload (server settings arrive too late to prevent it).
  try {
    localStorage.setItem("sayumi:theme", t.id);
    localStorage.setItem(
      "sayumi:theme-vars",
      JSON.stringify({ id: t.id, bg: t.bg, fg: t.fg, accent: t.accent, accentFg, scheme }),
    );
  } catch {
    // Private-mode / disabled storage: the theme still applies, just no cache.
  }
}
