<script lang="ts">
  import type { Bookmark } from "~/api/client";

  interface Props {
    bookmarks: Bookmark[];
    onnavigate: (bm: Bookmark) => void;
    ondelete: (id: string) => void;
    onupdate: (id: string, label: string, comment: string) => void;
    onclose: () => void;
  }
  let { bookmarks, onnavigate, ondelete, onupdate, onclose }: Props = $props();

  let editingId = $state<string | null>(null);
  let editLabel = $state("");
  let editComment = $state("");

  const sorted = $derived(
    [...bookmarks].sort((a, b) => a.chapter - b.chapter || a.percent - b.percent),
  );

  function startEdit(bm: Bookmark): void {
    editingId = bm.id;
    editLabel = bm.label;
    editComment = bm.comment;
  }
  function saveEdit(id: string): void {
    onupdate(id, editLabel.trim(), editComment.trim());
    editingId = null;
  }
</script>

<div class="bookmarks">
  <header>
    <h2>Bookmarks</h2>
    <button class="close" onclick={onclose} aria-label="Close bookmarks">×</button>
  </header>

  <div class="list">
    {#if sorted.length === 0}
      <p class="empty">No bookmarks yet. Press <kbd>B</kbd> while reading to add one.</p>
    {:else}
      {#each sorted as bm (bm.id)}
        <div class="bm">
          {#if editingId === bm.id}
            <input class="field" bind:value={editLabel} placeholder="Label" />
            <textarea class="field" bind:value={editComment} placeholder="Note…" rows="3"></textarea>
            <div class="actions">
              <button class="primary" onclick={() => saveEdit(bm.id)}>Save</button>
              <button class="ghost" onclick={() => (editingId = null)}>Cancel</button>
            </div>
          {:else}
            <button class="open" onclick={() => onnavigate(bm)}>
              <span class="bm-label">{bm.label || `Chapter ${bm.chapter + 1}`}</span>
              <span class="bm-meta">Ch {bm.chapter + 1} · {Math.round(bm.percent * 100)}%</span>
              {#if bm.comment}<span class="bm-comment">{bm.comment}</span>{/if}
            </button>
            <div class="actions">
              <button class="ghost" onclick={() => startEdit(bm)} aria-label="Edit">✎</button>
              <button class="ghost danger" onclick={() => ondelete(bm.id)} aria-label="Delete">🗑</button>
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .bookmarks {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    color: var(--fg);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 10%, transparent);
    flex: 0 0 auto;
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: 1.4rem;
    font-weight: 500;
    line-height: 1;
  }
  .close {
    border: none;
    background: transparent;
    color: var(--fg);
    font-size: 1.4rem;
    line-height: 1;
    cursor: pointer;
  }
  .list {
    overflow-y: auto;
    padding: 0.5rem 0.75rem 2rem;
  }
  .empty {
    color: var(--muted, #6b6661);
    padding: 0.5rem;
  }
  kbd {
    border: 1px solid color-mix(in srgb, var(--fg) 25%, transparent);
    border-radius: 0.25rem;
    padding: 0 0.3rem;
    font-size: 0.85em;
  }
  .bm {
    display: flex;
    gap: 0.4rem;
    align-items: flex-start;
    padding: 0.5rem 0;
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 8%, transparent);
  }
  .open {
    flex: 1;
    text-align: left;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.2rem;
    border-radius: 0.4rem;
  }
  .open:hover {
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }
  .bm-label {
    font-weight: 500;
    font-size: 0.9rem;
  }
  .bm-meta {
    font-size: 0.75rem;
    color: var(--muted, #6b6661);
    font-variant-numeric: tabular-nums;
  }
  .bm-comment {
    font-size: 0.82rem;
    color: var(--muted, #6b6661);
    margin-top: 0.15rem;
  }
  .actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .field {
    width: 100%;
    padding: 0.4rem 0.5rem;
    margin-bottom: 0.35rem;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.4rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    font-size: 0.85rem;
    resize: vertical;
  }
  .primary {
    border: none;
    background: var(--accent);
    color: #fff;
    font: inherit;
    font-size: 0.82rem;
    padding: 0.35rem 0.7rem;
    border-radius: 0.4rem;
    cursor: pointer;
  }
  .ghost {
    border: none;
    background: transparent;
    color: var(--muted, #6b6661);
    font: inherit;
    cursor: pointer;
    padding: 0.25rem 0.4rem;
    border-radius: 0.4rem;
  }
  .ghost:hover {
    background: color-mix(in srgb, var(--fg) 8%, transparent);
    color: var(--fg);
  }
  .ghost.danger:hover {
    color: #b3402f;
  }
</style>
