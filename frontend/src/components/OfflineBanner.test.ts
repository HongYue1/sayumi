// OfflineBanner.test.ts -- pins the contract this banner owns: an announced
// live region (WCAG 4.1.3), the reachability mirror it seeds from, the poll
// cadence, the pause on a hidden tab, probe coalescing, retry feedback, and a
// leak-free teardown.
//
// Only ~/api/client is mocked. checkHealth is the component's single
// collaborator, so the real ~/lib/reachability is driven through
// reportReachable/reportUnreachable instead of a hand-rolled twin: its
// notify-on-transition-only behaviour is part of what keeps the banner from
// thrashing the timer, and a stub would quietly hide that.
//
// document.hidden is defined directly rather than through visibilityState. In
// happy-dom `hidden` is an independent getter, so stubbing visibilityState
// alone leaves isHidden() false and the pause assertions would pass without
// ever entering the branch.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import { render } from "@solidjs/web";
import type * as ApiClient from "~/api/client";
import { reportReachable, reportUnreachable } from "~/lib/reachability";
import OfflineBanner from "~/components/OfflineBanner";

const checkHealth = vi.hoisted(() => vi.fn<() => Promise<boolean>>());

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, checkHealth };
});

let dispose: (() => void) | undefined;
let container: HTMLDivElement;
let tabHidden = false;

function mount(): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  dispose = render(() => OfflineBanner(), container);
  flush();
}

function unmount(): void {
  dispose?.();
  dispose = undefined;
  container.remove();
}

/** Drains the probe promise and the scheduler queue behind it. */
async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function setTabHidden(value: boolean): void {
  tabHidden = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

function live(): HTMLElement {
  const found = container.querySelector<HTMLElement>('[role="alert"]');
  if (!found) throw new Error("the live region is not mounted");
  return found;
}

function banner(): HTMLElement | null {
  return container.querySelector<HTMLElement>(".offline-banner");
}

function retry(): HTMLButtonElement {
  const found = container.querySelector<HTMLButtonElement>(
    ".offline-banner-retry",
  );
  if (!found) throw new Error("no retry button");
  return found;
}

function htmlHasOpenClass(): boolean {
  return document.documentElement.classList.contains("offline-banner-open");
}

beforeEach(() => {
  vi.useFakeTimers();
  tabHidden = false;
  Object.defineProperty(document, "hidden", {
    configurable: true,
    get: () => tabHidden,
  });
  reportReachable();
  checkHealth.mockResolvedValue(true);
});

afterEach(() => {
  if (dispose) unmount();
  document.documentElement.classList.remove("offline-banner-open");
  reportReachable();
  vi.useRealTimers();
  vi.resetAllMocks();
});

describe("OfflineBanner", () => {
  it("keeps the live region mounted and empty while reachable", async () => {
    mount();
    await settle();

    expect(live().textContent).toBe("");
    expect(banner()).toBeNull();
    expect(htmlHasOpenClass()).toBe(false);
  });

  it("announces from the region it was already mounted with", async () => {
    mount();
    await settle();
    const region = live();

    reportUnreachable();
    flush();

    // Same node: only its text changed, which is the whole point -- a region
    // inserted alongside its text is never announced.
    expect(live()).toBe(region);
    expect(region.textContent).toBe("Server unreachable");
    // The visible banner must not double as the live region.
    expect(banner()).not.toBeNull();
    expect(banner()?.getAttribute("role")).toBeNull();
    expect(htmlHasOpenClass()).toBe(true);

    // The reserved height on <html> is global, so it has to come back off when
    // the banner's owner is disposed while still offline.
    unmount();
    expect(htmlHasOpenClass()).toBe(false);
  });

  it("mirrors an already-unreachable flag before the first probe answers", () => {
    reportUnreachable();
    checkHealth.mockResolvedValue(false);

    mount();

    expect(banner()).not.toBeNull();
    expect(live().textContent).toBe("Server unreachable");
  });

  it("polls on the online cadence and backs off while offline", async () => {
    mount();
    await settle();
    const mounted = checkHealth.mock.calls.length;

    await vi.advanceTimersByTimeAsync(9_999);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(mounted);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(mounted + 1);

    checkHealth.mockResolvedValue(false);
    await vi.advanceTimersByTimeAsync(10_000);
    await settle();
    expect(banner()).not.toBeNull();
    const offline = checkHealth.mock.calls.length;

    await vi.advanceTimersByTimeAsync(14_999);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(offline);
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(offline + 1);
  });

  it("pauses the heartbeat on a hidden tab and re-probes on return", async () => {
    mount();
    await settle();
    const before = checkHealth.mock.calls.length;

    setTabHidden(true);
    await settle();
    expect(vi.getTimerCount()).toBe(0);

    await vi.advanceTimersByTimeAsync(60_000);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(before);

    setTabHidden(false);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(before + 1);
    expect(vi.getTimerCount()).toBe(1);

    // Disarming the timer on visibilitychange is not sufficient on its own: a
    // probe already in flight when the tab hides runs scheduleNext() from its
    // finally, so the hidden guard has to live there too or the loop silently
    // re-arms itself on a backgrounded tab.
    let resolveProbe: ((value: boolean) => void) | undefined;
    checkHealth.mockImplementation(
      () =>
        new Promise<boolean>((resolve) => {
          resolveProbe = resolve;
        }),
    );
    await vi.advanceTimersByTimeAsync(10_000);
    await settle();
    expect(checkHealth).toHaveBeenCalledTimes(before + 2);
    expect(vi.getTimerCount()).toBe(0);

    setTabHidden(true);
    await settle();
    resolveProbe?.(true);
    await settle();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("coalesces overlapping probes", async () => {
    let resolveProbe: ((value: boolean) => void) | undefined;
    checkHealth.mockImplementation(
      () =>
        new Promise<boolean>((resolve) => {
          resolveProbe = resolve;
        }),
    );

    mount();
    flush();
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("online"));
    document.dispatchEvent(new Event("visibilitychange"));
    await settle();

    expect(checkHealth).toHaveBeenCalledTimes(1);

    resolveProbe?.(true);
    await settle();
    expect(banner()).toBeNull();
  });

  it("never raises the banner on an OS offline event alone", async () => {
    mount();
    await settle();

    window.dispatchEvent(new Event("offline"));
    await settle();

    // 127.0.0.1 still answers with WiFi off, so only a failed probe counts.
    expect(banner()).toBeNull();
    expect(live().textContent).toBe("");
  });

  it("marks retry busy while a probe is in flight, and ignores the second click", async () => {
    reportUnreachable();
    checkHealth.mockResolvedValue(false);
    mount();
    await settle();
    expect(retry().getAttribute("aria-busy")).toBe("false");
    expect(retry().getAttribute("aria-disabled")).toBe("false");

    let resolveProbe: ((value: boolean) => void) | undefined;
    checkHealth.mockImplementation(
      () =>
        new Promise<boolean>((resolve) => {
          resolveProbe = resolve;
        }),
    );

    retry().click();
    flush();
    expect(retry().getAttribute("aria-busy")).toBe("true");
    expect(retry().getAttribute("aria-disabled")).toBe("true");
    const inFlight = checkHealth.mock.calls.length;

    retry().click();
    flush();
    expect(checkHealth).toHaveBeenCalledTimes(inFlight);

    resolveProbe?.(false);
    await settle();
    expect(retry().getAttribute("aria-busy")).toBe("false");
  });

  it("clears its timer and listeners on dispose", async () => {
    mount();
    await settle();
    expect(vi.getTimerCount()).toBe(1);

    unmount();

    expect(vi.getTimerCount()).toBe(0);
    expect(htmlHasOpenClass()).toBe(false);
    const after = checkHealth.mock.calls.length;

    window.dispatchEvent(new Event("focus"));
    window.dispatchEvent(new Event("online"));
    window.dispatchEvent(new Event("offline"));
    document.dispatchEvent(new Event("visibilitychange"));
    reportUnreachable();
    await settle();

    expect(checkHealth).toHaveBeenCalledTimes(after);
    expect(vi.getTimerCount()).toBe(0);
  });
});
