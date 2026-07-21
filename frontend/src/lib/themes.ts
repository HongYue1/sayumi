export type ThemeGroup = "light" | "dark";

export interface ThemeDef {
  id: string;
  label: string;
  group: ThemeGroup;
  bg: string;
  fg: string;
  accent: string;
}

export const THEMES: ThemeDef[] = [
  // ── Light ──
  {
    id: "light",
    label: "Light",
    group: "light",
    bg: "#ffffff",
    fg: "#1c1917",
    accent: "#2563eb",
  },
  {
    id: "sepia",
    label: "Sepia",
    group: "light",
    bg: "#faf6ef",
    fg: "#3d2e1e",
    accent: "#8b6914",
  },
  {
    id: "catppuccin-latte",
    label: "Catppuccin Latte",
    group: "light",
    bg: "#eff1f5",
    fg: "#4c4f69",
    accent: "#8839ef",
  },
  {
    id: "gruvbox-light",
    label: "Gruvbox Light",
    group: "light",
    bg: "#fbf1c7",
    fg: "#3c3836",
    accent: "#b57614",
  },
  {
    id: "ayu-light",
    label: "Ayu Light",
    group: "light",
    bg: "#ffffff",
    fg: "#5c6166",
    accent: "#ff9940",
  },
  {
    id: "rose-pine-dawn",
    label: "Rosé Pine Dawn",
    group: "light",
    bg: "#fffaf3",
    fg: "#575279",
    accent: "#907aa9",
  },
  {
    id: "solarized-light",
    label: "Solarized Light",
    group: "light",
    bg: "#fdf6e3",
    fg: "#657b83",
    accent: "#268bd2",
  },
  {
    id: "everforest-light",
    label: "Everforest Light",
    group: "light",
    bg: "#f4f0d9",
    fg: "#5c6a72",
    accent: "#8da101",
  },
  {
    id: "flexoki-light",
    label: "Flexoki Light",
    group: "light",
    bg: "#fffcf0",
    fg: "#100f0f",
    accent: "#205ea6",
  },
  {
    id: "night-owl-light",
    label: "Night Owl Light",
    group: "light",
    bg: "#fbfbfb",
    fg: "#403f53",
    accent: "#2aa298",
  },

  // ── Dark ──
  {
    id: "dark",
    label: "Dark",
    group: "dark",
    bg: "#1c1917",
    fg: "#fafaf9",
    accent: "#60a5fa",
  },
  {
    id: "rose-pine",
    label: "Rosé Pine",
    group: "dark",
    bg: "#1f1d2e",
    fg: "#e0def4",
    accent: "#c4a7e7",
  },
  {
    id: "nord",
    label: "Nord",
    group: "dark",
    bg: "#3b4252",
    fg: "#eceff4",
    accent: "#88c0d0",
  },
  {
    id: "dracula",
    label: "Dracula",
    group: "dark",
    bg: "#21222c",
    fg: "#f8f8f2",
    accent: "#bd93f9",
  },
  {
    id: "catppuccin",
    label: "Catppuccin Mocha",
    group: "dark",
    bg: "#181825",
    fg: "#cdd6f4",
    accent: "#cba6f7",
  },
  {
    id: "gruvbox",
    label: "Gruvbox Dark",
    group: "dark",
    bg: "#32302f",
    fg: "#ebdbb2",
    accent: "#fabd2f",
  },
  {
    id: "ayu-dark",
    label: "Ayu Dark",
    group: "dark",
    bg: "#0d1017",
    fg: "#bfbdb6",
    accent: "#e6b450",
  },
  {
    id: "solarized-dark",
    label: "Solarized Dark",
    group: "dark",
    bg: "#073642",
    fg: "#93a1a1",
    accent: "#268bd2",
  },
  {
    id: "tokyo-night",
    label: "Tokyo Night",
    group: "dark",
    bg: "#16161e",
    fg: "#c0caf5",
    accent: "#7aa2f7",
  },
  {
    id: "tokyo-storm",
    label: "Tokyo Storm",
    group: "dark",
    bg: "#1f2335",
    fg: "#c0caf5",
    accent: "#7aa2f7",
  },
  {
    id: "everforest-dark",
    label: "Everforest Dark",
    group: "dark",
    bg: "#343f44",
    fg: "#d3c6aa",
    accent: "#a7c080",
  },
  {
    id: "one-dark",
    label: "One Dark",
    group: "dark",
    bg: "#21252b",
    fg: "#abb2bf",
    accent: "#61afef",
  },
  {
    id: "kanagawa",
    label: "Kanagawa",
    group: "dark",
    bg: "#16161d",
    fg: "#dcd7ba",
    accent: "#7e9cd8",
  },
  {
    id: "flexoki-dark",
    label: "Flexoki Dark",
    group: "dark",
    bg: "#1c1b1a",
    fg: "#cecdc3",
    accent: "#4385be",
  },
  {
    id: "night-owl",
    label: "Night Owl",
    group: "dark",
    bg: "#011627",
    fg: "#d6deeb",
    accent: "#82aaff",
  },
];

const THEME_MAP = new Map(THEMES.map((t) => [t.id, t] as const));

// Single source of truth for id -> theme resolution. O(1) Map lookup, falling
// back to the first theme (light) for an unknown/empty id so every caller is
// guaranteed a usable ThemeDef.
const FALLBACK: ThemeDef = THEMES[0];

// Runtime registry of user-created custom themes, keyed by id. Kept in sync by
// customThemes.svelte.ts (setCustomThemes) after the backend list loads or
// changes, so a custom id resolves to a real ThemeDef everywhere built-ins do —
// the app chrome (applyTheme) and, via the reader payload below, the frame —
// without threading a store through every getTheme caller.
const CUSTOM_THEMES = new Map<string, ThemeDef>();

/** Replaces the custom-theme registry. Called by the customThemes store. */
export function setCustomThemes(themes: ThemeDef[]): void {
  CUSTOM_THEMES.clear();
  for (const t of themes) CUSTOM_THEMES.set(t.id, t);
}

export function getTheme(id: string): ThemeDef {
  return THEME_MAP.get(id) ?? CUSTOM_THEMES.get(id) ?? FALLBACK;
}

/** True for a built-in theme id (one backed by a static frame.css class). */
export function isBuiltInTheme(id: string): boolean {
  return THEME_MAP.has(id);
}

// ── Custom-theme color math ──
// themes.ts stays import-free, so these small hex helpers are local rather than
// shared with theme.ts (which imports from here, so importing back would cycle).

function parseHex(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return null;
  let h = m[1];
  if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
  const n = parseInt(h, 16);
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
}

function channelHex(v: number): string {
  const n = Math.max(0, Math.min(255, Math.round(v)));
  return n.toString(16).padStart(2, "0");
}

function mixHex(a: string, b: string, t: number): string {
  const pa = parseHex(a);
  const pb = parseHex(b);
  if (!pa || !pb) return a;
  const r = channelHex(pa[0] + (pb[0] - pa[0]) * t);
  const g = channelHex(pa[1] + (pb[1] - pa[1]) * t);
  const bl = channelHex(pa[2] + (pb[2] - pa[2]) * t);
  return `#${r}${g}${bl}`;
}

/** Relative luminance (WCAG) of a hex color, 0 (black) to 1 (white). */
function luminance(hex: string): number {
  const p = parseHex(hex);
  if (!p) return 1;
  const lin = (c: number): number => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(p[0]) + 0.7152 * lin(p[1]) + 0.0722 * lin(p[2]);
}

/**
 * Accent for a custom theme left on "auto". Picks a vivid blue seed sized to
 * the background's lightness (so it reads on light and dark), then nudges it
 * toward the foreground so the accent feels related to the palette. Always
 * returns a concrete hex so the shell (onAccentColor) and reader can use it.
 */
export function autoAccent(bg: string, fg: string): string {
  const seed = luminance(bg) > 0.4 ? "#2563eb" : "#60a5fa";
  return mixHex(seed, fg, 0.15);
}

/** Light/dark grouping inferred from a background color's lightness, so a new
 *  custom theme slots into the matching (day/night) row. */
export function themeGroupFor(bg: string): ThemeGroup {
  return luminance(bg) > 0.4 ? "light" : "dark";
}

/**
 * Reader-iframe CSS variable declarations for a theme. The frame paints from 7
 * tokens; built-ins define them via static html.theme-<id> rules in frame.css,
 * but a custom id has no class, so its palette is sent to the frame and set on
 * <html> (see frame.ts). Secondary tones use color-mix, already used in app.css.
 */
export function deriveReaderVars(t: ThemeDef): string {
  const accent = t.accent || autoAccent(t.bg, t.fg);
  const scheme = t.group === "dark" ? "dark" : "light";
  return [
    `color-scheme: ${scheme};`,
    `--bg-primary: ${t.bg};`,
    `--bg-secondary: color-mix(in srgb, ${t.bg} 92%, ${t.fg});`,
    `--text-primary: ${t.fg};`,
    `--text-secondary: color-mix(in srgb, ${t.fg} 72%, ${t.bg});`,
    `--text-muted: color-mix(in srgb, ${t.fg} 52%, ${t.bg});`,
    `--accent: ${accent};`,
    `--border: color-mix(in srgb, ${t.fg} 18%, ${t.bg});`,
  ].join(" ");
}

/**
 * Reader theme-var payload for a theme id, or null when none is needed. Built-in
 * ids return null (their frame.css class already supplies the tokens, and a bare
 * `html {}` override is outranked by html.theme-<id> anyway); custom ids return
 * their derived declarations. The list is passed in so a reactive caller
 * (settings.iframe) re-runs when a custom theme is edited.
 */
export function readerThemeVars(
  id: string,
  customThemes: ThemeDef[] = [],
): string | null {
  if (THEME_MAP.has(id)) return null;
  const t = customThemes.find((c) => c.id === id) ?? CUSTOM_THEMES.get(id);
  return t ? deriveReaderVars(t) : null;
}
