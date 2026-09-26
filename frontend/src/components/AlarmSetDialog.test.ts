// Suite for the "Set an alarm" sheet. The invariants:
//   - It exists only while ui.alarm is set, and Escape closes it in the window
//     CAPTURE phase so one press cannot also navigate the reader back.
//   - The draft is seeded ON EVERY OPEN, not once at mount: this sheet lives
//     for the whole session, so a once-only seed would show a stale time.
//     Seeded from the armed alarm when there is one, an hour out when not.
//   - The time field is a draft: nothing is armed until the form is submitted.
//   - Clear is offered only when an alarm is armed, and it drops the name too.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import AlarmSetDialog from "~/components/AlarmSetDialog";
import { clock } from "~/lib/clock";
import { ui } from "~/lib/ui";

const KEY = "sayumi:clock";
// Local time, so the rendered strings cannot move with the test host's
// timezone: 2026-03-04, 12:35:00.
const NOW = new Date(2026, 2, 4, 12, 35, 0, 0).getTime();

// happy-dom has no media playback, and arming warms the ring.
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

describe("AlarmSetDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;

  function sheet(): HTMLElement | null {
    return container.querySelector(".alarm-set-sheet");
  }

  function time(): HTMLInputElement | null {
    return container.querySelector<HTMLInputElement>(".alarm-set-time");
  }

  function name(): HTMLInputElement | null {
    return container.querySelector<HTMLInputElement>(".alarm-set-name");
  }

  function type(input: HTMLInputElement | null, value: string): void {
    if (input === null) throw new Error("field missing");
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }

  async function open(): Promise<void> {
    ui.openAlarm();
    await settle();
  }

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    localStorage.removeItem(KEY);
    clock.setAlarm(null);
    flush();
    container = document.createElement("div");
    document.body.append(container);
    dispose = render(() => AlarmSetDialog(), container);
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    ui.closeOverlays();
    clock.setAlarm(null);
    flush();
    vi.useRealTimers();
    localStorage.removeItem(KEY);
  });

  it("renders nothing until it is opened", async () => {
    await settle();
    expect(sheet()).toBeNull();

    await open();
    expect(sheet()).not.toBeNull();
    expect(sheet()?.getAttribute("aria-modal")).toBe("true");
  });

  it("seeds an hour out with nothing armed, and re-seeds on each open", async () => {
    await open();
    expect(time()?.value).toBe("13:35");
    expect(name()?.value).toBe("");

    ui.closeOverlays();
    await settle();
    clock.setAlarm(NOW + 90 * 60_000, "Stop for dinner");
    flush();

    // The second open shows the armed alarm, not the first draft.
    await open();
    expect(time()?.value).toBe("14:05");
    expect(name()?.value).toBe("Stop for dinner");
    expect(sheet()?.textContent).toContain("Armed for 2:05 PM");
  });

  it("arms only on submit, with the typed name", async () => {
    await open();
    type(time(), "13:00");
    type(name(), "Stop for dinner");
    await settle();
    // Still a draft: <input type="time"> reports every keystroke, and arming
    // on input would fire an alarm for a half-typed time.
    expect(clock.alarmAt).toBeNull();

    container
      .querySelector<HTMLFormElement>(".alarm-set-form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    expect(clock.alarmAt).toBe(new Date(2026, 2, 4, 13, 0, 0, 0).getTime());
    expect(clock.alarmLabel).toBe("Stop for dinner");
    // Arming closes the sheet: the confirmation is the toast and the pill.
    expect(sheet()).toBeNull();
  });

  it("offers Clear only when an alarm is armed", async () => {
    await open();
    expect(container.querySelector(".btn-ghost")).toBeNull();

    ui.closeOverlays();
    await settle();
    clock.setAlarm(NOW + 600_000, "Stop for dinner");
    flush();
    await open();
    container.querySelector<HTMLButtonElement>(".btn-ghost")?.click();
    await settle();
    expect(clock.alarmAt).toBeNull();
    expect(clock.alarmLabel).toBe("");
    expect(sheet()).toBeNull();
  });

  it("closes on Escape, and stops listening once closed", async () => {
    await open();
    const escape = new KeyboardEvent("keydown", {
      key: "Escape",
      cancelable: true,
    });
    window.dispatchEvent(escape);
    await settle();
    expect(sheet()).toBeNull();
    // Consumed, so the reader's own handler does not also act on it.
    expect(escape.defaultPrevented).toBe(true);

    const after = new KeyboardEvent("keydown", {
      key: "Escape",
      cancelable: true,
    });
    window.dispatchEvent(after);
    await settle();
    expect(after.defaultPrevented).toBe(false);
  });
});
