// getErrorMessage is a display policy, so these tests pin the boundary it
// draws: a server-authored ApiError message reaches the user, and everything
// else -- including a genuine frontend bug -- is replaced by the caller's
// fallback.
import { describe, expect, it } from "vitest";
import { ApiError } from "~/api/client";
import { getErrorMessage } from "~/lib/errors";

describe("getErrorMessage", () => {
  it("uses the ApiError message when present", () => {
    const err = new ApiError("Book not found", 404, "not_found");
    expect(getErrorMessage(err, "fallback")).toBe("Book not found");
  });

  it("falls back when the ApiError message is empty", () => {
    expect(getErrorMessage(new ApiError(""), "fallback")).toBe("fallback");
  });

  // The narrowing is the whole point: a bare Error at one of these call sites
  // is a frontend bug, and its message is an internal exception string rather
  // than copy anyone wrote for a user.
  it("falls back for an Error that is not an ApiError", () => {
    const bug = new TypeError("Cannot read properties of undefined");
    expect(getErrorMessage(bug, "Could not load")).toBe("Could not load");
  });

  it("falls back for an aborted request", () => {
    const abort = new DOMException("The operation was aborted.", "AbortError");
    expect(getErrorMessage(abort, "fallback")).toBe("fallback");
  });

  it("falls back for non-Error values", () => {
    expect(getErrorMessage("nope", "fallback")).toBe("fallback");
    expect(getErrorMessage(null, "fallback")).toBe("fallback");
    expect(getErrorMessage({ message: "x" }, "fallback")).toBe("fallback");
  });
});
