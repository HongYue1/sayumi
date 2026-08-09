// SearchPanel.test.ts -- mounts the real panel with @solidjs/web's render (the
// ChapterFrame/SettingsPanel pattern; @solidjs/testing-library's dist still
// imports the removed "solid-js/web" specifier).
//
// searchBook is the panel's only collaborator and test-setup.ts fails any test
// that lets a fetch escape, so it is replaced at the module boundary. The mock
// spreads the real module: ApiError and every other named export in the graph
// keeps resolving from the same instance.
//
// The 300ms debounce runs on fake timers, so advance(300) is the only way a
// query reaches the API. That also makes the teardown observable: if the panel
// closes without clearing the timer, the request lands after unmount.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type * as ApiClient from "~/api/client";
import SearchPanel from "~/components/reader/SearchPanel";

const api = vi.hoisted(() => ({
  searchBook:
    vi.fn<
      (
        bookId: string,
        query: string,
        cursor?: string,
        limit?: number,
        signal?: AbortSignal,
      ) => Promise<ApiClient.SearchResponse>
    >(),
}));

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});

// happy-dom has no layout engine; the arrow-key path scrolls the new option.
if (typeof Element.prototype.scrollIntoView !== "function") {
  Element.prototype.scrollIntoView = (): void => {};
}

/** A result the validator accepts: "the |needle| here" in chapter 1 of 5. */
function result(
  over: Partial<ApiClient.SearchResult> = {},
): ApiClient.SearchResult {
  return {
    chapterIndex: 0,
    charOffset: 100,
    matchLen: 6,
    snippet: "the needle here",
    snippetStart: 4,
    snippetLen: 6,
    ...over,
  };
}

function page(
  results: ApiClient.SearchResult[],
  hasMore = false,
  nextCursor?: string,
): ApiClient.SearchResponse {
  return { results, hasMore, nextCursor };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: Error) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

let dispose: (() => void) | undefined;
let container: HTMLDivElement;
const onclose = vi.fn();
const onresultclick = vi.fn();

function mount(chapterCount = 5): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  dispose = render(
    () =>
      SearchPanel({
        bookId: "book-1",
        chapterCount,
        onresultclick,
        onclose,
      }),
    container,
  );
  flush();
}

function unmount(): void {
  dispose?.();
  dispose = undefined;
  container.remove();
}

/** Drains the promise chains onSettled kicks off, flushing between ticks. */
async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function el(selector: string): HTMLElement {
  const found = container.querySelector<HTMLElement>(selector);
  if (!found) throw new Error(`no element matched ${selector}`);
  return found;
}

function all<T extends Element>(selector: string): T[] {
  return [...container.querySelectorAll<T>(selector)];
}

function field(): HTMLInputElement {
  return el("input.field") as HTMLInputElement;
}

function typeQuery(value: string): void {
  const input = field();
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flush();
}

function press(key: string): void {
  field().dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  flush();
}

function advance(ms: number): void {
  vi.advanceTimersByTime(ms);
  flush();
}

function rows(): HTMLButtonElement[] {
  return all<HTMLButtonElement>("button.srp-result");
}

function marks(): string[] {
  return all("button.srp-result mark").map((m) => m.textContent ?? "");
}

function stateText(): string {
  return el(".srp-state").textContent?.trim() ?? "";
}

beforeEach(() => {
  vi.clearAllMocks();
  // clearAllMocks only drops call history: mockReset also discards any
  // mockResolvedValueOnce a previous test queued but never consumed, which
  // would otherwise answer the first search of the next test.
  api.searchBook.mockReset();
  vi.useFakeTimers();
  api.searchBook.mockResolvedValue(page([]));
});

afterEach(() => {
  if (dispose) unmount();
  vi.useRealTimers();
});

describe("reader search panel", () => {
  it("renders grouped results once the debounce fires", async () => {
    // Also the guard for the teardown shape: onCleanup() inside onSettled
    // throws CLEANUP_IN_FORBIDDEN_SCOPE, and that uncaught throw halts the
    // reactive system, so nothing below this line would ever render.
    api.searchBook.mockResolvedValue(
      page([result(), result({ chapterIndex: 1, charOffset: 200 })]),
    );
    mount();
    await settle();
    typeQuery("needle");
    expect(api.searchBook).not.toHaveBeenCalled();
    advance(300);
    await settle();
    expect(api.searchBook).toHaveBeenCalledWith(
      "book-1",
      "needle",
      undefined,
      100,
      expect.any(AbortSignal),
    );
    expect(rows()).toHaveLength(2);
    expect(marks()).toEqual(["needle", "needle"]);
    expect(all(".srp-group")).toHaveLength(2);
  });

  it("clears the pending debounce when the panel closes", async () => {
    mount();
    await settle();
    typeQuery("needle");
    advance(100);
    expect(api.searchBook).not.toHaveBeenCalled();
    unmount();
    advance(500);
    await settle();
    expect(api.searchBook).not.toHaveBeenCalled();
  });

  it("aborts the in-flight search when the panel closes", async () => {
    const pending = deferred<ApiClient.SearchResponse>();
    let seen: AbortSignal | undefined;
    api.searchBook.mockImplementation(
      (_bookId, _query, _cursor, _limit, signal) => {
        seen = signal;
        return pending.promise;
      },
    );
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(seen?.aborted).toBe(false);
    unmount();
    expect(seen?.aborted).toBe(true);
    pending.reject(new DOMException("aborted", "AbortError"));
    await settle();
  });

  it("slices the snippet at code-point offsets, not UTF-16 units", async () => {
    // internal/epub/search.go computes snippetStart/snippetLen over []rune and
    // iframe/searchHighlight.ts pins the same code-point contract, so a
    // UTF-16 slice would shift <mark> one unit per surrogate pair and
    // could split a pair into a lone surrogate.
    api.searchBook.mockResolvedValue(
      page([
        result({ snippet: "🙂🙂🙂needle", snippetStart: 3, snippetLen: 6 }),
      ]),
    );
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(marks()).toEqual(["needle"]);
    // clippedStart is snippetStart > 0, so the leading ellipsis is expected;
    // a UTF-16 slice would also add a trailing one (matchEnd 9 < 15 units).
    expect(el(".srp-snippet").textContent).toBe("…🙂🙂🙂needle");
  });

  it("keeps an astral character inside the match intact", async () => {
    api.searchBook.mockResolvedValue(
      page([result({ snippet: "tail 🙂🙂", snippetStart: 5, snippetLen: 2 })]),
    );
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(marks()).toEqual(["🙂🙂"]);
    expect(el(".srp-snippet").textContent).toBe("…tail 🙂🙂");
  });

  it("drops a result whose snippetStart is past the last code point", async () => {
    // Two code points, four UTF-16 units: a UTF-16 bound admits 3 and renders
    // an empty <mark> after the entire snippet.
    api.searchBook.mockResolvedValue(
      page([result({ snippet: "🙂🙂", snippetStart: 3, snippetLen: 1 })]),
    );
    mount();
    await settle();
    typeQuery("q");
    advance(300);
    await settle();
    expect(rows()).toHaveLength(0);
    expect(stateText()).toBe("No results for “q”.");
  });

  it("rejects every result while the chapter count is unknown", async () => {
    // Read.tsx now gates the panel on book(), so chapterCount is never 0 in
    // practice; the validator holds the invariant as defence in depth.
    api.searchBook.mockResolvedValue(page([result(), result()]));
    mount(0);
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(rows()).toHaveLength(0);
    expect(stateText()).toBe("No results for “needle”.");
  });

  it("keeps paging when every row on a page is rejected", async () => {
    api.searchBook
      .mockResolvedValueOnce(page([result()], true, "c1"))
      .mockResolvedValueOnce(page([result({ chapterIndex: 99 })], true, "c2"))
      .mockResolvedValueOnce(page([result({ charOffset: 300 })], false));
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(rows()).toHaveLength(1);
    el(".srp-more").click();
    await settle();
    // The validator dropped the whole page, but the server still has matches:
    // ending paging here strands the rest of the book behind one bad page.
    expect(api.searchBook).toHaveBeenNthCalledWith(
      2,
      "book-1",
      "needle",
      "c1",
      100,
      expect.any(AbortSignal),
    );
    expect(rows()).toHaveLength(1);
    el(".srp-more").click();
    await settle();
    expect(api.searchBook).toHaveBeenNthCalledWith(
      3,
      "book-1",
      "needle",
      "c2",
      100,
      expect.any(AbortSignal),
    );
    expect(rows()).toHaveLength(2);
    expect(all(".srp-more")).toHaveLength(0);
  });

  it("closes on the first Escape even with a query typed", async () => {
    mount();
    await settle();
    typeQuery("needle");
    press("Escape");
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(field().value).toBe("needle");
  });

  it("closes on Escape with an empty query", async () => {
    mount();
    await settle();
    press("Escape");
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("reports the listbox as expanded before any results arrive", async () => {
    mount();
    await settle();
    expect(field().getAttribute("aria-expanded")).toBe("true");
    typeQuery("needle");
    advance(300);
    await settle();
    expect(stateText()).toBe("No results for “needle”.");
    expect(field().getAttribute("aria-expanded")).toBe("true");
  });

  it("abandons a stale response when the query changes", async () => {
    const first = deferred<ApiClient.SearchResponse>();
    const signals: Array<AbortSignal | undefined> = [];
    api.searchBook
      .mockImplementationOnce((_bookId, _query, _cursor, _limit, signal) => {
        signals.push(signal);
        return first.promise;
      })
      .mockImplementationOnce((_bookId, _query, _cursor, _limit, signal) => {
        signals.push(signal);
        return Promise.resolve(
          page([
            result({ snippet: "fresh needle", snippetStart: 6, snippetLen: 6 }),
          ]),
        );
      });
    mount();
    await settle();
    typeQuery("need");
    advance(300);
    await settle();
    typeQuery("needle");
    // Aborted on the keystroke, not after the next debounce elapses.
    expect(signals[0]?.aborted).toBe(true);
    advance(300);
    await settle();
    first.resolve(
      page([
        result({ snippet: "stale needle", snippetStart: 6, snippetLen: 6 }),
      ]),
    );
    await settle();
    expect(rows()).toHaveLength(1);
    expect(el(".srp-snippet").textContent).toBe("…fresh needle");
  });

  it("wraps the active option and re-asserts it after load more", async () => {
    api.searchBook
      .mockResolvedValueOnce(
        page([result(), result({ charOffset: 200 })], true, "c1"),
      )
      .mockResolvedValueOnce(page([result({ charOffset: 300 })], false));
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(field().getAttribute("aria-activedescendant")).toBe("sr-0");
    press("ArrowDown");
    expect(field().getAttribute("aria-activedescendant")).toBe("sr-1");
    expect(rows()[1].getAttribute("aria-selected")).toBe("true");
    expect(rows()[0].getAttribute("aria-selected")).toBe("false");
    press("ArrowDown");
    expect(field().getAttribute("aria-activedescendant")).toBe("sr-0");
    // aria-selected has to track the wrap in lockstep. setCurrentIdx is not
    // visible to a synchronous read in the same handler, so syncing without a
    // flush marks the option the user just left -- measured as ad=sr-0 with
    // sr-1 still aria-selected=true.
    expect(rows()[0].getAttribute("aria-selected")).toBe("true");
    expect(rows()[1].getAttribute("aria-selected")).toBe("false");
    el(".srp-more").click();
    await settle();
    expect(rows()).toHaveLength(3);
    // appendRawResults replaces the last group object, so the keyed <For>
    // recreates its rows with the static aria-selected="false"; the
    // directly-mutated active option has to be re-applied afterwards.
    expect(rows()[0].getAttribute("aria-selected")).toBe("true");
    press("ArrowUp");
    expect(field().getAttribute("aria-activedescendant")).toBe("sr-2");
    expect(rows()[2].getAttribute("aria-selected")).toBe("true");
    expect(rows()[0].getAttribute("aria-selected")).toBe("false");
  });

  it("moves the active option to a clicked result", async () => {
    api.searchBook.mockResolvedValue(
      page([result(), result({ charOffset: 200 })]),
    );
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    rows()[1].click();
    expect(onresultclick).toHaveBeenCalledTimes(1);
    expect(onresultclick).toHaveBeenCalledWith(
      expect.objectContaining({ charOffset: 200 }),
      "needle",
    );
    expect(field().getAttribute("aria-activedescendant")).toBe("sr-1");
    expect(rows()[1].getAttribute("aria-selected")).toBe("true");
    expect(rows()[0].getAttribute("aria-selected")).toBe("false");
  });
});

describe("reader search panel: paging from the empty arm", () => {
  it("keeps Load more reachable when a whole first page is rejected", async () => {
    // The validator drops every row of page one, but the server still holds
    // matches: the empty arm must not hide the only way to reach them.
    api.searchBook
      .mockResolvedValueOnce(page([result({ chapterIndex: 99 })], true, "c1"))
      .mockResolvedValueOnce(page([result({ charOffset: 300 })], false));
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(rows()).toHaveLength(0);
    const more = el(".srp-more");
    more.click();
    await settle();
    expect(api.searchBook).toHaveBeenNthCalledWith(
      2,
      "book-1",
      "needle",
      "c1",
      100,
      expect.any(AbortSignal),
    );
    expect(rows()).toHaveLength(1);
    expect(all(".srp-more")).toHaveLength(0);
  });

  it("surfaces a load-more failure in the empty arm and retries", async () => {
    api.searchBook
      .mockResolvedValueOnce(page([result({ chapterIndex: 99 })], true, "c1"))
      .mockRejectedValueOnce(new Error("boom"))
      .mockResolvedValueOnce(page([result({ charOffset: 300 })], false));
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(rows()).toHaveLength(0);
    el(".srp-more").click();
    await settle();
    expect(el(".srp-inline-error").textContent?.trim()).toBe(
      "Failed to load more.",
    );
    expect(rows()).toHaveLength(0);
    // The affordance stays put after the failure, and the retry pages on.
    el(".srp-more").click();
    await settle();
    expect(rows()).toHaveLength(1);
    expect(all(".srp-inline-error")).toHaveLength(0);
  });

  it("does not claim the book has no matches while more pages remain", async () => {
    api.searchBook.mockResolvedValueOnce(
      page([result({ chapterIndex: 99 })], true, "c1"),
    );
    mount();
    await settle();
    typeQuery("needle");
    advance(300);
    await settle();
    expect(stateText()).not.toBe("No results for “needle”.");
    expect(stateText()).toContain("more matches may follow");
  });
});
