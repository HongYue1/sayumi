import { describe, it, expect } from "vitest";
import { getErrorMessage } from "~/lib/errors";

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
