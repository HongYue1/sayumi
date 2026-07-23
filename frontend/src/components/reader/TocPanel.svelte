<script lang="ts">
  import { tick } from "svelte";
  import type { TocEntry } from "~/api/client";
  import Icon from "~/lib/Icon.svelte";
  import { X } from "@lucide/svelte";

  interface Props {
    toc: TocEntry[];
    activeEntry: TocEntry | null;
    onnavigate: (href: string) => void;
    onclose: () => void;
  }

  let { toc, activeEntry, onnavigate, onclose }: Props = $props();

  // Fixed-height virtual list. Only the rows in (or near) the viewport are ever
  // in the DOM, so a 6000-chapter TOC opens instantly instead of laying out and
  // painting thousands of buttons. ROW_H must match the rendered row height; we
  // pin .entry to var(--toc-row-h) (set from ROW_H below) so the two can't drift
  // and the scroll math stays exact.
  const ROW_H = 34; // px
  const OVERSCAN = 8; // rows kept above/below the viewport to avoid blank edges

  // The TOC is always fully expanded, so flatten the tree to a linear list in
  // display order once per book; each row remembers its depth for indentation.
  type Row = { entry: TocEntry; depth: number };
  const rows = $derived.by(() => {
    const out: Row[] = [];
    const walk = (entries: TocEntry[], depth: number): void => {
      for (const entry of entries) {
        out.push({ entry, depth });
        if (entry.children?.length) walk(entry.children, depth + 1);
      }
    };
    walk(toc, 0);
    return out;
  });

  // Client-side filter over the flattened list. Case-insensitive substring on
  // the title (mirrors the library's substring filter). The match is cheap even
  // for huge TOCs, so no debounce; an empty query passes the list through
  // untouched so the virtual-list math below is identical to the unfiltered
  // case. The filtered list (not the full list) is what the window renders, so
  // every derived below keys off it.
  let query = $state("");
  const normalizedQuery = $derived(query.trim().toLowerCase());
  const filteredRows = $derived.by(() => {
    const q = normalizedQuery;
    if (!q) return rows;
    return rows.filter((r) => r.entry.title.toLowerCase().includes(q));
  });

  // Highlight the matched substring (first occurrence) so a filtered list is
  // scannable. Returns null when there's no active query so unfiltered rows
  // render as plain text. The highlight is a height-neutral inline <mark>
  // (background/colour only, no vertical padding or border) so it can't shift
  // the fixed row height the virtual-scroll math depends on.
  function highlight(
    title: string,
  ): { before: string; match: string; after: string } | null {
    const q = normalizedQuery;
    if (!q) return null;

    let folded = "";
    const sourceStarts: number[] = [];
    const sourceEnds: number[] = [];
    let sourceOffset = 0;

    for (const char of title) {
      const lower = char.toLowerCase();
      const sourceEnd = sourceOffset + char.length;
      for (let i = 0; i < lower.length; i += 1) {
        sourceStarts.push(sourceOffset);
        sourceEnds.push(sourceEnd);
      }
      folded += lower;
      sourceOffset = sourceEnd;
    }

    const idx = folded.indexOf(q);
    if (idx < 0) return null;

    const sourceStart = sourceStarts[idx];
    const sourceEnd = sourceEnds[idx + q.length - 1];
    if (sourceStart === undefined || sourceEnd === undefined) return null;

    return {
      before: title.slice(0, sourceStart),
      match: title.slice(sourceStart, sourceEnd),
      after: title.slice(sourceEnd),
    };
  }

  // Match on reference identity (not href) so exactly one row is current even
  // when two entries point at the same file.
  const activeIndex = $derived(
    activeEntry ? filteredRows.findIndex((r) => r.entry === activeEntry) : -1,
  );

  let scrollEl: HTMLElement | null = $state(null);
  let queryEl: HTMLInputElement | null = $state(null);
  let scrollTop = $state(0);
  let viewportH = $state(0);
  let initialised = false;
  let lastQuery = "";
  let focusedIndex = $state(-1);
  let composing = false;

  const startIndex = $derived(
    Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN),
  );
  const endIndex = $derived(
    Math.min(
      filteredRows.length,
      startIndex + Math.ceil(viewportH / ROW_H) + OVERSCAN * 2,
    ),
  );
  const windowRows = $derived(filteredRows.slice(startIndex, endIndex));
  const totalHeight = $derived(filteredRows.length * ROW_H);
  const offsetY = $derived(startIndex * ROW_H);

  function onScroll(): void {
    if (scrollEl) scrollTop = scrollEl.scrollTop;
  }

  function clearQuery(): void {
    query = "";
    queryEl?.focus();
  }

  function onQueryKey(e: KeyboardEvent): void {
    if (e.isComposing || composing) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      void focusRow(focusedIndex >= 0 ? focusedIndex : 0);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      void focusRow(focusedIndex >= 0 ? focusedIndex : filteredRows.length - 1);
    } else if (e.key === "Enter") {
      // Jump straight to the first match so a quick filter-then-Enter opens the
      // most likely chapter without touching the mouse.
      e.preventDefault();
      const first = filteredRows[0];
      if (first) onnavigate(first.entry.href);
    } else if (e.key === "Escape") {
      // The reader's global Escape (which closes the panel) never fires while a
      // panel input is focused — handleWindowKey in Read.svelte bails on
      // INPUT/TEXTAREA so letter shortcuts aren't typed — and this field is
      // focused on open. So drive both steps locally: a non-empty query clears
      // first, an empty query closes the panel.
      e.preventDefault();
      e.stopPropagation();
      if (normalizedQuery) query = "";
      else onclose();
    }
  }

  function onCompositionStart(): void {
    composing = true;
  }

  function onCompositionEnd(): void {
    composing = false;
  }

  function rowId(index: number): string {
    return `toc-entry-${index}`;
  }

  async function focusRow(index: number): Promise<void> {
    if (!scrollEl || filteredRows.length === 0) return;

    const nextIndex = Math.max(0, Math.min(filteredRows.length - 1, index));
    const rowTop = nextIndex * ROW_H;
    const rowBottom = rowTop + ROW_H;
    const visibleTop = scrollEl.scrollTop;
    const visibleBottom = visibleTop + scrollEl.clientHeight;

    if (rowTop < visibleTop) {
      scrollEl.scrollTop = rowTop;
    } else if (rowBottom > visibleBottom) {
      scrollEl.scrollTop = Math.max(0, rowBottom - scrollEl.clientHeight);
    }

    scrollTop = scrollEl.scrollTop;
    focusedIndex = nextIndex;
    await tick();
    document.getElementById(rowId(nextIndex))?.focus({ preventScroll: true });
  }

  function onEntryKey(e: KeyboardEvent, index: number): void {
    let nextIndex: number | null = null;

    switch (e.key) {
      case "ArrowDown":
        nextIndex = index + 1;
        break;
      case "ArrowUp":
        nextIndex = index - 1;
        break;
      case "Home":
        nextIndex = 0;
        break;
      case "End":
        nextIndex = filteredRows.length - 1;
        break;
      case "PageDown":
        nextIndex = index + Math.max(1, Math.floor(viewportH / ROW_H));
        break;
      case "PageUp":
        nextIndex = index - Math.max(1, Math.floor(viewportH / ROW_H));
        break;
      default:
        return;
    }

    e.preventDefault();
    void focusRow(nextIndex);
  }

  // Track the viewport height (the panel resizes with the window).
  $effect(() => {
    if (!scrollEl) return;
    const ro = new ResizeObserver(() => {
      if (scrollEl) viewportH = scrollEl.clientHeight;
    });
    ro.observe(scrollEl);
    return () => ro.disconnect();
  });

  // On open, jump straight to the current chapter (centered) instead of opening
  // at the top of a long book, then put focus in the filter field so the user
  // can type immediately. Focusing our own element here is the focus-trap
  // opt-out (preventScroll keeps the centered position) — without it the trap
  // would grab the first row and yank the scroll back to the top. Runs once.
  $effect(() => {
    if (initialised || !scrollEl) return;
    viewportH = scrollEl.clientHeight;
    if (activeIndex >= 0) {
      const target = activeIndex * ROW_H - (viewportH - ROW_H) / 2;
      scrollEl.scrollTop = Math.max(0, target);
    }
    scrollTop = scrollEl.scrollTop;
    focusedIndex =
      activeIndex >= 0 ? activeIndex : filteredRows.length > 0 ? 0 : -1;
    queryEl?.focus({ preventScroll: true });
    initialised = true;
  });

  // Whenever the query changes, reset the scroll to the top of the results so a
  // new filter starts at its first match rather than wherever the previous list
  // was scrolled. Skips the initial empty-query pass so it can't clobber the
  // open-time centering above.
  $effect(() => {
    const q = normalizedQuery;
    if (q === lastQuery) return;
    lastQuery = q;
    if (scrollEl) {
      scrollEl.scrollTop = 0;
      scrollTop = 0;
      focusedIndex = filteredRows.length > 0 ? 0 : -1;
    }
  });
</script>

<div class="toc">
  <header>
    <h2>Contents</h2>
    <button class="close" onclick={onclose} aria-label="Close table of contents"
      ><Icon icon={X} size={18} /></button
    >
  </header>
  {#if rows.length === 0}
    <p class="empty">No table of contents.</p>
  {:else}
    <div class="filter">
      <input
        bind:this={queryEl}
        bind:value={query}
        class="field"
        type="text"
        placeholder="Filter chapters…"
        autocomplete="off"
        spellcheck="false"
        aria-label="Filter table of contents"
        onkeydown={onQueryKey}
        oncompositionstart={onCompositionStart}
        oncompositionend={onCompositionEnd}
      />
      {#if query}
        <button class="clear" onclick={clearQuery} aria-label="Clear filter"
          ><Icon icon={X} size={16} /></button
        >
      {/if}
    </div>
    {#if filteredRows.length === 0}
      <p class="empty" role="status">No chapters match “{query}”.</p>
    {:else}
      <nav
        class="scroll"
        aria-label="Table of contents"
        bind:this={scrollEl}
        onscroll={onScroll}
        style="--toc-row-h: {ROW_H}px"
      >
        <div class="sizer" style:height={`${totalHeight}px`}>
          <ul class="window" style:transform={`translateY(${offsetY}px)`}>
            {#each windowRows as row, i (row.entry.href + row.entry.title + "@" + (startIndex + i))}
              {@const hl = highlight(row.entry.title)}
              {@const globalIndex = startIndex + i}
              <li
                aria-level={row.depth + 1}
                aria-setsize={filteredRows.length}
                aria-posinset={globalIndex + 1}
              >
                <button
                  id={rowId(globalIndex)}
                  class="entry"
                  class:current={row.entry === activeEntry}
                  class:top={row.depth === 0}
                  aria-current={row.entry === activeEntry
                    ? "location"
                    : undefined}
                  tabindex={globalIndex === focusedIndex ? 0 : -1}
                  style:padding-left={`${row.depth * 0.75 + 0.75}rem`}
                  title={row.entry.title}
                  onfocus={() => (focusedIndex = globalIndex)}
                  onkeydown={(e) => onEntryKey(e, globalIndex)}
                  onclick={() => onnavigate(row.entry.href)}
                >
                  {#if hl}{hl.before}<mark>{hl.match}</mark
                    >{hl.after}{:else}{row.entry.title}{/if}
                </button>
              </li>
            {/each}
          </ul>
        </div>
      </nav>
    {/if}
  {/if}
</div>

<style>
  .toc {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--hairline);
    flex: 0 0 auto;
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 500;
    line-height: 1;
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
  .filter {
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
    font-size: var(--text-sm);
    transition: border-color var(--dur) var(--ease-out);
  }
  .field:hover {
    border-color: var(--accent);
  }
  .field::placeholder {
    color: var(--muted);
  }
  .clear {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius);
    cursor: pointer;
    flex: 0 0 auto;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out);
  }
  .clear:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: var(--sp-3) var(--sp-2) var(--sp-8);
  }
  .sizer {
    position: relative;
    width: 100%;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .window {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    will-change: transform;
  }
  .entry {
    display: block;
    height: var(--toc-row-h);
    line-height: var(--toc-row-h);
    width: 100%;
    text-align: left;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    padding: 0 var(--sp-2);
    border-radius: var(--radius);
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    transition:
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  /* Hierarchy: top-level entries read as headings (full ink, medium weight),
     nested entries recede (muted) so a Part > Chapter TOC has visible depth. */
  .entry {
    position: relative;
    color: var(--muted);
    font-weight: 400;
  }
  .entry.top {
    color: var(--fg);
    font-weight: 500;
  }
  .entry:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .entry:active {
    transform: scale(0.99);
  }
  /* Current chapter: accent text + tinted row + a left accent bar (inset shadow
     so it never changes the row height the virtual-scroll math depends on). */
  .entry.current,
  .entry.current.top {
    background: var(--surface-hover);
    color: var(--accent);
    font-weight: 600;
    box-shadow: inset 2px 0 0 var(--accent);
  }
  /* Filter match highlight: height-neutral (background + radius + horizontal
     padding only, never vertical padding or a border) so the fixed row height
     stays exact. Inherits the row's colour so it reads on muted/current rows. */
  .entry mark {
    background: color-mix(in srgb, var(--accent) 32%, transparent);
    color: inherit;
    border-radius: 2px;
    padding: 0 1px;
  }
  .empty {
    padding: var(--sp-4);
    color: var(--muted);
  }
</style>
