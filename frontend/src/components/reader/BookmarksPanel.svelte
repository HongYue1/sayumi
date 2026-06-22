<script lang="ts">
  import type { Bookmark } from "~/api/client";
  import Icon from "~/lib/Icon.svelte";
  import { X, Pencil, Trash2 } from "@lucide/svelte";

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
    [...bookmarks].sort(
      (a, b) => a.chapter - b.chapter || a.percent - b.percent,
    ),
  );

  function bookmarkName(bm: Bookmark): string {
    return bm.label || `Chapter ${bm.chapter + 1}`;
  }

  function startEdit(bm: Bookmark): void {
    editingId = bm.id;
    editLabel = bm.label;
    editComment = bm.comment;
  }
  function saveEdit(id: string): void {
    onupdate(id, editLabel.trim(), editComment.trim());
    editingId = null;
  }

  // Keep edit-mode keys local: Esc cancels (and is stopped from bubbling to the
  // reader's own Esc handler), Enter in the single-line label field saves. The
  // note textarea keeps Enter for newlines.
  function onEditKey(e: KeyboardEvent, id: string): void {
    if (e.key === "Escape") {
      e.stopPropagation();
      e.preventDefault();
      editingId = null;
    } else if (e.key === "Enter" && e.target instanceof HTMLInputElement) {
      e.preventDefault();
      saveEdit(id);
    }
  }
</script>

<div class="bookmarks">
  <header>
    <h2>Bookmarks</h2>
    <button class="close" onclick={onclose} aria-label="Close bookmarks"
      ><Icon icon={X} size={18} /></button
    >
  </header>

  <div class="list">
    {#if sorted.length === 0}
      <p class="empty">
        No bookmarks yet. Press <kbd>B</kbd> while reading to add one.
      </p>
    {:else}
      <ul class="bm-list">
        {#each sorted as bm (bm.id)}
          <li class="bm">
            {#if editingId === bm.id}
              <div class="edit">
                <input
                  class="field"
                  bind:value={editLabel}
                  placeholder="Label"
                  aria-label="Bookmark label"
                  onkeydown={(e) => onEditKey(e, bm.id)}
                  {@attach (el) => (el as HTMLInputElement).focus()}
                />
                <textarea
                  class="field"
                  bind:value={editComment}
                  placeholder="Note…"
                  rows="3"
                  aria-label="Bookmark note"
                  onkeydown={(e) => onEditKey(e, bm.id)}
                ></textarea>
                <div class="actions">
                  <button class="primary" onclick={() => saveEdit(bm.id)}
                    >Save</button
                  >
                  <button class="ghost-btn" onclick={() => (editingId = null)}
                    >Cancel</button
                  >
                </div>
              </div>
            {:else}
              <button
                class="open"
                onclick={() => onnavigate(bm)}
                aria-label={`Go to bookmark: ${bookmarkName(bm)}, chapter ${bm.chapter + 1}, ${Math.round(bm.percent * 100)}%`}
              >
                <span class="bm-label">{bookmarkName(bm)}</span>
                <span class="bm-meta tnum"
                  >Ch {bm.chapter + 1} · {Math.round(bm.percent * 100)}%</span
                >
                {#if bm.comment}<span class="bm-comment">{bm.comment}</span
                  >{/if}
              </button>
              <div class="actions">
                <button
                  class="icon-btn"
                  onclick={() => startEdit(bm)}
                  aria-label={`Edit bookmark: ${bookmarkName(bm)}`}
                  ><Icon icon={Pencil} size={15} /></button
                >
                <button
                  class="icon-btn danger"
                  onclick={() => ondelete(bm.id)}
                  aria-label={`Delete bookmark: ${bookmarkName(bm)}`}
                  ><Icon icon={Trash2} size={15} /></button
                >
              </div>
            {/if}
          </li>
        {/each}
      </ul>
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
  .list {
    overflow-y: auto;
    padding: var(--sp-2) var(--sp-3) var(--sp-8);
  }
  .bm-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .empty {
    color: var(--muted);
    padding: var(--sp-2);
  }
  kbd {
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius-sm);
    padding: 0 0.3rem;
    font-size: 0.85em;
  }
  .bm {
    display: flex;
    gap: var(--sp-2);
    align-items: flex-start;
    padding: var(--sp-2) 0;
    border-bottom: 1px solid var(--hairline);
  }
  .edit {
    flex: 1;
    display: flex;
    flex-direction: column;
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
    padding: 0.3rem;
    border-radius: var(--radius);
    transition: background var(--dur-fast) var(--ease-out);
  }
  .open:hover {
    background: var(--surface-hover);
  }
  .bm-label {
    font-weight: 500;
    font-size: var(--text-sm);
  }
  .bm-meta {
    font-size: var(--text-xs);
    color: var(--muted);
  }
  .bm-comment {
    font-size: var(--text-sm);
    color: var(--muted);
    margin-top: 0.15rem;
  }
  .actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .edit .actions {
    margin-top: 0.2rem;
  }
  .field {
    width: 100%;
    padding: 0.4rem 0.5rem;
    margin-bottom: 0.35rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    resize: vertical;
    transition: border-color var(--dur) var(--ease-out);
  }
  .field:hover {
    border-color: var(--accent);
  }
  .primary {
    border: none;
    background: var(--accent);
    color: var(--accent-fg);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 700;
    padding: 0.35rem 0.8rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      opacity var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .primary:hover {
    opacity: 0.88;
  }
  .primary:active {
    transform: scale(0.97);
  }
  .ghost-btn {
    border: 1px solid var(--hairline-strong);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    padding: 0.35rem 0.8rem;
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
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    cursor: pointer;
    padding: 0.3rem;
    border-radius: var(--radius);
    transition:
      background var(--dur-fast) var(--ease-out),
      color var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .icon-btn:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .icon-btn:active {
    transform: scale(0.95);
  }
  .icon-btn.danger:hover {
    color: var(--danger);
  }
</style>
