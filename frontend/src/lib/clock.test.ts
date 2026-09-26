// Suite for the reader clock and its alarm.
//
// What is pinned here, each easy to undo by accident:
//   - The clock is opt-OUT: with nothing stored, the pill shows.
//   - The four formatters are pure. Every boundary a clock has -- midnight and
//     noon on the 12-hour face, the hour carry in a countdown, a target time
//     that has already passed today -- is checked against a fixed instant
//     rather than the machine's own clock.
//   - A stored alarm in the PAST is dropped on read. A laptop that slept past
//     its alarm must not ring the moment the reader mounts.
//   - localStorage is allowed to fail and to hold junk; the signal stays the
//     source of truth for the tab either way.
//
// The prefs tests re-import the module, because the signal is module-level and
// seeds from storage at import time -- that seeding is itself under test.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// The getters read a signal, so a write made in the same turn needs a flush
// before it is observable -- the same idiom as cardSize.test.ts.
import { flush } from "solid-js";
// Type-only, so the dynamic import below still loads a fresh copy.
import type * as ClockModule from "~/lib/clock";
import {
  alarmTargetFrom,
  formatClock,
  formatRemaining,
  hhmmFrom,
} from "~/lib/clock";

const KEY = "sayumi:clock";

async function load(): Promise<typeof ClockModule> {
  vi.resetModules();
  return import("~/lib/clock");
}

/** Local-time constructor, so these cases do not move with the test host's
 *  timezone the way an ISO-with-Z string would. */
function at(
  y: number,
  m: number,
  d: number,
  h: number,
  min: number,
  s = 0,
): Date {
  return new Date(y, m - 1, d, h, min, s, 0);
}

describe("formatClock", () => {
  it("writes a 12-hour time without a leading zero, with AM/PM", () => {
    expect(formatClock(at(2026, 3, 4, 13, 25), false)).toBe("1:25 PM");
    expect(formatClock(at(2026, 3, 4, 9, 5), false)).toBe("9:05 AM");
  });

  it("pads the hour on the 24-hour clock", () => {
    expect(formatClock(at(2026, 3, 4, 9, 5), true)).toBe("09:05");
    expect(formatClock(at(2026, 3, 4, 13, 25), true)).toBe("13:25");
  });

  it("renders midnight and noon as 12, not 0", () => {
    expect(formatClock(at(2026, 3, 4, 0, 0), false)).toBe("12:00 AM");
    expect(formatClock(at(2026, 3, 4, 12, 0), false)).toBe("12:00 PM");
    expect(formatClock(at(2026, 3, 4, 0, 0), true)).toBe("00:00");
  });
});

describe("formatRemaining", () => {
  it("rounds up to the minute, so 1m never means due", () => {
    expect(formatRemaining(1)).toBe("1m");
    expect(formatRemaining(60_000)).toBe("1m");
    expect(formatRemaining(60_001)).toBe("2m");
  });

  it("carries into hours and drops a zero minute", () => {
    expect(formatRemaining(50 * 60_000)).toBe("50m");
    expect(formatRemaining(125 * 60_000)).toBe("2h 5m");
    expect(formatRemaining(120 * 60_000)).toBe("2h");
  });

  it("says now at or past the instant", () => {
    expect(formatRemaining(0)).toBe("now");
    expect(formatRemaining(-5000)).toBe("now");
  });
});

describe("alarmTargetFrom", () => {
  it("picks today when the time is still ahead", () => {
    const now = at(2026, 3, 4, 12, 35).getTime();
    expect(alarmTargetFrom(now, "13:25")).toBe(
      at(2026, 3, 4, 13, 25).getTime(),
    );
  });

  it("rolls to tomorrow when the time has passed, including right now", () => {
    const now = at(2026, 3, 4, 23, 40).getTime();
    expect(alarmTargetFrom(now, "06:00")).toBe(at(2026, 3, 5, 6, 0).getTime());
    // Equal counts as passed: an alarm for the current minute is tomorrow's.
    expect(alarmTargetFrom(now, "23:40")).toBe(
      at(2026, 3, 5, 23, 40).getTime(),
    );
  });

  it("refuses anything that is not a valid HH:MM", () => {
    const now = at(2026, 3, 4, 12, 0).getTime();
    expect(alarmTargetFrom(now, "")).toBeNull();
    expect(alarmTargetFrom(now, "7")).toBeNull();
    expect(alarmTargetFrom(now, "24:00")).toBeNull();
    expect(alarmTargetFrom(now, "12:60")).toBeNull();
    expect(alarmTargetFrom(now, "noon")).toBeNull();
  });
});

describe("hhmmFrom", () => {
  it("pads both halves for the time input", () => {
    expect(hhmmFrom(at(2026, 3, 4, 6, 5))).toBe("06:05");
  });
});

describe("clock preferences", () => {
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

  it("shows the clock, 12-hour, with no alarm, before anything is stored", async () => {
    const { clock } = await load();
    expect(clock.show).toBe(true);
    expect(clock.hour24).toBe(false);
    expect(clock.showRemaining).toBe(false);
    expect(clock.alarmAt).toBeNull();
  });

  it("round-trips a change through storage", async () => {
    const first = await load();
    first.clock.setHour24(true);
    first.clock.setShow(false);
    flush();
    expect(first.clock.hour24).toBe(true);
    expect(JSON.parse(localStorage.getItem(KEY)!)).toMatchObject({
      show: false,
      hour24: true,
    });
    // A fresh import is a fresh tab: the stored blob must seed it.
    const second = await load();
    expect(second.clock.hour24).toBe(true);
    expect(second.clock.show).toBe(false);
  });

  it("keeps a future alarm and drops a past one", async () => {
    const future = Date.now() + 600_000;
    localStorage.setItem(KEY, JSON.stringify({ alarmAt: future }));
    expect((await load()).clock.alarmAt).toBe(future);

    localStorage.setItem(
      KEY,
      JSON.stringify({ alarmAt: Date.now() - 600_000 }),
    );
    expect((await load()).clock.alarmAt).toBeNull();
  });

  it("falls back to the defaults for junk, and for wrongly typed fields", async () => {
    localStorage.setItem(KEY, "{not json");
    expect((await load()).clock.show).toBe(true);

    localStorage.setItem(KEY, JSON.stringify({ show: "no", hour24: 24 }));
    const { clock } = await load();
    expect(clock.show).toBe(true);
    expect(clock.hour24).toBe(false);
  });

  it("survives storage that throws, keeping the value for this tab", async () => {
    const { clock } = await load();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => clock.setShowRemaining(true)).not.toThrow();
    flush();
    expect(clock.showRemaining).toBe(true);
  });
});
