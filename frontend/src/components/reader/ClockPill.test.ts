// Suite for the reader clock pill. Nothing is stubbed but the wall clock and
// the alarm chime (a real AudioContext does not exist under happy-dom). The
// invariants:
//   - Placement is the reading mode's, not a preference: centre in paged
//     modes (rdp-clock-mid), the free right corner in scroll mode.
//   - Switching the clock off renders nothing at all, but an ARMED ALARM
//     still rings -- turning the clock off is not cancelling an alarm.
//   - The alarm fires once, clears itself, and opens a blocking sheet that
//     only a dismissal closes; a second tick past the instant must not open
//     another.
//   - Time-remaining is a display choice over the same armed alarm.
//   - Not a live region: a clock with role="status" interrupts screen readers
//     every minute, forever (the position pill next door pins the same rule).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import ClockPill from "~/components/reader/ClockPill";
import { clock, playAlarmChime } from "~/lib/clock";
import type * as ClockModule from "~/lib/clock";

const KEY = "sayumi:clock";
// A fixed instant, in local time so the rendered string cannot move with the
// test host's timezone: 2026-03-04, 12:35:00.
const NOW = new Date(2026, 2, 4, 12, 35, 0, 0).getTime();

vi.mock("~/lib/clock", async (importOriginal) => {
  const actual = await importOriginal<typeof ClockModule>();
  // Only the chime is replaced: happy-dom has no AudioContext, and the
  // formatters and the preference store are the things under test.
  return { ...actual, playAlarmChime: vi.fn() };
});

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("ClockPill", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;

  function mount(paged: boolean): void {
    dispose = render(() => ClockPill({ paged }), container);
  }

  function pill(): HTMLElement | null {
    return container.querySelector(".rdp-clock");
  }

  // The ringing sheet is portaled to document.body, so it is never found
  // inside the pill's container.
  function sheet(): HTMLElement | null {
    return document.querySelector(".alarm-sheet");
  }

  function chimes(): number {
    return vi.mocked(playAlarmChime).mock.calls.length;
  }

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    localStorage.removeItem(KEY);
    clock.setShow(true);
    clock.setHour24(false);
    clock.setShowRemaining(false);
    clock.setAlarm(null);
    flush();
    // A module-factory vi.fn, so restoreAllMocks does not clear its calls.
    vi.mocked(playAlarmChime).mockClear();
    container = document.createElement("div");
    document.body.append(container);
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    vi.useRealTimers();
    vi.restoreAllMocks();
    localStorage.removeItem(KEY);
  });

  it("shows the time, and takes the middle only in paged modes", async () => {
    mount(true);
    await settle();
    expect(pill()?.textContent).toContain("12:35 PM");
    expect(pill()?.classList.contains("rdp-clock-mid")).toBe(true);
    // No live region: see the header.
    expect(pill()?.getAttribute("role")).toBeNull();
    expect(pill()?.getAttribute("aria-label")).toContain("12:35 PM");
  });

  it("takes the right corner in scroll mode, where no page pill sits", async () => {
    mount(false);
    await settle();
    expect(pill()?.classList.contains("rdp-clock-mid")).toBe(false);
  });

  it("follows the 24-hour setting", async () => {
    clock.setHour24(true);
    flush();
    mount(true);
    await settle();
    expect(pill()?.textContent).toContain("12:35");
    expect(pill()?.textContent).not.toContain("PM");
  });

  it("renders the armed alarm as its target time, or as time left", async () => {
    clock.setAlarm(NOW + 50 * 60_000);
    flush();
    mount(true);
    await settle();
    expect(pill()?.textContent).toContain("1:25 PM");

    clock.setShowRemaining(true);
    await settle();
    expect(pill()?.textContent).toContain("50m");
    expect(pill()?.textContent).not.toContain("1:25 PM");
  });

  it("opens a blocking sheet when the alarm comes due, clearing the alarm", async () => {
    clock.setAlarm(NOW + 60_000);
    flush();
    mount(true);
    await settle();
    expect(sheet()).toBeNull();

    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(clock.alarmAt).toBeNull();
    // A sheet, not a toast: it has to survive being looked away from.
    const open = sheet();
    expect(open).not.toBeNull();
    expect(open?.getAttribute("role")).toBe("alertdialog");
    expect(open?.getAttribute("aria-modal")).toBe("true");
    // The instant it was set for, not "now": the two differ when a laptop
    // wakes with the alarm long past.
    expect(open?.textContent).toContain("12:36 PM");
    expect(chimes()).toBe(1);

    // A cleared alarm cannot ring a second time on the next tick.
    vi.setSystemTime(NOW + 62_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(document.querySelectorAll(".alarm-sheet")).toHaveLength(1);
  });

  it("keeps chiming while the sheet is up, and stops when it is turned off", async () => {
    clock.setAlarm(NOW + 60_000);
    flush();
    mount(true);
    await settle();
    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(chimes()).toBe(1);

    // One note is missable in a noisy room, so the sheet repeats it.
    vi.advanceTimersByTime(8000);
    await settle();
    expect(chimes()).toBeGreaterThan(1);

    const stop = sheet()?.querySelector<HTMLButtonElement>(".alarm-stop");
    stop?.click();
    await settle();
    expect(sheet()).toBeNull();
    const after = chimes();
    vi.advanceTimersByTime(20_000);
    await settle();
    expect(chimes()).toBe(after);
  });

  it("closes the ringing sheet on Escape", async () => {
    clock.setAlarm(NOW + 60_000);
    flush();
    mount(true);
    await settle();
    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(sheet()).not.toBeNull();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await settle();
    expect(sheet()).toBeNull();
  });

  it("renders no pill with the clock off, but still rings an armed alarm", async () => {
    clock.setShow(false);
    clock.setAlarm(NOW + 60_000);
    flush();
    mount(true);
    await settle();
    expect(pill()).toBeNull();

    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(sheet()).not.toBeNull();
    expect(clock.alarmAt).toBeNull();
  });
});
