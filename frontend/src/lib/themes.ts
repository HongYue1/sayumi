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
    bg: "#fafaf9",
    fg: "#1c1917",
    accent: "#2563eb",
  },
  {
    id: "sepia",
    label: "Sepia",
    group: "light",
    bg: "#f5f0e8",
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
    bg: "#f9f5d7",
    fg: "#3c3836",
    accent: "#b57614",
  },
  {
    id: "ayu-light",
    label: "Ayu Light",
    group: "light",
    bg: "#fafafa",
    fg: "#5c6166",
    accent: "#ff9940",
  },
  {
    id: "rose-pine-dawn",
    label: "Rosé Pine Dawn",
    group: "light",
    bg: "#faf4ed",
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

  // ── Dark ──
  {
    id: "dark",
    label: "Dark",
    group: "dark",
    bg: "#0c0a09",
    fg: "#fafaf9",
    accent: "#60a5fa",
  },
  {
    id: "rose-pine",
    label: "Rosé Pine",
    group: "dark",
    bg: "#191724",
    fg: "#e0def4",
    accent: "#c4a7e7",
  },
  {
    id: "nord",
    label: "Nord",
    group: "dark",
    bg: "#2e3440",
    fg: "#eceff4",
    accent: "#88c0d0",
  },
  {
    id: "dracula",
    label: "Dracula",
    group: "dark",
    bg: "#282a36",
    fg: "#f8f8f2",
    accent: "#bd93f9",
  },
  {
    id: "catppuccin",
    label: "Catppuccin Mocha",
    group: "dark",
    bg: "#1e1e2e",
    fg: "#cdd6f4",
    accent: "#cba6f7",
  },
  {
    id: "gruvbox",
    label: "Gruvbox Dark",
    group: "dark",
    bg: "#282828",
    fg: "#ebdbb2",
    accent: "#fabd2f",
  },
  {
    id: "ayu-dark",
    label: "Ayu Dark",
    group: "dark",
    bg: "#0b0e14",
    fg: "#bfbdb6",
    accent: "#e6b450",
  },
  {
    id: "solarized-dark",
    label: "Solarized Dark",
    group: "dark",
    bg: "#002b36",
    fg: "#93a1a1",
    accent: "#268bd2",
  },
  {
    id: "tokyo-night",
    label: "Tokyo Night",
    group: "dark",
    bg: "#1a1b26",
    fg: "#c0caf5",
    accent: "#7aa2f7",
  },
  {
    id: "tokyo-storm",
    label: "Tokyo Storm",
    group: "dark",
    bg: "#24283b",
    fg: "#c0caf5",
    accent: "#7aa2f7",
  },
  {
    id: "everforest-dark",
    label: "Everforest Dark",
    group: "dark",
    bg: "#2d353b",
    fg: "#d3c6aa",
    accent: "#a7c080",
  },
  {
    id: "one-dark",
    label: "One Dark",
    group: "dark",
    bg: "#282c34",
    fg: "#abb2bf",
    accent: "#61afef",
  },
  {
    id: "kanagawa",
    label: "Kanagawa",
    group: "dark",
    bg: "#1f1f28",
    fg: "#dcd7ba",
    accent: "#7e9cd8",
  },
  {
    id: "flexoki-dark",
    label: "Flexoki Dark",
    group: "dark",
    bg: "#100f0f",
    fg: "#cecdc3",
    accent: "#4385be",
  },
];

export const THEME_IDS = THEMES.map((t) => t.id);

const THEME_MAP = new Map(THEMES.map((t) => [t.id, t] as const));
const THEME_ID_SET = new Set(THEME_IDS);

export function getTheme(id: string): ThemeDef | undefined {
  return THEME_MAP.get(id);
}

export function isValidTheme(id: string): boolean {
  return THEME_ID_SET.has(id);
}
