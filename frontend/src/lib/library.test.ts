import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { library } from "~/lib/library.svelte";
import type { BookMeta } from "~/api/client";

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

// `library` is an app-lifetime singleton; reset the reactive fields we touch.
function reset(): void {
  library.books = [];
  library.query = "";
  library.debouncedQuery = "";
  library.sort = "title";
  library.flairFilters = [];
  library.customFlairs = [];
}

describe("library.visible (filter + sort)", () => {
  beforeEach(reset);

  it("sorts by title in natural numeric order", () => {
    library.books = [
      book({ id: "b", title: "Book 10" }),
      book({ id: "a", title: "Book 2" }),
    ];
    expect(library.visible.map((b) => b.title)).toEqual(["Book 2", "Book 10"]);
  });

  it("filters on the debounced query against title + author", () => {
    library.books = [
      book({ id: "1", title: "Dune", author: "Herbert" }),
      book({ id: "2", title: "Hyperion", author: "Simmons" }),
    ];
    library.debouncedQuery = "herb";
    expect(library.visible.map((b) => b.id)).toEqual(["1"]);
  });

  it("ignores the instant query — only the debounced mirror filters", () => {
    library.books = [
      book({ id: "1", title: "Dune" }),
      book({ id: "2", title: "Hyperion" }),
    ];
    library.query = "dune"; // not yet debounced
    expect(library.visible).toHaveLength(2);
  });

  it("applies flair filters with OR semantics", () => {
    library.books = [
      book({ id: "1", title: "A", flairId: "reading" }),
      book({ id: "2", title: "B", flairId: "finished" }),
      book({ id: "3", title: "C" }),
    ];
    library.flairFilters = ["reading", "finished"];
    expect([...library.visible.map((b) => b.id)].sort()).toEqual(["1", "2"]);
  });

  it("sorts by progress descending, tie-broken by title", () => {
    library.books = [
      book({ id: "1", title: "B", progress: 0.5 }),
      book({ id: "2", title: "A", progress: 0.5 }),
      book({ id: "3", title: "C", progress: 0.9 }),
    ];
    library.sort = "progress";
    expect(library.visible.map((b) => b.id)).toEqual(["3", "2", "1"]);
  });

  it("sorts by recently added (addedAt desc, missing dates last)", () => {
    library.books = [
      book({ id: "1", title: "A", addedAt: "2024-01-01" }),
      book({ id: "2", title: "B", addedAt: "2024-06-01" }),
      book({ id: "3", title: "C" }),
    ];
    library.sort = "added";
    expect(library.visible.map((b) => b.id)).toEqual(["2", "1", "3"]);
  });

  it("refreshes the haystack cache when a book's title changes (same id)", () => {
    library.books = [book({ id: "1", title: "Dune" })];
    library.debouncedQuery = "dune";
    expect(library.visible.map((b) => b.id)).toEqual(["1"]);

    // Replace the array, same id, new title: the cache entry must refresh so the
    // stale haystack can't keep matching the old query.
    library.books = [book({ id: "1", title: "Foundation" })];
    expect(library.visible).toHaveLength(0);
    library.debouncedQuery = "found";
    expect(library.visible.map((b) => b.id)).toEqual(["1"]);
  });
});

describe("library.setQuery (debounce)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    reset();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("updates query instantly but debouncedQuery only after 140ms", () => {
    library.setQuery("hello");
    expect(library.query).toBe("hello");
    expect(library.debouncedQuery).toBe("");
    vi.advanceTimersByTime(140);
    expect(library.debouncedQuery).toBe("hello");
  });

  it("coalesces rapid calls — the pending timer resets, only the last lands", () => {
    library.setQuery("a");
    vi.advanceTimersByTime(100);
    library.setQuery("ab");
    vi.advanceTimersByTime(100); // 200ms total, but timer reset at 100ms ago
    expect(library.debouncedQuery).toBe("");
    vi.advanceTimersByTime(40); // now 140ms since the last call
    expect(library.debouncedQuery).toBe("ab");
  });
});

describe("library flair filters", () => {
  beforeEach(reset);

  it("toggles a filter on and off", () => {
    library.toggleFlairFilter("reading");
    expect(library.flairFilters).toEqual(["reading"]);
    library.toggleFlairFilter("reading");
    expect(library.flairFilters).toEqual([]);
  });

  it("clears all filters", () => {
    library.flairFilters = ["a", "b"];
    library.clearFlairFilters();
    expect(library.flairFilters).toEqual([]);
  });
});
