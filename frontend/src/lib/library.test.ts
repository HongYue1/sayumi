import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
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
}));

vi.mock("~/api/client", () => ({
  ...mocks,
  ApiError: class ApiError extends Error {},
}));
vi.mock("~/lib/toast", () => ({ toast: { show: mocks.toast } }));
vi.mock("~/lib/reachability", () => ({ isReachable: () => true }));

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
  } as BookMeta;
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
