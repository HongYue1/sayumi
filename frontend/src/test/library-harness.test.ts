import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type * as ApiClient from "~/api/client";
import {
  libraryApi,
  restoreRealTimersWithoutLeaks,
} from "~/test/library-harness";

const SOURCE_ROOT = join(process.cwd(), "src");

/** Every suite sharing the transport seam, discovered by import. */
function seamConsumers(): string[] {
  const self = "src/test/library-harness.test.ts";
  const out: string[] = [];
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (
        entry.name.endsWith(".test.ts") &&
        readFileSync(path, "utf8").includes('from "~/test/library-harness"')
      )
        out.push(path);
    }
  };
  walk(SOURCE_ROOT);
  // POSIX form, matching the census assertions elsewhere in the suite. The
  // seam's own test imports the harness to exercise it, not to consume it.
  return out
    .map((path) => relative(process.cwd(), path).replaceAll("\\", "/"))
    .filter((path) => path !== self)
    .sort();
}

afterEach(() => {
  if (!vi.isFakeTimers()) return;
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe("library test harness", () => {
  it("mirrors real client exports", async () => {
    // A signature change in ~/api/client must break the seam here, not leave
    // the suites green against doubles of an interface that no longer exists
    // (the doubles themselves are typed against the client for the same
    // reason). importActual sees past this file's own vi.mock call.
    const real = await vi.importActual<typeof ApiClient>("~/api/client");
    for (const key of Object.keys(libraryApi) as (keyof typeof libraryApi)[]) {
      expect(typeof real[key], `${key} is not a client export`).toBe(
        "function",
      );
    }
  });

  it("intercepts the client module the suites import", async () => {
    const mocked = await import("~/api/client");
    for (const key of Object.keys(libraryApi) as (keyof typeof libraryApi)[]) {
      expect(mocked[key]).toBe(libraryApi[key]);
    }
  });

  it("keeps every consumer on the shared seam", () => {
    const consumers = seamConsumers();
    // Fail closed in both directions: a suite that stops importing the seam
    // silently drops out of this policing, so the roster is pinned too.
    expect(consumers).toEqual([
      "src/lib/library.test.ts",
      "src/lib/libraryLifecycle.test.ts",
      "src/routes/Library.test.ts",
    ]);
    for (const path of consumers) {
      const source = readFileSync(join(process.cwd(), path), "utf8");
      // Rogue doubles bypass the seam: a hand-rolled client mock, or
      // reachability mocked as a proxy for client internals, hides the drift
      // the seam exists to surface.
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
