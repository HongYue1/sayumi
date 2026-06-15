import { describe, it, expect } from "vitest";
import { getErrorMessage, isTextEntryTarget } from "~/lib/errors";

describe("getErrorMessage", () => {
  it("uses the Error message when present", () => {
    expect(getErrorMessage(new Error("boom"), "fallback")).toBe("boom");
  });

  it("falls back when the Error message is empty", () => {
    expect(getErrorMessage(new Error(""), "fallback")).toBe("fallback");
  });

  it("falls back for non-Error values", () => {
    expect(getErrorMessage("nope", "fallback")).toBe("fallback");
    expect(getErrorMessage(null, "fallback")).toBe("fallback");
    expect(getErrorMessage({ message: "x" }, "fallback")).toBe("fallback");
  });
});

describe("isTextEntryTarget", () => {
  it("is true for form text controls", () => {
    for (const tag of ["input", "textarea", "select"] as const) {
      expect(isTextEntryTarget(document.createElement(tag))).toBe(true);
    }
  });

  it("is false for a plain element and for null", () => {
    expect(isTextEntryTarget(document.createElement("div"))).toBe(false);
    expect(isTextEntryTarget(null)).toBe(false);
  });
});
