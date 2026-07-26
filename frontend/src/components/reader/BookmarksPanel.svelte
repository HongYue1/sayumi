<script lang="ts">
  import type { Bookmark } from "~/api/client";
  import Icon from "~/lib/Icon.svelte";
  import { X, Pencil, Trash2 } from "@lucide/svelte";

  interface Props {
    bookmarks: Bookmark[];
    /** Resolves a chapter index to its TOC heading (null → fall back). */
    chapterTitle?: (chapter: number) => string | null;
    onnavigate: (bm: Bookmark) => void;
    ondelete: (id: string) => void;
    onupdate: (id: string, label: string, comment: string) => void;
    onclose: () => void;
  }
  let {
    bookmarks,
    chapterTitle = () => null,
    onnavigate,
    ondelete,
    onupdate,
    onclose,
  }: Props = $props();

  const MAX_BOOKMARK_TEXT_BYTES = 2000;
  const textEncoder = new TextEncoder();

  let editingId = $state<string | null>(null);
  let editLabel = $state("");
  let editComment = $state("");
  let editError = $state("");
  let returnFocusId = $state<string | null>(null);

  const sorted = $derived(
    [...bookmarks].sort(
      (a, b) => a.chapter - b.chapter || a.percent - b.percent,
    ),
  );

  function bookmarkName(bm: Bookmark): string {
    // Prefer the user's label, then the chapter's real TOC heading.
    return bm.label || chapterTitle(bm.chapter) || `Chapter ${bm.chapter + 1}`;
  }

  function startEdit(bm: Bookmark): void {
    editingId = bm.id;
    editLabel = bm.label;
    editComment = bm.comment;
    editError = "";
    returnFocusId = null;
  }
  function saveEdit(id: string): void {
    const label = editLabel.trim();
    const comment = editComment.trim();
    const labelBytes = textEncoder.encode(label).byteLength;
    const commentBytes = textEncoder.encode(comment).byteLength;
    if (labelBytes > MAX_BOOKMARK_TEXT_BYTES) {
      editError = `Label must be ${MAX_BOOKMARK_TEXT_BYTES} UTF-8 bytes or fewer (currently ${labelBytes}).`;
      return;
    }
    if (commentBytes > MAX_BOOKMARK_TEXT_BYTES) {
      editError = `Note must be ${MAX_BOOKMARK_TEXT_BYTES} UTF-8 bytes or fewer (currently ${commentBytes}).`;
      return;
    }
    onupdate(id, label, comment);
    finishEdit(id);
  }

  function finishEdit(id: string): void {
    returnFocusId = id;
    editingId = null;
    editError = "";
  }

  function restoreEditFocus(node: HTMLButtonElement, id: string): void {
    if (returnFocusId !== id) return;
    returnFocusId = null;
    node.focus({ preventScroll: true });
  }

  // Keep edit-mode keys local: Esc cancels (and is stopped from bubbling to the
  // reader's own Esc handler), Enter in the single-line label field saves. The
  // note textarea keeps Enter for newlines.
  function onEditKey(e: KeyboardEvent, id: string): void {
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.stopPropagation();
      e.preventDefault();
      finishEdit(id);
    } else if (e.key === "Enter" && e.target instanceof HTMLInputElement) {
      e.preventDefault();
      saveEdit(id);
    }
  }
</script>

<div class="bookmarks">
  <header>
    <div class="head-text">
      <p class="eyebrow">Reader</p>
      <h2 class="display">Bookmarks</h2>
    </div>
    <button
      class="icon-btn press close"
      onclick={onclose}
      aria-label="Close bookmarks"><Icon icon={X} size={18} /></button
    >
  </header>

  <div class="list">
    {#if sorted.length === 0}
      <p class="empty">
        No bookmarks yet. Press <kbd class="kbd">B</kbd> while reading to add one.
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
                  aria-describedby={editError
                    ? `bookmark-edit-error-${bm.id}`
                    : undefined}
                  maxlength={MAX_BOOKMARK_TEXT_BYTES}
                  oninput={() => (editError = "")}
                  onkeydown={(e) => onEditKey(e, bm.id)}
                  {@attach (el) => (el as HTMLInputElement).focus()}
                />
                <textarea
                  class="field"
                  bind:value={editComment}
                  placeholder="Note…"
                  rows="3"
                  aria-label="Bookmark note"
                  aria-describedby={editError
                    ? `bookmark-edit-error-${bm.id}`
                    : undefined}
                  maxlength={MAX_BOOKMARK_TEXT_BYTES}
                  oninput={() => (editError = "")}
                  onkeydown={(e) => onEditKey(e, bm.id)}></textarea>
                {#if editError}
                  <p
                    class="edit-error"
                    id={`bookmark-edit-error-${bm.id}`}
                    role="alert"
                  >
                    {editError}
                  </p>
                {/if}
                <div class="actions">
                  <button
                    class="btn press small"
                    onclick={() => saveEdit(bm.id)}>Save</button
                  >
                  <button
                    class="btn-ghost press small"
                    onclick={() => finishEdit(bm.id)}>Cancel</button
                  >
                </div>
              </div>
            {:else}
              <span class="ribbon" aria-hidden="true"></span>
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
                  class="row-btn press"
                  onclick={() => startEdit(bm)}
                  aria-label={`Edit bookmark: ${bookmarkName(bm)}`}
                  {@attach (node) =>
                    restoreEditFocus(node as HTMLButtonElement, bm.id)}
                  ><Icon icon={Pencil} size={15} /></button
                >
                <button
                  class="row-btn press danger"
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
    color: var(--fg);
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-4) var(--sp-4) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
    flex: 0 0 auto;
  }
  .head-text {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .head-text .eyebrow {
    margin: 0;
  }
  h2 {
    margin: 0;
    font-size: var(--text-xl);
    font-weight: 540;
    line-height: var(--lh-tight);
  }
  .close {
    flex-shrink: 0;
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
    font-family: var(--font-display);
    font-style: italic;
  }
  .bm {
    position: relative;
    display: flex;
    gap: var(--sp-2);
    align-items: flex-start;
    padding: var(--sp-2) 0 var(--sp-2) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
  }
  .bm:last-child {
    border-bottom: none;
  }
  /* A small accent ribbon hanging into each row — the bookmark itself. */
  .ribbon {
    position: absolute;
    left: 0;
    top: 0;
    width: 7px;
    height: 1.35rem;
    background: var(--accent);
    opacity: 0.85;
    clip-path: polygon(0 0, 100% 0, 100% 100%, 50% calc(100% - 4px), 0 100%);
  }
  .edit {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .open {
    flex: 1;
    min-width: 0;
    text-align: left;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.35rem 0.45rem;
    border-radius: var(--radius-sm);
    transition: background var(--dur-fast) var(--ease-out);
  }
  .open:hover {
    background: var(--surface-hover);
  }
  .bm-label {
    font-weight: 560;
    font-size: var(--text-sm);
    overflow-wrap: anywhere;
  }
  .bm-meta {
    font-size: var(--text-xs);
    font-weight: 560;
    letter-spacing: 0.04em;
    color: var(--faint);
  }
  .bm-comment {
    font-size: var(--text-sm);
    color: var(--muted);
    margin-top: var(--sp-1);
    overflow-wrap: anywhere;
  }
  .actions {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  .edit .actions {
    margin-top: 0.2rem;
    gap: var(--sp-2);
  }
  .small {
    font-size: var(--text-xs);
    padding: 0.35rem 0.8rem;
  }
  .edit-error {
    margin: 0 0 var(--sp-2);
    padding: var(--sp-2);
    border-radius: var(--radius-sm);
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
    font-size: var(--text-xs);
    overflow-wrap: anywhere;
  }
  .field {
    margin-bottom: 0.35rem;
    resize: vertical;
  }
  .row-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    cursor: pointer;
    padding: 0.35rem;
    border-radius: var(--radius-sm);
    transition:
      background var(--dur-fast) var(--ease-out),
      color var(--dur-fast) var(--ease-out);
  }
  .row-btn:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .row-btn.danger:hover {
    color: var(--danger);
    background: color-mix(in srgb, var(--danger) 10%, transparent);
  }
</style>
