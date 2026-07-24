import type { FlairDef } from "~/api/client";

// Built-in flairs live on the client (like the theme catalogue). Custom flairs
// are created per-profile and fetched from the server.
export const DEFAULT_FLAIRS: FlairDef[] = [
  { id: "reading", label: "Reading", color: "#3b82f6" },
  { id: "finished", label: "Finished", color: "#22c55e" },
  { id: "dropped", label: "Dropped", color: "#ef4444" },
  { id: "plan-to-read", label: "Plan to Read", color: "#a855f7" },
];

// Cycled when creating new custom flairs: saturated mid-lightness hues that
// read clearly on both light and dark backgrounds.
const CUSTOM_PALETTE: readonly string[] = [
  "#3b82f6", // blue
  "#10b981", // emerald
  "#f59e0b", // amber
  "#ef4444", // red
  "#8b5cf6", // violet
  "#06b6d4", // cyan
  "#ec4899", // pink
  "#f97316", // orange
];

/** Next palette color, cycling indefinitely as the custom-flair count grows. */
export function getNextPaletteColor(count: number): string {
  return CUSTOM_PALETTE[count % CUSTOM_PALETTE.length];
}

export function findFlair(
  id: string | undefined,
  customs: FlairDef[],
): FlairDef | undefined {
  if (!id) return undefined;
  return (
    DEFAULT_FLAIRS.find((f) => f.id === id) ?? customs.find((f) => f.id === id)
  );
}

/** Chooses the higher-contrast opaque text color for a hex badge background. */
export function flairTextColor(background: string): "#000" | "#fff" {
  const match = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(background);
  if (!match) return "#000";

  const hex =
    match[1].length === 3
      ? [...match[1]].map((digit) => digit + digit).join("")
      : match[1];
  const linearChannel = (offset: number): number => {
    const value = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045
      ? value / 12.92
      : Math.pow((value + 0.055) / 1.055, 2.4);
  };
  const luminance =
    0.2126 * linearChannel(0) +
    0.7152 * linearChannel(2) +
    0.0722 * linearChannel(4);
  const blackContrast = (luminance + 0.05) / 0.05;
  const whiteContrast = 1.05 / (luminance + 0.05);

  return blackContrast >= whiteContrast ? "#000" : "#fff";
}
