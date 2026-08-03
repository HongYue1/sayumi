import { beforeEach, describe, expect, it, vi } from "vitest";

// The gate is module-level state, so every test imports it fresh.
beforeEach(() => {
  vi.resetModules();
});

describe("session generation", () => {
  it("advances on demand", async () => {
    const gate = await import("~/lib/sessionGate");
    const before = gate.currentSessionEpoch();

    gate.advanceSessionEpoch();

    expect(gate.currentSessionEpoch()).toBe(before + 1);
  });
});

describe("unauthenticated dispatch", () => {
  it("hands every listener the generation that started the request", async () => {
    const gate = await import("~/lib/sessionGate");
    const seen: number[] = [];
    gate.subscribeUnauthenticated((epoch) => seen.push(epoch));
    gate.subscribeUnauthenticated((epoch) => {
      // A listener tearing the session down advances the epoch mid-dispatch;
      // the listeners behind it must still be told which generation was lost,
      // otherwise delivery would silently depend on subscription order.
      gate.advanceSessionEpoch();
      seen.push(epoch);
    });
    gate.subscribeUnauthenticated((epoch) => seen.push(epoch));
    const reported = gate.currentSessionEpoch();

    gate.reportUnauthenticated(reported);

    expect(seen).toEqual([reported, reported, reported]);
  });

  it("keeps dispatching when a listener throws, and never rethrows", async () => {
    const gate = await import("~/lib/sessionGate");
    const later = vi.fn();
    gate.subscribeUnauthenticated(() => {
      throw new Error("listener exploded");
    });
    gate.subscribeUnauthenticated(later);

    // The client reports from inside its error path, one line before it throws
    // the ApiError the caller is awaiting: a listener must not replace it.
    expect(() =>
      gate.reportUnauthenticated(gate.currentSessionEpoch()),
    ).not.toThrow();
    expect(later).toHaveBeenCalledOnce();
  });

  it("stops delivering to an unsubscribed listener", async () => {
    const gate = await import("~/lib/sessionGate");
    const listener = vi.fn();

    gate.subscribeUnauthenticated(listener)();
    gate.reportUnauthenticated(gate.currentSessionEpoch());

    expect(listener).not.toHaveBeenCalled();
  });

  it("survives a listener that unsubscribes during dispatch", async () => {
    const gate = await import("~/lib/sessionGate");
    const later = vi.fn();
    const self: { unsubscribe?: () => void } = {};
    self.unsubscribe = gate.subscribeUnauthenticated(() =>
      self.unsubscribe?.(),
    );
    gate.subscribeUnauthenticated(later);

    gate.reportUnauthenticated(gate.currentSessionEpoch());

    expect(later).toHaveBeenCalledOnce();
  });
});
