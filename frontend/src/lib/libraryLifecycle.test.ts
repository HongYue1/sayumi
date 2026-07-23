import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BookMeta } from "~/api/client";

const mocks = vi.hoisted(() => ({
  getBooks: vi.fn(),
  uploadBook: vi.fn(),
  updateBookMeta: vi.fn(),
  uploadCover: vi.fn(),
  deleteBook: vi.fn(),
  rescanLibrary: vi.fn(),
  getFlairs: vi.fn(),
  createFlair: vi.fn(),
  deleteFlair: vi.fn(),
  setBookFlair: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("~/api/client", () => ({
  ...mocks,
  ApiError: class ApiError extends Error {},
}));
vi.mock("~/lib/toast.svelte", () => ({ toast: { show: mocks.toast } }));
vi.mock("~/lib/reachability", () => ({ isReachable: () => true }));

const { Library } = await import("~/lib/library.svelte");

function book(id: string, title: string): BookMeta {
  return {
    id,
    title,
    author: "",
    language: "",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "",
    chapterCount: 0,
    progress: 0,
  } as BookMeta;
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

describe("library profile lifecycle", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    mocks.getFlairs.mockResolvedValue([]);
  });

  it("clears old data and drops a stale profile load", async () => {
    const first = deferred<BookMeta[]>();
    const second = deferred<BookMeta[]>();
    mocks.getBooks
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const store = new Library();

    const profileALoad = store.loadForProfile("profile-a");
    const profileBLoad = store.loadForProfile("profile-b");
    expect(store.books).toEqual([]);

    second.resolve([book("b", "Profile B")]);
    await profileBLoad;
    first.resolve([book("a", "Profile A")]);
    await profileALoad;

    expect(store.books.map((item) => item.id)).toEqual(["b"]);
  });

  it("deduplicates concurrent loads for the active profile", async () => {
    const request = deferred<BookMeta[]>();
    mocks.getBooks.mockReturnValueOnce(request.promise);
    const store = new Library();
    store.activate("profile-a");

    const first = store.load();
    const second = store.load();
    expect(mocks.getBooks).toHaveBeenCalledTimes(1);

    request.resolve([]);
    await Promise.all([first, second]);
    await store.load();
    expect(mocks.getBooks).toHaveBeenCalledTimes(1);
  });

  it("does not let a stale mutation rollback the new profile", async () => {
    const request = deferred<void>();
    mocks.setBookFlair.mockReturnValueOnce(request.promise);
    const store = new Library();
    store.activate("profile-a");
    store.books = [book("a", "Profile A")];

    const mutation = store.setFlair("a", "reading");
    store.activate("profile-b");
    store.books = [book("b", "Profile B")];
    request.reject(new Error("late failure"));
    await mutation;

    expect(store.books.map((item) => item.id)).toEqual(["b"]);
    expect(mocks.toast).not.toHaveBeenCalled();
  });
});
