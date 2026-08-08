import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  libraryApi,
  restoreRealTimersWithoutLeaks,
} from "~/test/library-harness";

const TRANSPORT_METHODS = [
  "createFlair",
  "deleteBook",
  "deleteFlair",
  "getBooks",
  "getFlairs",
  "rescanLibrary",
  "setBookFlair",
  "updateBookMeta",
  "uploadBook",
  "uploadCover",
];

const SUITES = ["src/lib/library.test.ts", "src/routes/Library.test.ts"];

afterEach(() => {
  if (!vi.isFakeTimers()) return;
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe("library test harness", () => {
  it("owns the complete shared transport seam", () => {
    expect(Object.keys(libraryApi).sort()).toEqual(TRANSPORT_METHODS);
  });

  it("keeps both suites on the shared seam without reachability doubles", () => {
    for (const path of SUITES) {
      const source = readFileSync(path, "utf8");
      expect(source).toContain('from "~/test/library-harness"');
      expect(source).toContain("restoreRealTimersWithoutLeaks();");
      expect(source).not.toContain('vi.mock("~/api/client"');
      expect(source).not.toContain('"~/lib/reachability"');
    }
  });

  it("reports a pending timer and restores the real clock", () => {
    vi.useFakeTimers();
    setTimeout(() => {}, 1);

    expect(() => restoreRealTimersWithoutLeaks()).toThrow(
      "Test leaked 1 fake timer",
    );
    expect(vi.isFakeTimers()).toBe(false);
  });
});
