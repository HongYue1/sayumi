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

/** Trailing window before a chosen size reaches storage. A drag fires oninput
 *  on every pointermove -- dozens of events a second -- and setItem is a
 *  synchronous main-thread write, so persisting each tick lands one inside
 *  every frame of the drag. The signal above is what the shelf reads and it
 *  stays immediate, so the live reflow is unaffected; only the durable copy
 *  waits for the drag to settle. Same trailing-debounce shape as the settings
 *  store's 500ms save, shorter because this write never leaves the device. */
const PERSIST_DELAY_MS = 200;

let persistTimer: ReturnType<typeof setTimeout> | undefined;
/** The size a pending timer will write; only meaningful while one is armed. */
let pendingPx = 0;

function write(px: number): void {
  try {
    localStorage.setItem(KEY, String(px));
  } catch {
    // Blocked or full storage must not break the control: the signal is what
    // the shelf reads, and it is already correct for this tab.
  }
}

function cancelPendingWrite(): void {
  if (persistTimer === undefined) return;
  clearTimeout(persistTimer);
  persistTimer = undefined;
}

/** Write a staged size now: a trailing-only timer would drop the last drag of
 *  a closing tab, the same hazard the settings store flushes on pagehide. */
function flushPendingWrite(): void {
  if (persistTimer === undefined) return;
  cancelPendingWrite();
  write(pendingPx);
}

// App-lifetime listener, never removed: this preference outlives every
// component that edits it, matching the settings store's pagehide flush.
window.addEventListener("pagehide", flushPendingWrite);

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
    // Debounced on purpose -- see PERSIST_DELAY_MS. The signal above already
    // carries the new size, so only the stored copy trails the drag.
    pendingPx = next;
    cancelPendingWrite();
    persistTimer = setTimeout(() => {
      persistTimer = undefined;
      write(pendingPx);
    }, PERSIST_DELAY_MS);
  },

  /** Back to the fluid default -- removes the key rather than storing a
   *  sentinel, so "never chose" and "chose auto" stay the same state. */
  reset(): void {
    setSize(null);
    // Dropped rather than flushed: Auto is one click, and a size still waiting
    // on the timer would otherwise land after it and bring the size back.
    cancelPendingWrite();
    try {
      localStorage.removeItem(KEY);
    } catch {
      // See write().
    }
  },
};
