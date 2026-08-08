import { vi } from "vitest";
import type * as ApiClient from "~/api/client";

/**
 * The store and route suites exercise one library transport. Keep that seam in
 * one module so adding an endpoint cannot leave one suite on a stale partial
 * mock. Import this module before dynamically importing either production
 * module; the real client is spread so ApiError keeps its runtime identity.
 */
export const libraryApi = {
  getBooks: vi.fn(),
  getFlairs: vi.fn(),
  createFlair: vi.fn(),
  deleteFlair: vi.fn(),
  setBookFlair: vi.fn(),
  uploadBook: vi.fn(),
  updateBookMeta: vi.fn(),
  uploadCover: vi.fn(),
  deleteBook: vi.fn(),
  rescanLibrary: vi.fn(),
};

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...libraryApi };
});

/**
 * Restore the real clock even when the assertion fails, so one leaking test
 * cannot freeze the next suite's Solid scheduler behind fake microtasks.
 */
export function restoreRealTimersWithoutLeaks(): void {
  const pending = vi.getTimerCount();
  vi.clearAllTimers();
  vi.useRealTimers();
  if (pending !== 0) {
    throw new Error(
      `Test leaked ${pending} fake timer${pending === 1 ? "" : "s"}`,
    );
  }
}
