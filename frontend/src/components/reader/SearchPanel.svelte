<script lang="ts">
  import { onMount } from "svelte";
  import { searchBook, type SearchResult } from "~/api/client";
  import { getErrorMessage } from "~/lib/errors";

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
  let debounce: ReturnType<typeof setTimeout> | undefined;
  let token = 0;
  let abort: AbortController | undefined;
  let lastQuery = "";

  // Results grouped by chapter, with a global index per result for nav.
  interface Group {
    chapterIndex: number;
    items: { result: SearchResult; globalIdx: number }[];
  }
  const groups = $derived.by<Group[]>(() => {
    const out: Group[] = [];
    results.forEach((r, globalIdx) => {
      const last = out[out.length - 1];
      if (last && last.chapterIndex === r.chapterIndex) {
        last.items.push({ result: r, globalIdx });
      } else {
        out.push({ chapterIndex: r.chapterIndex, items: [{ result: r, globalIdx }] });
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
      token += 1;
      status = "idle";
      results = [];
      currentIdx = 0;
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
    try {
      const resp = await searchBook(bookId, q, undefined, 200, abort.signal);
      if (my !== token) return;
      results = resp.results ?? [];
      hasMore = resp.hasMore;
      nextCursor = resp.nextCursor ?? "";
      status = "done";
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
    loadingMore = true;
    try {
      const resp = await searchBook(bookId, q, nextCursor, 200, abort.signal);
      results = [...results, ...(resp.results ?? [])];
      hasMore = resp.hasMore;
      nextCursor = resp.nextCursor ?? "";
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        errorMsg = getErrorMessage(e, "Failed to load more.");
      }
    } finally {
      loadingMore = false;
    }
  }

  function pick(r: SearchResult, idx: number): void {
    currentIdx = idx;
    onresultclick(r, query.trim());
  }

  function onKey(e: KeyboardEvent): void {
    const total = results.length;
    switch (e.key) {
      case "Escape":
        e.stopPropagation();
        if (query.trim()) onInput("");
        else onclose();
        break;
      case "ArrowDown":
        if (total === 0) return;
        e.preventDefault();
        currentIdx = (currentIdx + 1) % total;
        break;
      case "ArrowUp":
        if (total === 0) return;
        e.preventDefault();
        currentIdx = (currentIdx - 1 + total) % total;
        break;
      case "Enter":
        if (total > 0) {
          e.preventDefault();
          pick(results[currentIdx], currentIdx);
        }
        break;
    }
  }

  function parts(r: SearchResult) {
    return {
      before: r.snippet.slice(0, r.snippetStart).trimStart(),
      match: r.snippet.slice(r.snippetStart, r.snippetStart + r.snippetLen),
      after: r.snippet.slice(r.snippetStart + r.snippetLen).trimEnd(),
    };
  }
</script>

<div class="search">
  <header>
    <input
      bind:this={input}
      class="field"
      type="search"
      placeholder="Search book…"
      value={query}
      oninput={(e) => onInput(e.currentTarget.value)}
      onkeydown={onKey}
      autocomplete="off"
      spellcheck="false"
    />
    {#if countText}<span class="count">{countText}</span>{/if}
    <button class="close" onclick={onclose} aria-label="Close search">×</button>
  </header>

  <div class="list">
    {#if status === "loading"}
      <p class="state">Searching…</p>
    {:else if status === "error"}
      <div class="state">
        <p>{errorMsg}</p>
        <button onclick={() => run(lastQuery)}>Try again</button>
      </div>
    {:else if status === "done" && results.length === 0}
      <p class="state">No results for “{query}”.</p>
    {:else if status === "done"}
      {#each groups as group (group.chapterIndex + "-" + group.items[0].globalIdx)}
        <div class="group">
          <div class="group-head">
            Chapter {group.chapterIndex + 1}
            <span class="group-count">{group.items.length}</span>
          </div>
          {#each group.items as it (it.globalIdx)}
            {@const p = parts(it.result)}
            <button
              class="result"
              class:active={it.globalIdx === currentIdx}
              onclick={() => pick(it.result, it.globalIdx)}
            >
              <span class="snippet">
                {#if it.result.snippetStart > 0}…{/if}{p.before}<mark>{p.match}</mark>{p.after}{#if it.result.snippetStart + it.result.snippetLen < it.result.snippet.length}…{/if}
              </span>
            </button>
          {/each}
        </div>
      {/each}
      {#if hasMore && !loadingMore}
        <button class="more" onclick={loadMore}>Load more results</button>
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
    padding: 0.6rem 0.75rem;
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 10%, transparent);
    flex: 0 0 auto;
  }
  .field {
    flex: 1;
    min-width: 0;
    padding: 0.45rem 0.6rem;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.45rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
  }
  .field:focus {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  .count {
    font-size: 0.72rem;
    color: var(--muted, #6b6661);
    white-space: nowrap;
  }
  .close {
    border: none;
    background: transparent;
    color: var(--fg);
    font-size: 1.3rem;
    line-height: 1;
    cursor: pointer;
  }
  .list {
    overflow-y: auto;
    padding: 0.4rem 0.5rem 2rem;
  }
  .state {
    color: var(--muted, #6b6661);
    padding: 0.6rem 0.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    align-items: flex-start;
  }
  .state button {
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    background: transparent;
    color: var(--fg);
    font: inherit;
    padding: 0.35rem 0.7rem;
    border-radius: 0.4rem;
    cursor: pointer;
  }
  .group {
    margin-bottom: 0.6rem;
  }
  .group-head {
    display: flex;
    justify-content: space-between;
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted, #6b6661);
    padding: 0.3rem 0.5rem;
  }
  .group-count {
    font-variant-numeric: tabular-nums;
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
    border-radius: 0.4rem;
    cursor: pointer;
    line-height: 1.35;
  }
  .result:hover {
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }
  .result.active {
    background: color-mix(in srgb, var(--accent) 18%, transparent);
  }
  .snippet {
    font-size: 0.85rem;
  }
  .snippet mark {
    background: color-mix(in srgb, var(--accent) 40%, transparent);
    color: inherit;
    border-radius: 2px;
    padding: 0 1px;
  }
  .more {
    width: 100%;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    background: transparent;
    color: var(--fg);
    font: inherit;
    padding: 0.5rem;
    border-radius: 0.45rem;
    cursor: pointer;
    margin-top: 0.4rem;
  }
</style>
