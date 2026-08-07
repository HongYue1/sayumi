export type ThemeGroup = "light" | "dark";

export interface ThemeDef {
  id: string;
  label: string;
  group: ThemeGroup;
  bg: string;
  fg: string;
  accent: string;
  /**
   * The scheme's OFFICIAL elevated/secondary surface (the color its own
   * ecosystem uses for bars, sidebars, panels) — e.g. Rosé Pine `surface`,
   * Catppuccin `mantle`, Nord `nord1`, Solarized `base02`/`base2`, Tokyo
   * Night `bg_dark`, Atom One Dark's sidebar. Omitted where the scheme is
   * officially flat (Night Owl paints every pane #011627); those fall back
   * to a derived wash via themeSurface().
   */
  surface?: string;
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
    surface: "#f5f5f4",
  },
  {
    id: "sepia",
    label: "Sepia",
    group: "light",
    bg: "#faf6ef",
    fg: "#3d2e1e",
    accent: "#8b6914",
    surface: "#ede8df",
  },
  {
    id: "catppuccin-latte",
    label: "Catppuccin Latte",
    group: "light",
    bg: "#eff1f5",
    fg: "#4c4f69",
    accent: "#8839ef",
    surface: "#e6e9ef",
  },
  {
    id: "gruvbox-light",
    label: "Gruvbox Light",
    group: "light",
    bg: "#fbf1c7",
    fg: "#3c3836",
    accent: "#af3a03",
    surface: "#f2e5bc",
  },
  {
    id: "ayu-light",
    label: "Ayu Light",
    group: "light",
    bg: "#fcfcfc",
    fg: "#5c6166",
    accent: "#ff9940",
    surface: "#f3f4f5",
  },
  {
    id: "rose-pine-dawn",
    label: "Rosé Pine Dawn",
    group: "light",
    bg: "#faf4ed",
    fg: "#575279",
    accent: "#907aa9",
    surface: "#fffaf3",
  },
  {
    id: "solarized-light",
    label: "Solarized Light",
    group: "light",
    bg: "#fdf6e3",
    fg: "#657b83",
    accent: "#268bd2",
    surface: "#eee8d5",
  },
  {
    id: "everforest-light",
    label: "Everforest Light",
    group: "light",
    bg: "#fdf6e3",
    fg: "#5c6a72",
    accent: "#8da101",
    surface: "#f4f0d9",
  },
  {
    id: "flexoki-light",
    label: "Flexoki Light",
    group: "light",
    bg: "#fffcf0",
    fg: "#100f0f",
    accent: "#205ea6",
    surface: "#f2f0e5",
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
    surface: "#292524",
  },
  {
    id: "rose-pine",
    label: "Rosé Pine",
    group: "dark",
    bg: "#191724",
    fg: "#e0def4",
    accent: "#c4a7e7",
    surface: "#1f1d2e",
  },
  {
    id: "nord",
    label: "Nord",
    group: "dark",
    bg: "#2e3440",
    fg: "#d8dee9",
    accent: "#88c0d0",
    surface: "#3b4252",
  },
  {
    id: "dracula",
    label: "Dracula",
    group: "dark",
    bg: "#282a36",
    fg: "#f8f8f2",
    accent: "#bd93f9",
    surface: "#21222c",
  },
  {
    id: "catppuccin",
    label: "Catppuccin Mocha",
    group: "dark",
    bg: "#1e1e2e",
    fg: "#cdd6f4",
    accent: "#cba6f7",
    surface: "#181825",
  },
  {
    id: "gruvbox",
    label: "Gruvbox Dark",
    group: "dark",
    bg: "#282828",
    fg: "#ebdbb2",
    accent: "#fe8019",
    surface: "#32302f",
  },
  {
    id: "ayu-dark",
    label: "Ayu Dark",
    group: "dark",
    bg: "#0b0e14",
    fg: "#bfbdb6",
    accent: "#e6b450",
    surface: "#0d1017",
  },
  {
    id: "solarized-dark",
    label: "Solarized Dark",
    group: "dark",
    bg: "#002b36",
    fg: "#839496",
    accent: "#268bd2",
    surface: "#073642",
  },
  {
    id: "tokyo-night",
    label: "Tokyo Night",
    group: "dark",
    bg: "#1a1b26",
    fg: "#c0caf5",
    accent: "#7aa2f7",
    surface: "#16161e",
  },
  {
    id: "tokyo-storm",
    label: "Tokyo Storm",
    group: "dark",
    bg: "#24283b",
    fg: "#c0caf5",
    accent: "#7aa2f7",
    surface: "#1f2335",
  },
  {
    id: "everforest-dark",
    label: "Everforest Dark",
    group: "dark",
    bg: "#2d353b",
    fg: "#d3c6aa",
    accent: "#a7c080",
    surface: "#343f44",
  },
  {
    id: "one-dark",
    label: "One Dark",
    group: "dark",
    bg: "#282c34",
    fg: "#abb2bf",
    accent: "#61afef",
    surface: "#21252b",
  },
  {
    id: "kanagawa",
    label: "Kanagawa",
    group: "dark",
    bg: "#1f1f28",
    fg: "#dcd7ba",
    accent: "#7e9cd8",
    surface: "#16161d",
  },
  {
    id: "flexoki-dark",
    label: "Flexoki Dark",
    group: "dark",
    bg: "#100f0f",
    fg: "#cecdc3",
    accent: "#4385be",
    surface: "#1c1b1a",
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
// back to the light theme for an unknown/empty id so every caller is
// guaranteed a usable ThemeDef. Resolved by id rather than by position so
// reordering THEMES cannot silently repoint every unknown-id lookup.
const FALLBACK: ThemeDef = THEME_MAP.get("light") ?? THEMES[0];

// Runtime registry of user-created custom themes, keyed by id. Kept in sync by
// customThemes.ts (setCustomThemes) after the backend list loads or
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

// ── Color math ──
// themes.ts imports nothing, which makes it the right home for the shared hex
// helpers: theme.ts and flairs.ts both import from here, so the arrow only
// ever points one way. Parsing and mixing stay private; the decisions callers
// need (prefersBlackText, readableAccent, themeSurface) are exported.

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

/**
 * Relative luminance (WCAG) of a hex color, 0 (black) to 1 (white).
 * An unparseable color yields 1, the lightest possible paper, so callers treat
 * unknown input as light: themeGroupFor() groups it with the day themes and
 * readableAccent() darkens against it rather than washing it out.
 */
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

/** WCAG contrast ratio between two hex colors (1..21). */
function contrastRatio(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/**
 * Whether black text reads better than white on this color. The one home for
 * that decision: the app shell (onAccentColor) and the flair badge
 * (flairTextColor) both delegate here so the two cannot drift apart. Callers
 * pass the answer they want for a color that cannot be parsed, because their
 * historical defaults differ -- the shell assumes white ink, the badge black.
 */
export function prefersBlackText(color: string, fallback: boolean): boolean {
  if (!parseHex(color)) return fallback;
  return contrastRatio("#000000", color) >= contrastRatio("#ffffff", color);
}

/**
 * The accent, darkened/lightened just enough to read as TEXT on the theme's
 * background (WCAG AA, 4.5:1). Official palettes tune their accent for fills
 * and focus rings, and several (ayu light, solarized, rosé pine dawn) sit well
 * under text contrast on their own paper. Fills keep using --accent; ink-like
 * uses (active labels, links, checkmarks) use this. Binary-searches the
 * smallest mix toward black/white so the hue shifts as little as possible; if
 * even the endpoint can't reach 4.5:1 it returns the endpoint (best effort).
 */
export function readableAccent(accent: string, bg: string): string {
  if (!parseHex(accent) || !parseHex(bg)) return accent;
  if (contrastRatio(accent, bg) >= 4.5) return accent;
  // Mix toward whichever endpoint actually contrasts more with this paper, not
  // the one implied by the light/dark grouping. Those disagree over a mid-tone
  // background: themeGroupFor() splits day from night at luminance 0.4, but
  // black overtakes white as the readable ink at 0.179. Using the grouping
  // pivot sent every background in between toward white, which then failed the
  // guard below and returned bare white at as little as 2.4:1.
  const toward = prefersBlackText(bg, true) ? "#000000" : "#ffffff";
  // Defensive: black clears 4.5:1 from luminance 0.175 up and white from 0.183
  // down, so the better endpoint always qualifies and this cannot trigger
  // today. Kept so a future change to the endpoints degrades to best effort.
  if (contrastRatio(toward, bg) < 4.5) return toward;
  let lo = 0;
  let hi = 1;
  for (let i = 0; i < 12; i++) {
    const mid = (lo + hi) / 2;
    if (contrastRatio(mixHex(accent, toward, mid), bg) >= 4.5) hi = mid;
    else lo = mid;
  }
  return mixHex(accent, toward, hi);
}

/** Derived elevated surface for themes without an official one (custom themes,
 *  officially-flat schemes): a 6% wash of the ink into the paper. */
export function deriveSurface(bg: string, fg: string): string {
  return mixHex(bg, fg, 0.06);
}

/** A theme's elevated surface: the official color when the scheme defines
 *  one, else the derived wash. Always a concrete hex. */
export function themeSurface(t: ThemeDef): string {
  return t.surface ?? deriveSurface(t.bg, t.fg);
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
