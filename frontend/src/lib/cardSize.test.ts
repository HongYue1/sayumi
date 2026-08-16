// Suite for the library card-size preference.
//
// Three decisions are pinned here, each easy to undo by accident:
//   - null is a REAL state ("no preference"), not a synonym for a number: the
//     shelf keeps app.css's fluid clamp() until a size is chosen, and
//     cardSizeCss() spells that state as the CSS-wide `initial` keyword so
//     every var(--card-size, ...) fallback in the sheet takes over. Returning
//     a number instead would freeze the shelf at one width for everybody, and
//     emitting undefined would be worse still: Solid writes style objects
//     through setProperty(), so the literal token `undefined` would land in
//     the property and invalidate grid-template-columns entirely.
//   - The stored value is clamped on READ as well as on write, so tightening
//     the bounds in a later version cannot leave a stale 400px in force.
//   - localStorage is allowed to fail (private modes, full quota). The signal
//     is the source of truth for the tab either way.
//
// Every test re-imports the module: the signal is module-level and seeds from
// storage at import time, so that seeding is itself behaviour under test.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
// Type-only, so the module below is still loaded fresh by the dynamic import.
import type * as CardSizeModule from "~/lib/cardSize";

const KEY = "sayumi:card-size";

async function load(): Promise<typeof CardSizeModule> {
  vi.resetModules();
  return import("~/lib/cardSize");
}

describe("card size preference", () => {
  beforeEach(() => {
    localStorage.removeItem(KEY);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    try {
      localStorage.removeItem(KEY);
    } catch {
      // A test that stubs storage into throwing must not fail the teardown.
    }
  });

  it("starts with no preference, leaving the shelf fluid", async () => {
    const { cardSize, cardSizeCss } = await load();
    expect(cardSize.value).toBeNull();
    expect(cardSizeCss()).toBe("initial");
  });

  it("seeds from storage", async () => {
    localStorage.setItem(KEY, "172");
    const { cardSize, cardSizeCss } = await load();
    expect(cardSize.value).toBe(172);
    expect(cardSizeCss()).toBe("172px");
  });

  it("clamps a stored value into the current bounds", async () => {
    localStorage.setItem(KEY, "4000");
    const wide = await load();
    expect(wide.cardSize.value).toBe(wide.CARD_SIZE_MAX);

    localStorage.setItem(KEY, "1");
    const narrow = await load();
    expect(narrow.cardSize.value).toBe(narrow.CARD_SIZE_MIN);
  });

  it("treats junk and an empty string as no preference", async () => {
    localStorage.setItem(KEY, "wide please");
    expect((await load()).cardSize.value).toBeNull();

    localStorage.setItem(KEY, "");
    expect((await load()).cardSize.value).toBeNull();
  });

  it("rounds, clamps and persists what the slider sends", async () => {
    const { cardSize, CARD_SIZE_MAX } = await load();
    // A write is not visible to a read in the same tick under Solid 2.0
    // batching; flush() publishes it. Same contract as lib/ui.ts.
    cardSize.set(171.6);
    flush();
    expect(cardSize.value).toBe(172);
    expect(localStorage.getItem(KEY)).toBe("172");

    cardSize.set(9999);
    flush();
    expect(cardSize.value).toBe(CARD_SIZE_MAX);
    expect(localStorage.getItem(KEY)).toBe(String(CARD_SIZE_MAX));
  });

  it("reset() removes the key instead of storing a sentinel", async () => {
    localStorage.setItem(KEY, "200");
    const { cardSize, cardSizeCss } = await load();
    expect(cardSize.value).toBe(200);

    cardSize.reset();
    flush();
    expect(cardSize.value).toBeNull();
    expect(cardSizeCss()).toBe("initial");
    // "Never chose" and "chose Auto" are the same state, so there is nothing
    // to write: a sentinel would be indistinguishable from a chosen size on
    // the next load.
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("keeps working when storage throws", async () => {
    const getItem = vi.spyOn(localStorage, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const { cardSize } = await load();
    expect(cardSize.value).toBeNull();
    getItem.mockRestore();

    const setItem = vi.spyOn(localStorage, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    const removeItem = vi
      .spyOn(localStorage, "removeItem")
      .mockImplementation(() => {
        throw new Error("quota");
      });
    try {
      cardSize.set(200);
      flush();
      expect(cardSize.value).toBe(200);

      cardSize.reset();
      flush();
      expect(cardSize.value).toBeNull();
    } finally {
      // Restored here rather than in the teardown: the teardown itself
      // touches storage, so a surviving spy would fail the test after it had
      // already passed.
      setItem.mockRestore();
      removeItem.mockRestore();
    }
  });
});
