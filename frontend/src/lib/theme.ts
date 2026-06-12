import { THEMES, type ThemeDef } from "~/lib/themes";

const FALLBACK: ThemeDef = THEMES[0];

export function getTheme(id: string): ThemeDef {
  return THEMES.find((t) => t.id === id) ?? FALLBACK;
}

/**
 * Applies a theme's tokens to the document root as CSS custom properties.
 * App chrome reads --bg / --fg / --accent; the reader iframe mirrors these
 * separately via its own override layer.
 */
export function applyTheme(id: string): void {
  const t = getTheme(id);
  const root = document.documentElement;
  root.style.setProperty("--bg", t.bg);
  root.style.setProperty("--fg", t.fg);
  root.style.setProperty("--accent", t.accent);
  root.style.colorScheme = t.group === "dark" ? "dark" : "light";
  root.dataset.theme = t.id;
}
