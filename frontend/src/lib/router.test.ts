import { describe, expect, it, vi } from "vitest";
import { createEffect, createRoot, flush } from "solid-js";
import { createRouter, matchRoute, type Router } from "~/lib/router";

describe("matchRoute", () => {
  it("decodes a valid encoded book id", () => {
    expect(matchRoute("/read/book%20one")).toEqual({
      path: "/read/:id",
      params: { id: "book one" },
    });
  });

  it("falls back to the library for malformed percent escapes", () => {
    expect(matchRoute("/read/%")).toEqual({ path: "/", params: {} });
    expect(matchRoute("/read/%E0%A4%A")).toEqual({ path: "/", params: {} });
  });

  it("falls back to the library for unknown routes", () => {
    expect(matchRoute("/settings")).toEqual({ path: "/", params: {} });
  });

  it("refuses an empty book id", () => {
    // Load-bearing. App.tsx matches on path and then renders
    // <Show when={params.id} keyed> with no fallback, so a matched route
    // carrying "" is a blank screen -- not the library. The non-empty capture
    // group is the only thing standing between the two.
    expect(matchRoute("/read/")).toEqual({ path: "/", params: {} });
  });

  it("anchors the read route at both ends", () => {
    expect(matchRoute("/x/read/abc")).toEqual({ path: "/", params: {} });
    expect(matchRoute("/read/a/b")).toEqual({ path: "/", params: {} });
  });
});

/** Subscribes an effect to the route signal; count() reports apply runs. */
function watchRoute(r: Router): { count: () => number; dispose: () => void } {
  let notifications = 0;
  const dispose = createRoot((d) => {
    createEffect(
      () => r.route,
      () => {
        notifications += 1;
      },
    );
    return d;
  });
  return { count: () => notifications, dispose };
}

// createRouter is exported for these tests only: the module singleton is built
// at import time, so none of the behaviour below is reachable through it. Each
// call attaches its own permanent hashchange listener to the shared window, so
// nothing here assumes it is the only subscriber.
describe("createRouter", () => {
  it("parses the initial hash at construction", () => {
    window.location.hash = "#/read/abc";
    expect(createRouter().route).toEqual({
      path: "/read/:id",
      params: { id: "abc" },
    });
  });

  it("re-parses on an external hash change", async () => {
    window.location.hash = "#/";
    const r = createRouter();
    expect(r.route.path).toBe("/");
    window.location.hash = "#/read/next";
    await vi.waitFor(() => expect(r.route.params.id).toBe("next"));
  });

  it("navigating to the current hash is a silent no-op that reports false", async () => {
    // An identical hash assignment fires no hashchange at all, so the call
    // would do nothing: navigate reports it instead of swallowing it, and a
    // caller whose intent is already satisfied runs its own fallback.
    window.location.hash = "#/read/same";
    await new Promise((resolve) => setTimeout(resolve, 20));
    const r = createRouter();
    let events = 0;
    const count = (): void => {
      events += 1;
    };
    window.addEventListener("hashchange", count);
    try {
      expect(r.navigate("/read/same")).toBe(false);
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(events).toBe(0);
      expect(window.location.hash).toBe("#/read/same");
      expect(r.navigate("/read/other")).toBe(true);
      await vi.waitFor(() => expect(events).toBe(1));
      expect(r.route.params.id).toBe("other");
    } finally {
      window.removeEventListener("hashchange", count);
    }
  });

  it("detects the no-op against an encoded hash (like-for-like comparison)", async () => {
    // location.hash reads back raw — never percent-decoded — and the /read/
    // call sites encodeURIComponent their ids, so both sides of navigate's
    // comparison are encoded. The fixture uses an encoded id so the named
    // branch is the only path: a decoded-vs-encoded comparison would
    // misreport this as a real navigation.
    window.location.hash = "#/read/book%20one";
    await new Promise((resolve) => setTimeout(resolve, 20));
    const r = createRouter();
    expect(r.route.params.id).toBe("book one");
    expect(r.navigate("/read/book%20one")).toBe(false);
    expect(window.location.hash).toBe("#/read/book%20one");
  });

  it("does not wake subscribers when a hashchange parses to an equal route", async () => {
    // A same-value assignment fires no hashchange at all, so the synthetic
    // dispatch is the deterministic way to reach the listener with an equal
    // parse — and the comparator must swallow it. The trailing real change
    // is the control proving the subscriber was live.
    window.location.hash = "#/read/abc";
    await new Promise((resolve) => setTimeout(resolve, 20));
    const r = createRouter();
    const watch = watchRoute(r);
    try {
      flush();
      const baseline = watch.count();
      window.dispatchEvent(new Event("hashchange"));
      flush();
      expect(watch.count()).toBe(baseline);
      window.location.hash = "#/read/other";
      await vi.waitFor(() => expect(watch.count()).toBe(baseline + 1));
    } finally {
      watch.dispose();
    }
  });

  it("still notifies for a same-path route with a different id", async () => {
    // Load-bearing: App keys the reader on params.id, so the comparator must
    // not weaken to path-only. (happy-dom queues hashchange asynchronously.)
    window.location.hash = "#/read/abc";
    await new Promise((resolve) => setTimeout(resolve, 20));
    const r = createRouter();
    const watch = watchRoute(r);
    try {
      flush();
      const baseline = watch.count();
      window.location.hash = "#/read/abcd";
      await vi.waitFor(() => expect(watch.count()).toBe(baseline + 1));
      expect(r.route.params.id).toBe("abcd");
    } finally {
      watch.dispose();
    }
  });
});
