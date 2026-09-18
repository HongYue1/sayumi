import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import type { BookMeta } from "~/api/client";
import {
  libraryApi,
  restoreRealTimersWithoutLeaks,
} from "~/test/library-harness";

const toast = vi.hoisted(() => vi.fn());
vi.mock("~/lib/toast", () => ({ toast: { show: toast } }));

const { Library } = await import("~/lib/library");

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
  };
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
    vi.useFakeTimers();
    vi.resetAllMocks();
    libraryApi.getFlairs.mockResolvedValue([]);
  });

  afterEach(() => {
    restoreRealTimersWithoutLeaks();
  });

  it("clears old data and drops a stale profile load", async () => {
    const first = deferred<BookMeta[]>();
    const second = deferred<BookMeta[]>();
    libraryApi.getBooks
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const store = new Library();

    const profileALoad = store.loadForProfile("profile-a");
    const profileBLoad = store.loadForProfile("profile-b");
    flush();
    expect([...store.books]).toEqual([]);

    second.resolve([book("b", "Profile B")]);
    await profileBLoad;
    first.resolve([book("a", "Profile A")]);
    await profileALoad;
    flush();

    expect(store.books.map((item) => item.id)).toEqual(["b"]);
  });

  it("deduplicates concurrent loads for the active profile", async () => {
    const request = deferred<BookMeta[]>();
    libraryApi.getBooks.mockReturnValueOnce(request.promise);
    const store = new Library();
    store.activate("profile-a");

    const first = store.load();
    const second = store.load();
    expect(libraryApi.getBooks).toHaveBeenCalledTimes(1);

    request.resolve([]);
    await Promise.all([first, second]);
    await store.load();
    expect(libraryApi.getBooks).toHaveBeenCalledTimes(1);
  });

  it("chains overlapping refreshes so each gets its own post-mutation read", async () => {
    const first = deferred<BookMeta[]>();
    const second = deferred<BookMeta[]>();
    libraryApi.getBooks
      .mockReturnValueOnce(Promise.resolve([book("a", "A")]))
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const store = new Library();
    await store.loadForProfile("p");
    flush();

    const r1 = store.refresh();
    const r2 = store.refresh();
    // r1's read predates whatever mutation r2's caller has committed, so r2
    // must wait for r1 and then read again itself — deduping onto r1 would
    // resolve r2 with the stale list and nothing would ever refetch.
    first.resolve([book("a", "pre-mutation")]);
    await r1;
    flush();

    second.resolve([book("a", "post-mutation"), book("b", "new")]);
    await r2;
    flush();

    expect(libraryApi.getBooks).toHaveBeenCalledTimes(3);
    expect(store.books.map((item) => item.id)).toEqual(["a", "b"]);
  });

  it("does not let a stale mutation rollback the new profile", async () => {
    const request = deferred<void>();
    libraryApi.setBookFlair.mockReturnValueOnce(request.promise);
    libraryApi.getBooks
      .mockResolvedValueOnce([book("a", "Profile A")])
      .mockResolvedValueOnce([book("b", "Profile B")]);
    const store = new Library();
    await store.loadForProfile("profile-a");
    flush();

    const mutation = store.setFlair("a", "reading");
    await store.loadForProfile("profile-b");
    flush();
    request.reject(new Error("late failure"));
    await mutation;
    flush();

    expect(store.books.map((item) => item.id)).toEqual(["b"]);
    expect(toast).not.toHaveBeenCalled();
  });

  it("publishes reader progress into the active profile immediately", async () => {
    libraryApi.getBooks.mockResolvedValueOnce([
      { ...book("a", "Book A"), chapterCount: 4 },
    ]);
    const store = new Library();
    await store.loadForProfile("profile-a");
    flush();
    const publish = store.createReadingProgressPublisher("profile-a", "a");

    publish(1, 0.5, "2024-06-01T12:00:00.000Z");
    flush();

    expect(store.books[0]).toMatchObject({
      progress: 0.375,
      lastReadAt: "2024-06-01T12:00:00.000Z",
    });
  });

  it("drops a reader update while a different profile is active", async () => {
    libraryApi.getBooks
      .mockResolvedValueOnce([{ ...book("a", "Book A"), chapterCount: 4 }])
      // Same book id existing under profile B proves the guard is the
      // profile binding, not a lucky book-id miss.
      .mockResolvedValueOnce([{ ...book("a", "B's copy"), chapterCount: 4 }]);
    const store = new Library();
    await store.loadForProfile("profile-a");
    flush();
    const stalePublish = store.createReadingProgressPublisher("profile-a", "a");

    await store.loadForProfile("profile-b");
    flush();
    stalePublish(3, 1, "2024-06-01T12:00:00.000Z");
    flush();

    expect(store.books[0]).toMatchObject({ progress: 0 });
  });

  it("publishes when created before activate (hard refresh into /read)", async () => {
    // On a hard refresh straight into the reader, Read initializes (creating
    // the publisher) BEFORE App's effect runs activate(), which bumps the
    // internal generation. The publisher is bound to the profile NAME exactly
    // so this ordering still works — a generation captured at creation would
    // be stale on arrival and the publisher dead for the whole session.
    libraryApi.getBooks.mockResolvedValueOnce([
      { ...book("a", "Book A"), chapterCount: 4 },
    ]);
    const store = new Library();
    const publish = store.createReadingProgressPublisher("profile-a", "a");
    await store.loadForProfile("profile-a");
    flush();

    publish(1, 0.5, "2024-06-01T12:00:00.000Z");
    flush();

    expect(store.books[0]).toMatchObject({
      progress: 0.375,
      lastReadAt: "2024-06-01T12:00:00.000Z",
    });
  });
});

describe("library profile lifecycle - timer hygiene", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    restoreRealTimersWithoutLeaks();
  });

  it("clears a pending debounce timer on profile switch", () => {
    const store = new Library();
    store.activate("a");
    store.setQuery("pending query");

    store.activate("b");
    vi.advanceTimersByTime(200);
    flush();

    expect(store.debouncedQuery).toBe("");
  });
});
