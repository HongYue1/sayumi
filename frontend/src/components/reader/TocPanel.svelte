<script lang="ts">
  import type { TocEntry } from "~/api/client";

  interface Props {
    toc: TocEntry[];
    onnavigate: (href: string) => void;
  }

  let { toc, onnavigate }: Props = $props();
</script>

{#snippet node(entry: TocEntry)}
  <li>
    <button
      class="entry"
      style:padding-left={`${entry.depth * 0.75 + 0.75}rem`}
      onclick={() => onnavigate(entry.href)}
    >
      {entry.title}
    </button>
    {#if entry.children?.length}
      <ul>
        {#each entry.children as child (child.href + child.title)}
          {@render node(child)}
        {/each}
      </ul>
    {/if}
  </li>
{/snippet}

<nav class="toc" aria-label="Table of contents">
  <h2>Contents</h2>
  {#if toc.length === 0}
    <p class="empty">No table of contents.</p>
  {:else}
    <ul class="root">
      {#each toc as entry (entry.href + entry.title)}
        {@render node(entry)}
      {/each}
    </ul>
  {/if}
</nav>

<style>
  .toc {
    height: 100%;
    overflow-y: auto;
    padding: 1rem 0.5rem 2rem;
  }
  h2 {
    margin: 0 0 0.75rem 0.75rem;
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--muted, #6b6661);
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
    font-size: 0.9rem;
    line-height: 1.3;
    padding: 0.4rem 0.5rem;
    border-radius: 0.4rem;
    cursor: pointer;
  }
  .entry:hover {
    background: color-mix(in srgb, var(--accent) 12%, transparent);
  }
  .empty {
    margin: 0 0.75rem;
    color: var(--muted, #6b6661);
  }
</style>
