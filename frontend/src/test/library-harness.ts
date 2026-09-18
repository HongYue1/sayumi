import { vi } from "vitest";
import type * as ApiClient from "~/api/client";

/**
 * The store and route suites exercise one library transport. Keep that seam in
 * one module so adding an endpoint cannot leave one suite on a stale partial
 * mock. Import this module before dynamically importing either production
 * module; the real client is spread so ApiError keeps its runtime identity.
 *
 * Each double is typed against the real client export it stands in for, so a
 * signature change in ~/api/client breaks the suites at typecheck instead of
 * leaving them green against doubles of an interface that no longer exists.
 */
export const libraryApi = {
  getBooks: vi.fn<typeof ApiClient.getBooks>(),
  getFlairs: vi.fn<typeof ApiClient.getFlairs>(),
  createFlair: vi.fn<typeof ApiClient.createFlair>(),
  deleteFlair: vi.fn<typeof ApiClient.deleteFlair>(),
  setBookFlair: vi.fn<typeof ApiClient.setBookFlair>(),
  uploadBook: vi.fn<typeof ApiClient.uploadBook>(),
  updateBookMeta: vi.fn<typeof ApiClient.updateBookMeta>(),
  uploadCover: vi.fn<typeof ApiClient.uploadCover>(),
  deleteBook: vi.fn<typeof ApiClient.deleteBook>(),
  rescanLibrary: vi.fn<typeof ApiClient.rescanLibrary>(),
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
