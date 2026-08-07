// First suite for the shared reachability signal. The module is the source of
// truth the OfflineBanner, the session boot retry and the API client all
// report through, so what it does NOT do matters as much as what it does:
// these tests pin transition-only notification, unsubscribe silence and the
// listener-exception contract (a throwing listener escapes into the reporter
// by design — swallowing it here would hide a listener bug behind a network
// error). Probe-verified before this suite was written.
import { describe, expect, it, vi } from "vitest";
import {
  isReachable,
  reportReachable,
  reportUnreachable,
  subscribeReachability,
} from "~/lib/reachability";

describe("reachability", () => {
  it("starts reachable", () => {
    expect(isReachable()).toBe(true);
  });

  it("notifies listeners on transitions only", () => {
    const seen: boolean[] = [];
    const stop = subscribeReachability((value) => seen.push(value));
    reportUnreachable();
    reportUnreachable();
    reportReachable();
    reportReachable();
    stop();
    expect(seen).toEqual([false, true]);
    expect(isReachable()).toBe(true);
  });

  it("silences an unsubscribed listener", () => {
    const seen: boolean[] = [];
    const stop = subscribeReachability((value) => seen.push(value));
    reportUnreachable();
    stop();
    reportReachable();
    reportUnreachable();
    reportReachable();
    expect(seen).toEqual([false]);
    expect(isReachable()).toBe(true);
  });

  it("fans out to every listener in registration order", () => {
    const order: string[] = [];
    const stopFirst = subscribeReachability(() => order.push("first"));
    const stopSecond = subscribeReachability(() => order.push("second"));
    reportUnreachable();
    stopFirst();
    stopSecond();
    reportReachable();
    expect(order).toEqual(["first", "second"]);
    expect(isReachable()).toBe(true);
  });

  it("lets a throwing listener escape and starve later listeners", () => {
    // The write lands before notification, so the flag still flips; the throw
    // escapes into the reporter and the listeners after the thrower never run.
    const later = vi.fn();
    const stopThrower = subscribeReachability(() => {
      throw new Error("listener boom");
    });
    const stopLater = subscribeReachability(later);
    expect(() => reportUnreachable()).toThrow("listener boom");
    expect(later).not.toHaveBeenCalled();
    expect(isReachable()).toBe(false);
    stopThrower();
    stopLater();
    reportReachable();
    expect(isReachable()).toBe(true);
  });

  it("survives a listener unsubscribing a later listener mid-dispatch", () => {
    const seen: string[] = [];
    let stopSecond: () => void = () => undefined;
    const stopFirst = subscribeReachability(() => {
      seen.push("first");
      stopSecond();
    });
    stopSecond = subscribeReachability(() => {
      seen.push("second");
    });
    reportUnreachable();
    reportReachable();
    stopFirst();
    stopSecond();
    // A Set skips an element deleted before iteration reaches it.
    expect(seen).toEqual(["first", "first"]);
    expect(isReachable()).toBe(true);
  });
});
