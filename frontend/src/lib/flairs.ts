import type { FlairDef } from "~/api/client";
import { prefersBlackText } from "~/lib/themes";

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

/**
 * Chooses the higher-contrast opaque text color for a hex badge background.
 * The strict "#rgb"/"#rrggbb" test is deliberate: badge colors come from the
 * server or CUSTOM_PALETTE and are always in that form, so anything else is
 * unknown input and keeps this component's long-standing black default rather
 * than being coerced. The contrast decision itself is shared with the app shell;
 * the black fallback handed to prefersBlackText records the same policy for a
 * color it cannot parse, though the test above already rules that case out.
 */
export function flairTextColor(background: string): "#000" | "#fff" {
  if (!/^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(background)) return "#000";
  return prefersBlackText(background, true) ? "#000" : "#fff";
}
