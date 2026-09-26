// Suite for the reader clock pill. Nothing is stubbed but the wall clock and
// the alarm sound (happy-dom has no media playback). The invariants:
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
import { clock } from "~/lib/clock";
import { startAlarmSound, stopAlarmSound } from "~/lib/alarmSound";

const KEY = "sayumi:clock";
// A fixed instant, in local time so the rendered string cannot move with the
// test host's timezone: 2026-03-04, 12:35:00.
const NOW = new Date(2026, 2, 4, 12, 35, 0, 0).getTime();

// The whole sound module is replaced: happy-dom's <audio> never plays, and
// what is under test is that the ring is started and stopped with the sheet.
vi.mock("~/lib/alarmSound", () => ({
  startAlarmSound: vi.fn(),
  stopAlarmSound: vi.fn(),
  preloadAlarmSound: vi.fn(),
}));

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

  function rings(): number {
    return vi.mocked(startAlarmSound).mock.calls.length;
  }

  function stops(): number {
    return vi.mocked(stopAlarmSound).mock.calls.length;
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
    // Module-factory vi.fns, so restoreAllMocks does not clear their calls.
    vi.mocked(startAlarmSound).mockClear();
    vi.mocked(stopAlarmSound).mockClear();
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
    expect(rings()).toBe(1);

    // A cleared alarm cannot ring a second time on the next tick.
    vi.setSystemTime(NOW + 62_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(document.querySelectorAll(".alarm-sheet")).toHaveLength(1);
  });

  it("rings for as long as the sheet is up, and no longer", async () => {
    clock.setAlarm(NOW + 60_000);
    flush();
    mount(true);
    await settle();
    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    // The sound loops in the element, so it is started once, not re-triggered
    // on a timer while the sheet waits.
    expect(rings()).toBe(1);
    vi.advanceTimersByTime(20_000);
    await settle();
    expect(rings()).toBe(1);
    expect(stops()).toBe(0);

    sheet()?.querySelector<HTMLButtonElement>(".alarm-stop")?.click();
    await settle();
    expect(sheet()).toBeNull();
    // Turning it off must silence it: a loop left behind a closed sheet has no
    // control left to stop it.
    expect(stops()).toBe(1);
  });

  it("shows the alarm's name, and keeps it across a snooze", async () => {
    clock.setAlarm(NOW + 60_000, "Stop for dinner");
    flush();
    mount(true);
    await settle();
    vi.setSystemTime(NOW + 61_000);
    vi.advanceTimersByTime(1000);
    await settle();
    expect(sheet()?.textContent).toContain("Stop for dinner");

    const snooze =
      sheet()?.querySelectorAll<HTMLButtonElement>(".alarm-snooze-btn");
    expect(snooze?.length).toBe(3);
    snooze?.[0]?.click();
    await settle();
    // Snoozing closes the ring and re-arms the same alarm ten minutes out.
    expect(sheet()).toBeNull();
    // Ten minutes from the click, which is a second past the ring: the fake
    // clock moved with the timer that fired the alarm.
    expect(clock.alarmAt).toBe(NOW + 62_000 + 10 * 60_000);
    expect(clock.alarmLabel).toBe("Stop for dinner");
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
