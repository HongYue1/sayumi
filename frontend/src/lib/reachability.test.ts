// First suite for the shared reachability signal. The module is the source of
// truth the OfflineBanner, the session boot retry and the API client all
// report through, so what it does NOT do matters as much as what it does:
// these tests pin transition-only notification, unsubscribe silence and the
// dispatch-isolation contract (a throwing listener neither replaces the error
// the API client is about to throw nor starves the listeners behind it, and
// the listener set is snapshotted so subscribing mid-dispatch cannot backdate
// a notification). Probe-verified before this suite was written.
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

  it("isolates a throwing listener from the reporter and its peers", () => {
    // reportUnreachable() runs inside the API client's fetch catch, one line
    // before it throws the network_error ApiError the caller is awaiting: an
    // escaping listener error would replace that ApiError, and the listeners
    // behind the thrower would never hear the transition.
    const later = vi.fn();
    const stopThrower = subscribeReachability(() => {
      throw new Error("listener boom");
    });
    const stopLater = subscribeReachability(later);
    expect(() => reportUnreachable()).not.toThrow();
    expect(later).toHaveBeenCalledWith(false);
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
    // Dispatch re-checks membership, so a listener unsubscribed before its
    // turn stays silent even though the snapshot still holds it.
    expect(seen).toEqual(["first", "first"]);
    expect(isReachable()).toBe(true);
  });

  it("does not notify a listener that subscribes mid-dispatch", () => {
    const latecomer = vi.fn();
    let subscribed = false;
    let stopLatecomer: () => void = () => undefined;
    const stopFirst = subscribeReachability(() => {
      if (subscribed) return;
      subscribed = true;
      stopLatecomer = subscribeReachability(latecomer);
    });
    reportUnreachable();
    // The newcomer registered after the transition it would have been handed;
    // iterating a copy is what keeps that stale edge from reaching it.
    expect(latecomer).not.toHaveBeenCalled();
    reportReachable();
    expect(latecomer).toHaveBeenCalledWith(true);
    stopFirst();
    stopLatecomer();
    expect(isReachable()).toBe(true);
  });
});
