<script lang="ts">
  import { onMount, tick } from "svelte";
  import { searchBook, type SearchResult } from "~/api/client";
  import { getErrorMessage } from "~/lib/errors";
  import Icon from "~/lib/Icon.svelte";
  import { X } from "@lucide/svelte";

  interface Props {
    bookId: string;
    onresultclick: (result: SearchResult, query: string) => void;
    onclose: () => void;
  }
  let { bookId, onresultclick, onclose }: Props = $props();

  type Status = "idle" | "loading" | "error" | "done";

  let query = $state("");
  let status = $state<Status>("idle");
  let results = $state<SearchResult[]>([]);
  let hasMore = $state(false);
  let nextCursor = $state("");
  let currentIdx = $state(0);
  let loadingMore = $state(false);
  let errorMsg = $state("");

  let input = $state<HTMLInputElement | null>(null);
  let listEl = $state<HTMLDivElement | null>(null);
  let debounce: ReturnType<typeof setTimeout> | undefined;
  let token = 0;
  let abort: AbortController | undefined;
  let lastQuery = "";

  // Results grouped by chapter, with a global index per result for nav.
  interface SearchResultItem {
    result: SearchResult;
    globalIdx: number;
    before: string;
    match: string;
    after: string;
    clippedStart: boolean;
    clippedEnd: boolean;
  }

  interface Group {
    chapterIndex: number;
    items: SearchResultItem[];
  }

  function toItem(r: SearchResult, globalIdx: number): SearchResultItem {
    const matchEnd = r.snippetStart + r.snippetLen;
    return {
      result: r,
      globalIdx,
      before: r.snippet.slice(0, r.snippetStart).trimStart(),
      match: r.snippet.slice(r.snippetStart, matchEnd),
      after: r.snippet.slice(matchEnd).trimEnd(),
      clippedStart: r.snippetStart > 0,
      clippedEnd: matchEnd < r.snippet.length,
    };
  }

  const groups = $derived.by<Group[]>(() => {
    const out: Group[] = [];
    results.forEach((r, globalIdx) => {
      const item = toItem(r, globalIdx);
      const last = out[out.length - 1];
      if (last && last.chapterIndex === r.chapterIndex) {
        last.items.push(item);
      } else {
        out.push({ chapterIndex: r.chapterIndex, items: [item] });
      }
    });
    return out;
  });
  const countText = $derived(
    results.length === 0
      ? ""
      : hasMore
        ? `${results.length}+ results`
        : `${results.length} result${results.length === 1 ? "" : "s"}`,
  );

  let activeOptionEl: HTMLElement | null = null;

  // Keep the active-descendant option state out of Svelte's per-row update path.
  // Arrowing through 200+ results should update the combobox input plus the two
  // affected option nodes, not re-evaluate active classes/aria-selected for
  // every rendered result on each key repeat.
  async function syncActiveOption(scroll = false): Promise<void> {
    await tick();
    const next =
      listEl?.querySelector<HTMLElement>(`#sr-${currentIdx}`) ?? null;
    if (activeOptionEl && activeOptionEl !== next) {
      activeOptionEl.setAttribute("aria-selected", "false");
    }
    if (next) {
      next.setAttribute("aria-selected", "true");
      if (scroll) next.scrollIntoView({ block: "nearest" });
    }
    activeOptionEl = next;
  }

  onMount(() => {
    input?.focus();
    return () => {
      if (debounce) clearTimeout(debounce);
      abort?.abort();
    };
  });

  function onInput(value: string): void {
    query = value;
    if (debounce) clearTimeout(debounce);
    if (!value.trim()) {
      // Cancel any in-flight request too: bumping the token alone discards a
      // late response but leaves the fetch running; aborting frees it now.
      abort?.abort();
      token += 1;
      status = "idle";
      results = [];
      currentIdx = 0;
      activeOptionEl = null;
      return;
    }
    const trimmed = value.trim();
    debounce = setTimeout(() => run(trimmed), 300);
  }

  async function run(q: string): Promise<void> {
    abort?.abort();
    abort = new AbortController();
    token += 1;
    const my = token;
    lastQuery = q;
    status = "loading";
    currentIdx = 0;
    activeOptionEl = null;
    try {
      const resp = await searchBook(bookId, q, undefined, 200, abort.signal);
      if (my !== token) return;
      results = resp.results ?? [];
      hasMore = resp.hasMore;
      nextCursor = resp.nextCursor ?? "";
      status = "done";
      void syncActiveOption();
    } catch (e) {
      if (e instanceof DOMException && e.name === "AbortError") return;
      if (my !== token) return;
      errorMsg = getErrorMessage(e, "Search failed. Please try again.");
      status = "error";
    }
  }

  async function loadMore(): Promise<void> {
    if (!hasMore || loadingMore) return;
    const q = query.trim();
    if (!q) return;
    abort?.abort();
    abort = new AbortController();
    token += 1;
    const my = token;
    loadingMore = true;
    try {
      const resp = await searchBook(bookId, q, nextCursor, 200, abort.signal);
      if (my !== token) return;
      results = [...results, ...(resp.results ?? [])];
      hasMore = resp.hasMore;
      nextCursor = resp.nextCursor ?? "";
    } catch (e) {
      if (my !== token) return;
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        errorMsg = getErrorMessage(e, "Failed to load more.");
      }
    } finally {
      loadingMore = false;
    }
  }

  function pick(r: SearchResult, idx: number): void {
    currentIdx = idx;
    void syncActiveOption();
    onresultclick(r, query.trim());
  }

  function onKey(e: KeyboardEvent): void {
    const total = results.length;
    switch (e.key) {
      case "Escape":
        e.preventDefault();
        e.stopPropagation();
        if (query.trim()) onInput("");
        else onclose();
        break;
      case "ArrowDown":
        e.preventDefault();
        e.stopPropagation();
        if (total === 0) return;
        currentIdx = (currentIdx + 1) % total;
        void syncActiveOption(true);
        break;
      case "ArrowUp":
        e.preventDefault();
        e.stopPropagation();
        if (total === 0) return;
        currentIdx = (currentIdx - 1 + total) % total;
        void syncActiveOption(true);
        break;
      case "Enter":
        if (total > 0 && e.target === input) {
          e.preventDefault();
          e.stopPropagation();
          pick(results[currentIdx], currentIdx);
        }
        break;
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="search" onkeydown={onKey}>
  <header>
    <input
      bind:this={input}
      class="field"
      type="search"
      placeholder="Search book…"
      value={query}
      oninput={(e) => onInput(e.currentTarget.value)}
      autocomplete="off"
      spellcheck="false"
      role="combobox"
      aria-label="Search book"
      aria-controls="search-results"
      aria-expanded={results.length > 0}
      aria-autocomplete="list"
      aria-activedescendant={results.length > 0
        ? `sr-${currentIdx}`
        : undefined}
    />
    {#if countText}<span class="count tnum" aria-live="polite">{countText}</span
      >{/if}
    <button class="close" onclick={onclose} aria-label="Close search"
      ><Icon icon={X} size={18} /></button
    >
  </header>

  <div
    class="list"
    bind:this={listEl}
    id="search-results"
    role="listbox"
    aria-label="Search results"
  >
    {#if status === "loading"}
      <p class="state" role="status">Searching…</p>
    {:else if status === "error"}
      <div class="state" role="alert">
        <p>{errorMsg}</p>
        <button class="ghost-btn" onclick={() => run(lastQuery)}
          >Try again</button
        >
      </div>
    {:else if status === "done" && results.length === 0}
      <p class="state" role="status">No results for “{query}”.</p>
    {:else if status === "done"}
      {#each groups as group (group.chapterIndex + "-" + group.items[0].globalIdx)}
        <div
          class="group"
          role="group"
          aria-label={`Chapter ${group.chapterIndex + 1}`}
        >
          <div class="group-head">
            Chapter {group.chapterIndex + 1}
            <span class="group-count tnum">{group.items.length}</span>
          </div>
          {#each group.items as it (it.globalIdx)}
            <button
              class="result"
              id={`sr-${it.globalIdx}`}
              role="option"
              tabindex="-1"
              aria-selected="false"
              onclick={() => pick(it.result, it.globalIdx)}
            >
              <span class="snippet">
                {#if it.clippedStart}…{/if}{it.before}<mark>{it.match}</mark
                >{it.after}{#if it.clippedEnd}…{/if}
              </span>
            </button>
          {/each}
        </div>
      {/each}
      {#if hasMore && !loadingMore}
        <button class="more ghost-btn" onclick={loadMore}
          >Load more results</button
        >
      {/if}
      {#if loadingMore}<p class="state">Loading…</p>{/if}
    {/if}
  </div>
</div>

<style>
  .search {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    color: var(--fg);
  }
  header {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: var(--sp-2) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
    flex: 0 0 auto;
  }
  .field {
    flex: 1;
    min-width: 0;
    padding: 0.45rem 0.6rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    transition: border-color var(--dur) var(--ease-out);
  }
  .field:hover {
    border-color: var(--accent);
  }
  .count {
    font-size: var(--text-xs);
    color: var(--muted);
    white-space: nowrap;
  }
  .close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .close:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .close:active {
    transform: scale(0.94);
  }
  .list {
    overflow-y: auto;
    padding: 0.4rem 0.5rem 2rem;
  }
  .state {
    color: var(--muted);
    padding: 0.6rem 0.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    align-items: flex-start;
  }
  .ghost-btn {
    border: 1px solid var(--hairline-strong);
    background: transparent;
    color: var(--fg);
    font: inherit;
    padding: 0.4rem 0.8rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .ghost-btn:hover {
    background: var(--surface-hover);
  }
  .ghost-btn:active {
    transform: scale(0.97);
  }
  .group {
    margin-bottom: 0.6rem;
  }
  .group-head {
    display: flex;
    justify-content: space-between;
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
    padding: 0.3rem 0.5rem;
  }
  .result {
    display: block;
    width: 100%;
    text-align: left;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    padding: 0.45rem 0.5rem;
    border-radius: var(--radius);
    cursor: pointer;
    line-height: 1.35;
    transition: background var(--dur-fast) var(--ease-out);
  }
  .result:hover {
    background: var(--surface-hover);
  }
  .search :global(.result[aria-selected="true"]) {
    background: color-mix(in srgb, var(--accent) 18%, transparent);
  }
  .snippet {
    font-size: var(--text-sm);
  }
  .snippet mark {
    background: color-mix(in srgb, var(--accent) 40%, transparent);
    color: inherit;
    border-radius: 2px;
    padding: 0 1px;
  }
  .more {
    width: 100%;
    margin-top: 0.4rem;
  }
</style>
