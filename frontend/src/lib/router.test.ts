import { describe, expect, it, vi } from "vitest";
import { createRouter, matchRoute } from "~/lib/router";

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

  it("navigating to the current hash is a silent no-op", async () => {
    // This is why SettingsPanel special-cases the specimen: an identical hash
    // assignment fires no hashchange at all, so the click does nothing.
    window.location.hash = "#/read/same";
    await new Promise((resolve) => setTimeout(resolve, 20));
    const r = createRouter();
    let events = 0;
    const count = (): void => {
      events += 1;
    };
    window.addEventListener("hashchange", count);
    try {
      r.navigate("/read/same");
      await new Promise((resolve) => setTimeout(resolve, 20));
      expect(events).toBe(0);
      expect(window.location.hash).toBe("#/read/same");
      r.navigate("/read/other");
      await vi.waitFor(() => expect(events).toBe(1));
      expect(r.route.params.id).toBe("other");
    } finally {
      window.removeEventListener("hashchange", count);
    }
  });
});
