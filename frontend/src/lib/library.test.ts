import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type * as ApiClient from "~/api/client";
import { flush } from "solid-js";
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
  reachable: vi.fn(),
}));

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...mocks };
});
vi.mock("~/lib/toast", () => ({ toast: { show: mocks.toast } }));
vi.mock("~/lib/reachability", () => ({ isReachable: mocks.reachable }));

const { Library } = await import("~/lib/library");

function book(p: Partial<BookMeta> & { id: string; title: string }): BookMeta {
  return {
    author: "",
    language: "",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "",
    chapterCount: 0,
    progress: 0,
    ...p,
  };
}

/**
 * Builds an isolated store seeded through the real load path.
 *
 * The exported `library` is an app-lifetime singleton, and its read surface is
 * deliberately readonly - assigning `store.books = [...]` is now a compile
 * error, which is exactly what stops a component from silently no-op'ing on a
 * write. So these tests own their instance and seed it over the mocked
 * transport instead of reaching into observable state.
 */
async function seed(books: BookMeta[]): Promise<InstanceType<typeof Library>> {
  mocks.getBooks.mockResolvedValueOnce(books);
  const store = new Library();
  await store.loadForProfile("p");
  flush();
  return store;
}

// Fake timers throughout: setQuery's 140ms debounce is the only way to reach
// debouncedQuery, which is the field `visible` actually filters on.
beforeEach(() => {
  vi.useFakeTimers();
  vi.resetAllMocks();
  mocks.getFlairs.mockResolvedValue([]);
  mocks.reachable.mockReturnValue(true);
});
afterEach(() => {
  vi.useRealTimers();
});

describe("library.visible (filter + sort)", () => {
  it("sorts by title in natural numeric order", async () => {
    const store = await seed([
      book({ id: "b", title: "Book 10" }),
      book({ id: "a", title: "Book 2" }),
    ]);
    expect(store.visible.map((b) => b.title)).toEqual(["Book 2", "Book 10"]);
  });

  it("filters on the debounced query against title + author", async () => {
    const store = await seed([
      book({ id: "1", title: "Dune", author: "Herbert" }),
      book({ id: "2", title: "Hyperion", author: "Simmons" }),
    ]);
    store.setQuery("herb");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["1"]);
  });

  it("ignores the instant query - only the debounced mirror filters", async () => {
    const store = await seed([
      book({ id: "1", title: "Dune" }),
      book({ id: "2", title: "Hyperion" }),
    ]);
    store.setQuery("dune"); // not yet debounced
    flush();
    expect(store.visible).toHaveLength(2);
  });

  // Two toggles in one tick also cover the draft-mutation fix: a
  // read-then-replace setter would discard the first of the pair.
  it("applies flair filters with OR semantics", async () => {
    const store = await seed([
      book({ id: "1", title: "A", flairId: "reading" }),
      book({ id: "2", title: "B", flairId: "finished" }),
      book({ id: "3", title: "C" }),
    ]);
    store.toggleFlairFilter("reading");
    store.toggleFlairFilter("finished");
    flush();
    expect([...store.flairFilters]).toEqual(["reading", "finished"]);
    expect([...store.visible.map((b) => b.id)].sort()).toEqual(["1", "2"]);
  });

  it("sorts by progress descending, tie-broken by title", async () => {
    const store = await seed([
      book({ id: "1", title: "B", progress: 0.5 }),
      book({ id: "2", title: "A", progress: 0.5 }),
      book({ id: "3", title: "C", progress: 0.9 }),
    ]);
    store.sort = "progress";
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["3", "2", "1"]);
  });

  it("sorts by recently added (addedAt desc, missing dates last)", async () => {
    const store = await seed([
      book({ id: "1", title: "A", addedAt: "2024-01-01" }),
      book({ id: "2", title: "B", addedAt: "2024-06-01" }),
      book({ id: "3", title: "C" }),
    ]);
    store.sort = "added";
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["2", "1", "3"]);
  });

  it("sorts recently read descending with a title tie-break", async () => {
    const store = await seed([
      book({ id: "1", title: "C" }),
      book({ id: "2", title: "B", lastReadAt: "2024-06-01" }),
      book({ id: "3", title: "A", lastReadAt: "2024-06-01" }),
    ]);
    store.sort = "read";
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["3", "2", "1"]);
  });

  it("refreshes the haystack cache when a book's title changes (same id)", async () => {
    const store = await seed([book({ id: "1", title: "Dune" })]);
    store.setQuery("dune");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["1"]);

    // Reload the same id with a new title: the cache entry must refresh so the
    // stale haystack can't keep matching the old query.
    mocks.getBooks.mockResolvedValueOnce([
      book({ id: "1", title: "Foundation" }),
    ]);
    await store.refresh();
    flush();
    expect(store.visible).toHaveLength(0);

    store.setQuery("found");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["1"]);
  });
});

describe("library.setQuery (debounce)", () => {
  it("updates query instantly but debouncedQuery only after 140ms", () => {
    const store = new Library();
    store.setQuery("hello");
    flush();
    expect(store.query).toBe("hello");
    expect(store.debouncedQuery).toBe("");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.debouncedQuery).toBe("hello");
  });

  it("coalesces rapid calls - the pending timer resets, only the last lands", () => {
    const store = new Library();
    store.setQuery("a");
    vi.advanceTimersByTime(100);
    store.setQuery("ab");
    vi.advanceTimersByTime(100); // 200ms total, but timer reset at 100ms ago
    flush();
    expect(store.debouncedQuery).toBe("");
    vi.advanceTimersByTime(40); // now 140ms since the last call
    flush();
    expect(store.debouncedQuery).toBe("ab");
  });
});

describe("library flair filters", () => {
  it("toggles a filter on and off", () => {
    const store = new Library();
    store.toggleFlairFilter("reading");
    flush();
    expect([...store.flairFilters]).toEqual(["reading"]);
    store.toggleFlairFilter("reading");
    flush();
    expect([...store.flairFilters]).toEqual([]);
  });

  it("clears all filters", () => {
    const store = new Library();
    store.toggleFlairFilter("a");
    store.toggleFlairFilter("b");
    flush();
    expect([...store.flairFilters]).toEqual(["a", "b"]);
    store.clearFlairFilters();
    flush();
    expect([...store.flairFilters]).toEqual([]);
  });
});

const { ApiError } = await import("~/api/client");

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

const epub = (name = "book.epub") => new File(["x"], name);

describe("library.load errors", () => {
  it("shows the server's message when the load fails", async () => {
    const store = new Library();
    store.activate("p");
    mocks.getBooks.mockRejectedValue(new ApiError("boom", 500, "server_error"));

    await store.load();
    flush();

    expect(store.error).toBe("boom");
    expect(store.loading).toBe(false);
  });

  it("suppresses the inline error when the failure is transport-level", async () => {
    const store = new Library();
    store.activate("p");
    mocks.getBooks.mockRejectedValue(
      new ApiError("Could not reach the server.", undefined, "network_error"),
    );

    await store.load();
    flush();

    expect(store.error).toBe("");
    expect(store.books).toHaveLength(0);
    expect(store.loading).toBe(false);
  });
});

describe("library.setFlair", () => {
  it("applies optimistically and rolls back on failure with a toast", async () => {
    const store = await seed([book({ id: "a", title: "A" })]);
    const request = deferred<void>();
    mocks.setBookFlair.mockReturnValueOnce(request.promise);

    const mutation = store.setFlair("a", "reading");
    flush();
    expect(store.books[0].flairId).toBe("reading");

    request.reject(new Error("nope"));
    await mutation;
    flush();

    expect(store.books[0].flairId).toBeUndefined();
    expect(mocks.toast).toHaveBeenCalledWith("Could not update flair");
  });

  it("does not roll back over a newer overlapping change", async () => {
    const store = await seed([book({ id: "a", title: "A" })]);
    const first = deferred<void>();
    const second = deferred<void>();
    mocks.setBookFlair
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const m1 = store.setFlair("a", "reading");
    const m2 = store.setFlair("a", "finished");
    flush();

    first.reject(new Error("late failure"));
    await m1;
    flush();
    // The failed call's rollback must not clobber the newer optimistic value.
    expect(store.books[0].flairId).toBe("finished");

    second.resolve();
    await m2;
    flush();
    expect(store.books[0].flairId).toBe("finished");
  });
});

describe("library.editMetadata", () => {
  it("rejects after rolling back, without a toast", async () => {
    const store = await seed([book({ id: "a", title: "Old", author: "X" })]);
    mocks.updateBookMeta.mockRejectedValue(new ApiError("bad", 400, "invalid"));

    const mutation = store.editMetadata("a", { title: "New" });
    flush();
    expect(store.books[0].title).toBe("New");

    await expect(mutation).rejects.toThrow("bad");
    flush();

    expect(store.books[0].title).toBe("Old");
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("reconciles the server record without wiping reader-owned fields", async () => {
    const store = await seed([
      book({
        id: "a",
        title: "Old",
        progress: 0.4,
        flairId: "reading",
        lastReadAt: "2024-01-01 10:00:00",
      }),
    ]);
    // The PATCH response is a bare books-row summary: progress 0, no
    // flairId/lastReadAt, fresh updatedAt.
    mocks.updateBookMeta.mockResolvedValue({
      ...book({ id: "a", title: "Trimmed" }),
      updatedAt: "2024-06-01 00:00:00",
    });

    await store.editMetadata("a", { title: "Trimmed" });
    flush();

    expect(store.books[0]).toMatchObject({
      title: "Trimmed",
      progress: 0.4,
      flairId: "reading",
      lastReadAt: "2024-01-01 10:00:00",
      updatedAt: "2024-06-01 00:00:00",
    });
  });
});

describe("library.replaceCover", () => {
  it("takes the new cover metadata but keeps progress and flair", async () => {
    const store = await seed([
      book({ id: "a", title: "A", progress: 0.4, flairId: "reading" }),
    ]);
    mocks.uploadCover.mockResolvedValue({
      ...book({ id: "a", title: "A", hasCover: true }),
      updatedAt: "2024-06-02 00:00:00",
    });

    await store.replaceCover("a", new File(["x"], "cover.png"));
    flush();

    expect(store.books[0]).toMatchObject({
      hasCover: true,
      progress: 0.4,
      flairId: "reading",
      updatedAt: "2024-06-02 00:00:00",
    });
  });
});

describe("library.remove", () => {
  it("splices the book and drops its search haystack", async () => {
    const store = await seed([
      book({ id: "a", title: "Dune" }),
      book({ id: "b", title: "Other" }),
    ]);
    mocks.deleteBook.mockResolvedValue(undefined);

    await store.remove("a");
    flush();

    expect(store.books.map((b) => b.id)).toEqual(["b"]);
    store.setQuery("dune");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible).toHaveLength(0);
  });
});

describe("library.uploadFiles", () => {
  it("rejects a same-tick second batch with a toast", async () => {
    mocks.uploadBook.mockImplementation(() => new Promise<never>(() => {}));
    const store = new Library();
    store.activate("p");

    void store.uploadFiles([epub()]);
    await store.uploadFiles([epub("second.epub")]);
    flush();

    expect(mocks.uploadBook).toHaveBeenCalledTimes(1);
    expect(mocks.toast).toHaveBeenCalledWith(
      expect.stringContaining("Still importing"),
    );
  });

  it("toasts the per-file outcome after the refresh", async () => {
    mocks.getBooks.mockResolvedValue([]);
    mocks.uploadBook
      .mockResolvedValueOnce(book({ id: "u1", title: "U1" }))
      .mockRejectedValueOnce(new Error("bad epub"))
      .mockResolvedValueOnce(book({ id: "u3", title: "U3" }));
    const store = new Library();
    store.activate("p");

    await store.uploadFiles([epub(), epub(), epub()]);
    flush();

    const messages = mocks.toast.mock.calls.map((c) => c[0]);
    expect(messages).toContain("Added 2 books");
    expect(messages).toContain("1 file failed to import");
    expect(store.uploading).toBe(false);
  });
});

describe("library.rescan", () => {
  it("refreshes even when nothing new was imported", async () => {
    mocks.getBooks.mockResolvedValue([]);
    mocks.rescanLibrary.mockResolvedValue({ imported: 0 });
    const store = new Library();
    store.activate("p");

    await store.rescan();

    expect(mocks.getBooks).toHaveBeenCalledTimes(1);
    expect(mocks.toast).toHaveBeenCalledWith("No new books found");
  });

  it("ignores a same-tick second rescan silently", async () => {
    mocks.rescanLibrary.mockImplementation(() => new Promise<never>(() => {}));
    const store = new Library();
    store.activate("p");

    void store.rescan();
    await store.rescan();

    expect(mocks.rescanLibrary).toHaveBeenCalledTimes(1);
    expect(mocks.toast).not.toHaveBeenCalled();
  });
});

describe("library progress publisher guards", () => {
  it("rejects out-of-range and non-numeric input", async () => {
    const store = await seed([book({ id: "a", title: "A", chapterCount: 4 })]);
    const publish = store.createReadingProgressPublisher("p", "a");

    publish(4, 0.5, "2024-01-01 00:00:00");
    publish(-1, 0.5, "2024-01-01 00:00:00");
    publish(1.5, 0.5, "2024-01-01 00:00:00");
    publish(1, NaN, "2024-01-01 00:00:00");
    flush();

    expect(store.books[0].progress).toBe(0);
    expect(store.books[0].lastReadAt).toBeUndefined();
  });

  it("clamps an out-of-range percent", async () => {
    const store = await seed([book({ id: "a", title: "A", chapterCount: 4 })]);
    const publish = store.createReadingProgressPublisher("p", "a");

    publish(1, 5, "2024-01-01 00:00:00");
    flush();

    expect(store.books[0].progress).toBe(0.5);
    expect(store.books[0].lastReadAt).toBe("2024-01-01 00:00:00");
  });

  it("stamps the default readAt in the server's own format", async () => {
    const store = await seed([book({ id: "a", title: "A", chapterCount: 4 })]);
    const publish = store.createReadingProgressPublisher("p", "a");

    publish(1, 0.5);
    flush();

    expect(store.books[0].lastReadAt).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/,
    );
  });
});

describe("library.activate clearing contract", () => {
  it("clears query, filters, and a pending debounce timer", async () => {
    const store = new Library();
    store.activate("p");
    store.setQuery("dune");
    store.toggleFlairFilter("reading");

    store.activate("q");
    vi.advanceTimersByTime(200);
    flush();

    expect(store.query).toBe("");
    expect(store.debouncedQuery).toBe("");
    expect(store.flairFilters).toHaveLength(0);
  });

  it("keeps the sort preference across a profile switch", async () => {
    const store = new Library();
    store.sort = "progress";
    flush();

    store.activate("q");

    expect(store.sort).toBe("progress");
  });
});

describe("library sort and cache edge cases", () => {
  it("sorts by author with a title tie-break", async () => {
    const store = await seed([
      book({ id: "1", title: "Z", author: "B" }),
      book({ id: "2", title: "Y", author: "A" }),
      book({ id: "3", title: "X", author: "A" }),
    ]);
    store.sort = "author";
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["3", "2", "1"]);
  });

  it("refreshes the haystack cache when only the author changes", async () => {
    const store = await seed([book({ id: "1", title: "T", author: "X" })]);
    store.setQuery("x");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible.map((b) => b.id)).toEqual(["1"]);

    mocks.getBooks.mockResolvedValueOnce([
      book({ id: "1", title: "T", author: "Y" }),
    ]);
    await store.refresh();
    flush();
    expect(store.visible).toHaveLength(0);
  });
});

describe("library.refresh loading state", () => {
  it("keeps the populated shelf visible during a refresh", async () => {
    const store = await seed([book({ id: "a", title: "A" })]);
    mocks.getBooks.mockResolvedValueOnce([book({ id: "a", title: "A" })]);

    const refresh = store.refresh();
    expect(store.loading).toBe(false);
    await refresh;
  });

  it("shows the loading state on the initial load", async () => {
    const request = deferred<BookMeta[]>();
    mocks.getBooks.mockReturnValueOnce(request.promise);
    const store = new Library();
    store.activate("p");

    const load = store.load();
    flush();
    expect(store.loading).toBe(true);

    request.resolve([]);
    await load;
    flush();
    expect(store.loading).toBe(false);
  });
});

describe("library.visible (H5: split sort/filter memos)", () => {
  it("constructs without reading a field that is not assigned yet", () => {
    // Regression for the memo-ordering trap the split exposed. `visible` read
    // `this.#sorted()` inside its own memo body; createMemo evaluates eagerly,
    // so that body ran during the constructor -- before runWithOwner returned
    // and the field was assigned -- and every instantiation threw. The sorted
    // memo is now a local const passed in as an argument.
    expect(() => new Library()).not.toThrow();
  });

  it("keeps the sorted order once the query narrows the list", async () => {
    // Filtering is order-preserving, which is the invariant that lets `visible`
    // skip re-sorting entirely.
    const store = await seed([
      book({ id: "c", title: "Book 3", author: "Zed" }),
      book({ id: "a", title: "Book 1", author: "Zed" }),
      book({ id: "b", title: "Book 2", author: "Other" }),
    ]);
    expect(store.visible.map((b) => b.title)).toEqual([
      "Book 1",
      "Book 2",
      "Book 3",
    ]);

    store.setQuery("zed");
    vi.advanceTimersByTime(140);
    flush();
    expect(store.visible.map((b) => b.title)).toEqual(["Book 1", "Book 3"]);
  });

  it("re-sorts when the sort key changes, with the filter still applied", async () => {
    const store = await seed([
      book({ id: "a", title: "Book 1", author: "Zed" }),
      book({ id: "b", title: "Book 2", author: "Other" }),
      book({ id: "c", title: "Book 3", author: "Abe" }),
    ]);
    store.setQuery("book");
    vi.advanceTimersByTime(140);
    flush();

    store.sort = "author";
    flush();
    expect(store.visible.map((b) => b.author)).toEqual(["Abe", "Other", "Zed"]);

    store.sort = "title";
    flush();
    expect(store.visible.map((b) => b.title)).toEqual([
      "Book 1",
      "Book 2",
      "Book 3",
    ]);
  });
});

describe("library.addCustomFlair (M2)", () => {
  it("returns null for a blank label without reaching the transport", async () => {
    const store = await seed([]);
    await expect(store.addCustomFlair("   ")).resolves.toBeNull();
    expect(mocks.createFlair).not.toHaveBeenCalled();
  });

  it("returns null and surfaces a toast when the request fails", async () => {
    // The caller clears the text the user typed only on a non-null return, so
    // a failed create must be distinguishable from a successful one.
    const store = await seed([]);
    mocks.createFlair.mockRejectedValueOnce(new Error("offline"));
    await expect(store.addCustomFlair("Favourites")).resolves.toBeNull();
    expect(mocks.toast).toHaveBeenCalled();
    expect(store.customFlairs).toHaveLength(0);
  });

  it("returns the created flair and appends it exactly once", async () => {
    const store = await seed([]);
    const flair = { id: "cf1", label: "Favourites", color: "#3b82f6" };
    mocks.createFlair.mockResolvedValueOnce(flair);
    await expect(store.addCustomFlair("Favourites")).resolves.toEqual(flair);
    expect(store.customFlairs.map((f) => f.id)).toEqual(["cf1"]);
  });
});
