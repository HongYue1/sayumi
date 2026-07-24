import { describe, expect, it } from "vitest";
import { matchRoute } from "~/lib/router.svelte";

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
});
