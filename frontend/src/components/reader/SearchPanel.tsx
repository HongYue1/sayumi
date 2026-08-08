// SearchPanel: in-book search with paging, chapter grouping, and combobox
// keyboard nav — Solid 2.0 port.
//
// Solid 2.0 notes:
//   - Rendered state is signals; debounce/token/abort/lastQuery/composing and
//     activeOptionEl stay plain lets (never rendered).
//   - The active-descendant perf trick survives unchanged: rows always render
//     aria-selected="false" and syncActiveOption mutates the two affected DOM
//     nodes directly, so arrowing through 200+ results never re-renders rows.
//     Safe because every row attribute is static — Solid never re-applies
//     them, so the manual setAttribute can't be clobbered.
//   - onMount -> onSettled, which RETURNS the debounce/abort teardown.
//     onCleanup() inside onSettled throws CLEANUP_IN_FORBIDDEN_SCOPE, and an
//     uncaught throw there halts the whole reactive system: no further updates
//     anywhere in the app, and dispose() can no longer unmount. Login.tsx and
//     Library.tsx document the same rule.
//   - await tick() -> flush() in syncActiveOptionAfterRender.
//   - appendRawResults replaces the LAST group object (Solid's keyed <For>
//     maps by group identity; mutating the old object in place would never
//     re-render). The last group's rows are recreated on each load-more, so we
//     re-sync the active option afterwards — the Svelte original relied on
//     rune proxies mutating in place and needed no such pass.
//   - aria-expanded is a constant "true" string (EnumeratedPseudoBoolean): the
//     listbox is always rendered, and carries the loading/error/empty states
//     too, so the popup is never collapsed (as in CommandPalette.tsx).
import {
  createMemo,
  createSignal,
  flush,
  For,
  Match,
  onSettled,
  Show,
  Switch,
} from "solid-js";
import { searchBook, type SearchResult } from "~/api/client";
import { getErrorMessage } from "~/lib/errors";
import Icon from "~/lib/Icon";
import { X } from "~/lib/icons";

interface Props {
  bookId: string;
  chapterCount: number;
  onresultclick: (result: SearchResult, query: string) => void;
  onclose: () => void;
}

type Status = "idle" | "loading" | "error" | "done";
const SEARCH_PAGE_SIZE = 100;

// Results grouped by chapter, with a global index per result for nav.
interface SearchResultItem {
  result: SearchResult;
  globalIdx: number;
  id: string;
  before: string;
  match: string;
  after: string;
  clippedStart: boolean;
  clippedEnd: boolean;
}

interface Group {
  chapterIndex: number;
  label: string;
  items: SearchResultItem[];
}

function responseCursor(resp: { nextCursor?: unknown }): string {
  const cursor = typeof resp.nextCursor === "string" ? resp.nextCursor : "";
  return cursor.trim().length > 0 ? cursor : "";
}

function hasNextPage(resp: {
  hasMore: unknown;
  nextCursor?: unknown;
}): boolean {
  return resp.hasMore === true && responseCursor(resp).length > 0;
}

function hasAdvancedPage(
  resp: { hasMore: unknown; nextCursor?: unknown },
  previousCursor: string,
): boolean {
  const cursor = responseCursor(resp);
  return (
    resp.hasMore === true && cursor.length > 0 && cursor !== previousCursor
  );
}

function isSafeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value);
}

// Snippet offsets from the API are code-point counts, not UTF-16 lengths.
function codePointLength(value: string): number {
  return Array.from(value).length;
}

function toItem(r: SearchResult, globalIdx: number): SearchResultItem {
  // snippetStart/snippetLen are CODE POINT offsets: internal/epub/search.go
  // slices []rune, and iframe/searchHighlight.ts pins the same code-point
  // contract for charOffset. String#slice counts UTF-16 units, so one astral
  // character ahead of the match would slide <mark> a unit per surrogate pair,
  // and a boundary landing inside a pair would render a lone surrogate.
  const chars = Array.from(r.snippet);
  const snippetStart = Number.isSafeInteger(r.snippetStart)
    ? Math.min(Math.max(r.snippetStart, 0), chars.length)
    : 0;
  const snippetLen = Number.isSafeInteger(r.snippetLen)
    ? Math.max(r.snippetLen, 0)
    : 0;
  const matchLen = Math.min(snippetLen, chars.length - snippetStart);
  const matchEnd = snippetStart + matchLen;
  return {
    result: r,
    globalIdx,
    id: `sr-${globalIdx}`,
    before: chars.slice(0, snippetStart).join("").trimStart(),
    match: chars.slice(snippetStart, matchEnd).join(""),
    after: chars.slice(matchEnd).join("").trimEnd(),
    clippedStart: snippetStart > 0,
    clippedEnd: matchEnd < chars.length,
  };
}

function addItemToGroups(target: Group[], item: SearchResultItem): void {
  const last = target[target.length - 1];
  if (last && last.chapterIndex === item.result.chapterIndex) {
    last.items.push(item);
  } else {
    const chapterNumber = item.result.chapterIndex + 1;
    target.push({
      chapterIndex: item.result.chapterIndex,
      label: `Chapter ${chapterNumber}`,
      items: [item],
    });
  }
}

export default function SearchPanel(props: Props) {
  const [query, setQuery] = createSignal("");
  const [status, setStatus] = createSignal<Status>("idle");
  const [hasMore, setHasMore] = createSignal(false);
  const [nextCursor, setNextCursor] = createSignal("");
  const [currentIdx, setCurrentIdx] = createSignal(0);
  const [loadingMore, setLoadingMore] = createSignal(false);
  const [errorMsg, setErrorMsg] = createSignal("");
  const [loadMoreError, setLoadMoreError] = createSignal("");
  const [resultItems, setResultItems] = createSignal<SearchResultItem[]>([]);
  const [groups, setGroups] = createSignal<Group[]>([]);

  let input: HTMLInputElement | undefined;
  let debounce: ReturnType<typeof setTimeout> | undefined;
  let token = 0;
  let abort: AbortController | undefined;
  let lastQuery = "";
  let composing = false;
  let activeOptionEl: HTMLElement | null = null;

  function isSearchResult(value: unknown): value is SearchResult {
    if (typeof value !== "object" || value === null) return false;
    const r = value as Partial<SearchResult>;
    const {
      chapterIndex,
      charOffset,
      matchLen,
      snippet,
      snippetStart,
      snippetLen,
    } = r;
    return (
      isSafeInteger(chapterIndex) &&
      chapterIndex >= 0 &&
      chapterIndex < props.chapterCount &&
      isSafeInteger(charOffset) &&
      charOffset >= 0 &&
      isSafeInteger(matchLen) &&
      matchLen > 0 &&
      Number.isSafeInteger(charOffset + matchLen) &&
      typeof snippet === "string" &&
      snippet.length > 0 &&
      isSafeInteger(snippetStart) &&
      snippetStart >= 0 &&
      // Code-point bound: snippetStart indexes runes, so a UTF-16 length would
      // over-admit by one per astral character ahead of the match.
      snippetStart < codePointLength(snippet) &&
      isSafeInteger(snippetLen) &&
      snippetLen > 0 &&
      Number.isSafeInteger(snippetStart + snippetLen)
    );
  }

  function searchResults(value: unknown): SearchResult[] {
    return Array.isArray(value) ? value.filter(isSearchResult) : [];
  }

  function isValidIndex(value: number): boolean {
    return (
      Number.isSafeInteger(value) && value >= 0 && value < resultItems().length
    );
  }

  function setRawResults(raw: SearchResult[]): void {
    const items: SearchResultItem[] = [];
    const nextGroups: Group[] = [];
    raw.forEach((r, globalIdx) => {
      const item = toItem(r, globalIdx);
      items.push(item);
      addItemToGroups(nextGroups, item);
    });
    setResultItems(items);
    setGroups(nextGroups);
  }

  function appendRawResults(raw: SearchResult[]): void {
    if (raw.length === 0) return;
    const offset = resultItems().length;
    const items: SearchResultItem[] = [];
    // Replace the last group object (never mutate it): keyed <For> maps by
    // identity, so an in-place push would render nothing.
    const nextGroups = groups().slice();
    const last = nextGroups[nextGroups.length - 1];
    if (last) {
      nextGroups[nextGroups.length - 1] = {
        ...last,
        items: last.items.slice(),
      };
    }
    raw.forEach((r, i) => {
      const item = toItem(r, offset + i);
      items.push(item);
      addItemToGroups(nextGroups, item);
    });
    setResultItems([...resultItems(), ...items]);
    setGroups(nextGroups);
  }

  const countText = createMemo(() =>
    resultItems().length === 0
      ? ""
      : hasMore()
        ? `${resultItems().length}+ results`
        : `${resultItems().length} result${resultItems().length === 1 ? "" : "s"}`,
  );

  // Keep the active-descendant option state out of the per-row render path.
  // Arrowing through 200+ results should update the combobox input plus the two
  // affected option nodes, not re-evaluate active classes/aria-selected for
  // every rendered result on each key repeat.
  function syncActiveOption(scroll = false): void {
    const nextId = resultItems()[currentIdx()]?.id;
    const next = nextId ? document.getElementById(nextId) : null;
    if (activeOptionEl && activeOptionEl !== next) {
      activeOptionEl.setAttribute("aria-selected", "false");
    }
    if (next) {
      next.setAttribute("aria-selected", "true");
      if (scroll) next.scrollIntoView({ block: "nearest" });
    }
    activeOptionEl = next;
  }

  // flush() applies the just-committed results synchronously so the option
  // nodes exist before we sync (the Svelte original awaited tick()). It also
  // publishes a setCurrentIdx() issued by the calling handler: a signal write
  // is not visible to a synchronous read in the same handler, so syncing
  // without it marks the option the user just left and leaves aria-selected a
  // keystroke behind aria-activedescendant.
  function syncActiveOptionAfterRender(scroll = false): void {
    flush();
    syncActiveOption(scroll);
  }

  onSettled(() => {
    input?.focus();
    return () => {
      if (debounce) clearTimeout(debounce);
      abort?.abort();
    };
  });

  function onInput(value: string, deferSearch = false): void {
    setQuery(value);
    if (debounce) clearTimeout(debounce);
    const trimmed = value.trim();
    if (
      !deferSearch &&
      trimmed &&
      trimmed === lastQuery &&
      status() === "done"
    ) {
      return;
    }
    // Invalidate any in-flight search immediately, not after the debounce. This
    // keeps a slow previous query/load-more response from repainting stale
    // results while the user is already typing the next query.
    abort?.abort();
    abort = undefined;
    token += 1;
    setLoadingMore(false);
    setErrorMsg("");
    if (!trimmed) {
      setStatus("idle");
      setResultItems([]);
      setGroups([]);
      setHasMore(false);
      setNextCursor("");
      setLoadMoreError("");
      setCurrentIdx(0);
      activeOptionEl = null;
      return;
    }
    setStatus("loading");
    setResultItems([]);
    setGroups([]);
    setHasMore(false);
    setNextCursor("");
    setLoadMoreError("");
    setCurrentIdx(0);
    activeOptionEl = null;
    if (deferSearch) {
      setStatus("idle");
      return;
    }
    debounce = setTimeout(() => void run(trimmed), 300);
  }

  async function run(q: string): Promise<void> {
    abort?.abort();
    abort = new AbortController();
    token += 1;
    const my = token;
    lastQuery = q;
    setStatus("loading");
    setCurrentIdx(0);
    activeOptionEl = null;
    setLoadingMore(false);
    setErrorMsg("");
    setLoadMoreError("");
    try {
      const resp = await searchBook(
        props.bookId,
        q,
        undefined,
        SEARCH_PAGE_SIZE,
        abort.signal,
      );
      if (my !== token) return;
      const rows = searchResults(resp.results);
      // Page on the server's hasMore + cursor alone. The validator is a display
      // filter: a page whose rows it all drops is not "no more matches", and
      // treating it as such strands the rest of the book behind it.
      const canPage = hasNextPage(resp);
      setRawResults(rows);
      setHasMore(canPage);
      setNextCursor(canPage ? responseCursor(resp) : "");
      setStatus("done");
      syncActiveOptionAfterRender();
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      if (my !== token) return;
      setErrorMsg(getErrorMessage(e, "Search failed. Please try again."));
      setStatus("error");
    } finally {
      if (my === token) abort = undefined;
    }
  }

  async function loadMore(): Promise<void> {
    if (!hasMore() || loadingMore() || !nextCursor()) return;
    const q = query().trim();
    if (!q) return;
    const cursor = nextCursor();
    abort?.abort();
    abort = new AbortController();
    token += 1;
    const my = token;
    setLoadingMore(true);
    setLoadMoreError("");
    try {
      const resp = await searchBook(
        props.bookId,
        q,
        cursor,
        SEARCH_PAGE_SIZE,
        abort.signal,
      );
      if (my !== token) return;
      const more = searchResults(resp.results);
      // hasAdvancedPage already refuses a cursor that did not move, which is
      // the real loop guard; an all-dropped page must not end paging.
      const canPage = hasAdvancedPage(resp, cursor);
      appendRawResults(more);
      setHasMore(canPage);
      setNextCursor(canPage ? responseCursor(resp) : "");
      // The last group's rows were recreated by the group-object replacement
      // (see appendRawResults), so re-assert the active option's aria-selected.
      syncActiveOptionAfterRender();
    } catch (e) {
      if (my !== token) return;
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        setLoadMoreError(getErrorMessage(e, "Failed to load more."));
      }
    } finally {
      if (my === token) {
        abort = undefined;
        setLoadingMore(false);
      }
    }
  }

  function pick(r: SearchResult, idx: number): void {
    if (!isValidIndex(idx)) return;
    setCurrentIdx(idx);
    syncActiveOptionAfterRender();
    props.onresultclick(r, query().trim());
  }

  function onListClick(e: MouseEvent): void {
    const target = e.target as Element | null;
    const button = target?.closest<HTMLButtonElement>("button.srp-result");
    if (!button) return;
    const idx = Number(button.dataset.idx);
    if (!isValidIndex(idx)) return;
    const item = resultItems()[idx];
    pick(item.result, idx);
  }

  // Keep mousedown on a result from moving focus out of the search input —
  // the combobox pattern keeps focus on the input while the pointer hovers.
  function onListMouseDown(e: MouseEvent): void {
    const target = e.target as Element | null;
    if (target?.closest("button.srp-result")) e.preventDefault();
  }

  function onTextInput(e: Event): void {
    const value = (e.currentTarget as HTMLInputElement).value;
    const isComposing = (e as InputEvent).isComposing === true;
    onInput(value, isComposing);
  }

  function onCompositionStart(e: CompositionEvent): void {
    composing = true;
    onInput((e.currentTarget as HTMLInputElement).value, true);
  }

  function onCompositionEnd(e: CompositionEvent): void {
    composing = false;
    onInput((e.currentTarget as HTMLInputElement).value);
  }

  function onKey(e: KeyboardEvent): void {
    if (e.isComposing || composing) return;
    const total = resultItems().length;
    switch (e.key) {
      case "Escape":
        // First press closes, per ShortcutsHelp ("Close overlay / panel") and
        // the three sibling panels. Clearing first was redundant: the panel is
        // unmounted on close, so every signal resets anyway.
        e.preventDefault();
        e.stopPropagation();
        props.onclose();
        break;
      case "ArrowDown":
        e.preventDefault();
        e.stopPropagation();
        if (total === 0) return;
        setCurrentIdx((currentIdx() + 1) % total);
        syncActiveOptionAfterRender(true);
        break;
      case "ArrowUp":
        e.preventDefault();
        e.stopPropagation();
        if (total === 0) return;
        setCurrentIdx((currentIdx() - 1 + total) % total);
        syncActiveOptionAfterRender(true);
        break;
      case "Enter":
        if (total > 0 && e.target === input && isValidIndex(currentIdx())) {
          e.preventDefault();
          e.stopPropagation();
          pick(resultItems()[currentIdx()].result, currentIdx());
        }
        break;
    }
  }

  return (
    // Keyboard handling lives on the wrapper (combobox pattern: focus stays on
    // the input; arrows walk aria-activedescendant). The list's click/mouse
    // handling is delegation, so there are no per-row handlers at all — these
    // cover the two a11y ignores the Svelte original carried.
    <div class="srp" onKeyDown={onKey}>
      <header class="srp-head">
        <input
          ref={(el) => {
            input = el;
          }}
          class="field"
          type="search"
          placeholder="Search book…"
          value={query()}
          onInput={onTextInput}
          onCompositionStart={onCompositionStart}
          onCompositionEnd={onCompositionEnd}
          autocomplete="off"
          spellcheck="false"
          role="combobox"
          aria-label="Search book"
          aria-controls="search-results"
          aria-expanded="true"
          aria-autocomplete="list"
          aria-activedescendant={
            resultItems().length > 0
              ? resultItems()[currentIdx()]?.id
              : undefined
          }
        />
        <span class="sr-only" aria-live="polite" aria-atomic="true">
          {countText()}
        </span>
        <Show when={countText()}>
          <span class="srp-count tnum" aria-hidden="true">
            {countText()}
          </span>
        </Show>
        <button
          class="icon-btn press srp-close"
          onClick={props.onclose}
          aria-label="Close search"
        >
          <Icon icon={X} size={18} labelFromParent />
        </button>
      </header>

      <div
        class="srp-list"
        id="search-results"
        role="listbox"
        aria-label="Search results"
        tabindex="-1"
        onMouseDown={onListMouseDown}
        onClick={onListClick}
      >
        <Switch>
          <Match when={status() === "loading"}>
            <p class="srp-state" role="status">
              Searching…
            </p>
          </Match>
          <Match when={status() === "error"}>
            <div class="srp-state" role="alert">
              <p>{errorMsg()}</p>
              <button
                class="btn-ghost press"
                onClick={() => void run(lastQuery)}
              >
                Try again
              </button>
            </div>
          </Match>
          <Match when={status() === "done" && resultItems().length === 0}>
            <p class="srp-state" role="status">
              No results for “{query()}”.
            </p>
          </Match>
          <Match when={status() === "done"}>
            <For each={groups()}>
              {(group) => (
                <div class="srp-group" role="group" aria-label={group.label}>
                  <div class="srp-group-head">
                    {group.label}
                    <span class="srp-group-count tnum">
                      {group.items.length}
                    </span>
                  </div>
                  <For each={group.items}>
                    {(it) => (
                      <button
                        class="srp-result"
                        id={it.id}
                        role="option"
                        tabindex="-1"
                        data-idx={it.globalIdx}
                        aria-selected="false"
                      >
                        <span class="srp-snippet">
                          {it.clippedStart ? "…" : ""}
                          {it.before}
                          <mark>{it.match}</mark>
                          {it.after}
                          {it.clippedEnd ? "…" : ""}
                        </span>
                      </button>
                    )}
                  </For>
                </div>
              )}
            </For>
            <Show when={hasMore()}>
              <button
                class="srp-more btn-ghost press"
                onClick={() => void loadMore()}
                aria-disabled={loadingMore() ? "true" : "false"}
              >
                {loadingMore() ? "Loading…" : "Load more results"}
              </button>
            </Show>
            <Show when={loadMoreError()}>
              <p class="srp-state srp-inline-error" role="alert">
                {loadMoreError()}
              </p>
            </Show>
          </Match>
        </Switch>
      </div>
    </div>
  );
}
