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

export function findFlair(id: string | undefined, customs: FlairDef[]): FlairDef | undefined {
  if (!id) return undefined;
  return DEFAULT_FLAIRS.find((f) => f.id === id) ?? customs.find((f) => f.id === id);
}

/** Readable text color for a flair badge. Palette hues read best with dark text. */
export function flairTextColor(): string {
  return "rgba(0,0,0,0.78)";
}
