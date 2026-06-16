<script lang="ts">
  import type { TocEntry } from "~/api/client";

  interface Props {
    toc: TocEntry[];
    activeHref: string | null;
    onnavigate: (href: string) => void;
  }

  let { toc, activeHref, onnavigate }: Props = $props();

  let navEl: HTMLElement | null = $state(null);
  // When the panel opens (or the highlighted entry changes), scroll the current
  // chapter into view so a reader deep in a long book doesn't open the TOC at
  // chapter 1. Instant scroll (no behavior:"smooth") keeps it reduced-motion
  // friendly.
  $effect(() => {
    void activeHref;
    navEl?.querySelector(".entry.current")?.scrollIntoView({ block: "center" });
  });
</script>

{#snippet node(entry: TocEntry)}
  <li>
    <button
      class="entry"
      class:current={entry.href === activeHref}
      aria-current={entry.href === activeHref ? "location" : undefined}
      style:padding-left={`${entry.depth * 0.75 + 0.75}rem`}
      onclick={() => onnavigate(entry.href)}
    >
      {entry.title}
    </button>
    {#if entry.children?.length}
      <ul>
        {#each entry.children as child, i (child.href + child.title + "@" + i)}
          {@render node(child)}
        {/each}
      </ul>
    {/if}
  </li>
{/snippet}

<nav class="toc" aria-label="Table of contents" bind:this={navEl}>
  <h2 class="eyebrow">Contents</h2>
  {#if toc.length === 0}
    <p class="empty">No table of contents.</p>
  {:else}
    <ul class="root">
      {#each toc as entry, i (entry.href + entry.title + "@" + i)}
        {@render node(entry)}
      {/each}
    </ul>
  {/if}
</nav>

<style>
  .toc {
    height: 100%;
    overflow-y: auto;
    padding: var(--sp-4) var(--sp-2) var(--sp-8);
  }
  h2 {
    margin: 0 0 var(--sp-3) var(--sp-3);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .entry {
    display: block;
    width: 100%;
    text-align: left;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    line-height: var(--lh-snug);
    padding: 0.4rem 0.5rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .entry:hover {
    background: var(--surface-hover);
  }
  .entry:active {
    transform: scale(0.99);
  }
  .entry.current {
    background: var(--surface-hover);
    color: var(--accent);
    font-weight: 600;
  }
  .empty {
    margin: 0 var(--sp-3);
    color: var(--muted);
  }
</style>
