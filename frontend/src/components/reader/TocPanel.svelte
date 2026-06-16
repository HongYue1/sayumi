<script lang="ts">
  import type { TocEntry } from "~/api/client";

  interface Props {
    toc: TocEntry[];
    activeEntry: TocEntry | null;
    onnavigate: (href: string) => void;
  }

  let { toc, activeEntry, onnavigate }: Props = $props();

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
  // Match on reference identity (not href) so exactly one row is current even
  // when two entries point at the same file.
  const activeIndex = $derived(
    activeEntry ? rows.findIndex((r) => r.entry === activeEntry) : -1,
  );

  let scrollEl: HTMLElement | null = $state(null);
  let scrollTop = $state(0);
  let viewportH = $state(0);
  let initialised = false;
  let needsFocus = false;

  const startIndex = $derived(
    Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN),
  );
  const endIndex = $derived(
    Math.min(
      rows.length,
      startIndex + Math.ceil(viewportH / ROW_H) + OVERSCAN * 2,
    ),
  );
  const windowRows = $derived(rows.slice(startIndex, endIndex));
  const totalHeight = $derived(rows.length * ROW_H);
  const offsetY = $derived(startIndex * ROW_H);

  function onScroll(): void {
    if (scrollEl) scrollTop = scrollEl.scrollTop;
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
  // at the top of a long book. Runs once.
  $effect(() => {
    if (initialised || !scrollEl) return;
    viewportH = scrollEl.clientHeight;
    if (activeIndex >= 0) {
      const target = activeIndex * ROW_H - (viewportH - ROW_H) / 2;
      scrollEl.scrollTop = Math.max(0, target);
      needsFocus = true;
    }
    scrollTop = scrollEl.scrollTop;
    initialised = true;
  });

  // Once the current row is rendered in the window, move focus to it (once) so
  // the focus trap defers to us instead of grabbing the first row (the book
  // title) and yanking the scroll back to the top. preventScroll keeps the
  // centered position. Re-runs as the window fills, so it still lands even if
  // the row wasn't mounted on the first pass.
  $effect(() => {
    void windowRows;
    if (!needsFocus || !scrollEl) return;
    const current = scrollEl.querySelector<HTMLElement>(".entry.current");
    if (current) {
      current.focus({ preventScroll: true });
      needsFocus = false;
    }
  });
</script>

<div class="toc">
  <h2 class="eyebrow">Contents</h2>
  {#if rows.length === 0}
    <p class="empty">No table of contents.</p>
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
            <li aria-level={row.depth + 1}>
              <button
                class="entry"
                class:current={row.entry === activeEntry}
                class:top={row.depth === 0}
                aria-current={row.entry === activeEntry
                  ? "location"
                  : undefined}
                style:padding-left={`${row.depth * 0.75 + 0.75}rem`}
                title={row.entry.title}
                onclick={() => onnavigate(row.entry.href)}
              >
                {row.entry.title}
              </button>
            </li>
          {/each}
        </ul>
      </div>
    </nav>
  {/if}
</div>

<style>
  .toc {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  h2 {
    margin: 0 0 var(--sp-3) var(--sp-3);
    padding-top: var(--sp-4);
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 var(--sp-2) var(--sp-8);
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
    padding: 0 0.5rem;
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
  .empty {
    margin: 0 var(--sp-3);
    color: var(--muted);
  }
</style>
