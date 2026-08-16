// Library card size: how wide a cover may grow before the shelf adds another
// column. A per-device VIEW preference, deliberately NOT part of user settings.
//
// Why it is not in lib/settings.ts: every field there round-trips through the
// server (internal/api/setting.go validates and persists it, storage carries
// the column), so one more field is a schema change plus a migration -- for a
// value that describes the screen in front of you rather than the profile. A
// phone and a 27-inch monitor want different answers to this question, and the
// same profile is read on both. localStorage keeps them independent, the same
// reasoning as Read.tsx's progress cache and lib/theme.ts's pre-paint cache.
//
// null means "no preference", which is a real state and not a synonym for a
// number: app.css then falls back to a fluid clamp() (and to a narrower floor
// on phones), which is exactly what the shelf did before this control existed.
// Library.tsx spells that state as the CSS-wide `initial` keyword, the
// guaranteed-invalid value for a custom property, so every
// var(--card-size, ...) in the sheet takes its fallback.
import { createSignal } from "solid-js";

const KEY = "sayumi:card-size";

/** Slider bounds in px. Below MIN the cover's title clips to one word; above
 *  MAX a 1440px shelf shows fewer than five books per row. */
export const CARD_SIZE_MIN = 120;
export const CARD_SIZE_MAX = 280;
/** Where the slider sits before a preference exists: the middle of the fluid
 *  default's clamp() range in app.css, so the first drag barely jumps. */
export const CARD_SIZE_SEED = 172;

function clampSize(px: number): number {
  return Math.min(CARD_SIZE_MAX, Math.max(CARD_SIZE_MIN, Math.round(px)));
}

/** Reads the stored preference, tolerating blocked storage and junk values. */
function read(): number | null {
  let raw: string | null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    return null;
  }
  if (raw === null || raw === "") return null;
  const px = Number(raw);
  // Clamped on read as well as on write: the bounds above may tighten in a
  // later version, and a stored 400 must not survive as a 400px floor.
  return Number.isFinite(px) ? clampSize(px) : null;
}

const [size, setSize] = createSignal<number | null>(read());

/**
 * The value for the shelf's `--card-size` custom property.
 *
 * `initial` is the guaranteed-invalid value for a custom property, so every
 * `var(--card-size, ...)` in app.css falls back -- which is how one
 * always-present inline style can also mean "no preference". Deliberately not
 * an omitted/undefined property: Solid writes style objects through
 * setProperty(), and `undefined` would stringify to the literal token
 * `undefined`, invalidating the whole grid-template-columns declaration and
 * collapsing the shelf to a single column.
 */
export function cardSizeCss(): string {
  const px = size();
  return px === null ? "initial" : `${px}px`;
}

export const cardSize = {
  /** px floor for a shelf column, or null while no preference is stored. */
  get value(): number | null {
    return size();
  },

  set(px: number): void {
    const next = clampSize(px);
    setSize(next);
    try {
      localStorage.setItem(KEY, String(next));
    } catch {
      // Blocked or full storage must not break the control: the signal above
      // is what the shelf reads, and it is already correct for this tab.
    }
  },

  /** Back to the fluid default -- removes the key rather than storing a
   *  sentinel, so "never chose" and "chose auto" stay the same state. */
  reset(): void {
    setSize(null);
    try {
      localStorage.removeItem(KEY);
    } catch {
      // See set().
    }
  },
};
