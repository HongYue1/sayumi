<script lang="ts">
  import { onDestroy } from "svelte";
  import { ApiError, getCoverUrl, type BookMeta } from "~/api/client";
  import { library } from "~/lib/library.svelte";
  import { toast } from "~/lib/toast.svelte";
  import { focusTrap } from "~/lib/focusTrap";
  import Icon from "~/lib/Icon.svelte";
  import { X, ImageUp } from "@lucide/svelte";

  interface Props {
    book: BookMeta;
    onclose: () => void;
  }
  let { book, onclose }: Props = $props();

  // Snapshot the title/author at open so the dirty check compares against the
  // values the user started from, not the live store record (which the
  // optimistic editMetadata replaces mid-session).
  // svelte-ignore state_referenced_locally
  const initialTitle = book.title;
  // svelte-ignore state_referenced_locally
  const initialAuthor = book.author;

  // svelte-ignore state_referenced_locally
  let title = $state(book.title);
  // svelte-ignore state_referenced_locally
  let author = $state(book.author);

  let coverFile = $state<File | null>(null);
  let coverPreview = $state<string | null>(null);

  let busy = $state(false);
  let error = $state<string | null>(null);

  const trimmedTitle = $derived(title.trim());
  const dirty = $derived(
    trimmedTitle !== initialTitle ||
      author.trim() !== initialAuthor ||
      coverFile !== null,
  );
  const canSubmit = $derived(!busy && trimmedTitle.length > 0 && dirty);

  // The chosen file's object URL is revoked when replaced (below) and on
  // destroy, so a dialog opened/closed repeatedly doesn't leak blob URLs.
  onDestroy(() => {
    if (coverPreview) URL.revokeObjectURL(coverPreview);
  });

  function onCoverPick(e: Event): void {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    if (!file) return;
    if (!/^image\/(jpeg|png|webp)$/.test(file.type)) {
      error = "Choose a JPEG, PNG, or WebP image.";
      input.value = "";
      return;
    }
    error = null;
    if (coverPreview) URL.revokeObjectURL(coverPreview);
    coverFile = file;
    coverPreview = URL.createObjectURL(file);
  }

  const currentCover = $derived(
    coverPreview ??
      (book.hasCover ? getCoverUrl(book.id, book.updatedAt) : null),
  );

  async function submit(e: Event): Promise<void> {
    e.preventDefault();
    if (!canSubmit) return;
    busy = true;
    error = null;
    try {
      const patch: { title?: string; author?: string } = {};
      if (trimmedTitle !== initialTitle) patch.title = trimmedTitle;
      if (author.trim() !== initialAuthor) patch.author = author.trim();
      if (patch.title !== undefined || patch.author !== undefined) {
        await library.editMetadata(book.id, patch);
      }
      if (coverFile) await library.replaceCover(book.id, coverFile);
      toast.show("Saved changes");
      onclose();
    } catch (err) {
      error = err instanceof ApiError ? err.message : "Something went wrong.";
      busy = false;
    }
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on it.
      e.stopImmediatePropagation();
      if (!busy) onclose();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="overlay" role="presentation" onclick={() => !busy && onclose()}>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="sheet"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label="Edit book"
    onclick={(e) => e.stopPropagation()}
    {@attach focusTrap}
  >
    <header>
      <h2>Edit book</h2>
      <button
        class="close"
        aria-label="Close"
        onclick={onclose}
        disabled={busy}
      >
        <Icon icon={X} size={18} />
      </button>
    </header>

    <form onsubmit={submit}>
      <div class="cover-row">
        <div class="cover-preview">
          {#if currentCover}
            <img src={currentCover} alt="" />
          {:else}
            <div class="cover-placeholder">{title}</div>
          {/if}
        </div>
        <div class="cover-actions">
          <label class="btn ghost file-btn">
            <Icon icon={ImageUp} size={16} />
            {coverFile ? "Change image" : "Replace cover"}
            <input
              type="file"
              accept="image/jpeg,image/png,image/webp"
              hidden
              onchange={onCoverPick}
            />
          </label>
          {#if coverFile}
            <p class="cover-name" title={coverFile.name}>{coverFile.name}</p>
          {/if}
          <p class="hint">JPEG, PNG, or WebP · resized on save.</p>
        </div>
      </div>

      <label class="field">
        <span>Title</span>
        <input
          type="text"
          bind:value={title}
          maxlength="512"
          autocomplete="off"
          {@attach (el) => (el as HTMLInputElement).focus()}
        />
      </label>
      {#if trimmedTitle.length === 0}
        <p class="note" role="alert">Title can’t be empty.</p>
      {/if}

      <label class="field">
        <span>Author</span>
        <input
          type="text"
          bind:value={author}
          maxlength="512"
          autocomplete="off"
        />
      </label>

      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}

      <div class="actions">
        <button
          type="button"
          class="btn ghost"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button type="submit" class="btn primary" disabled={!canSubmit}>
          {busy ? "Saving…" : "Save changes"}
        </button>
      </div>
    </form>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: grid;
    place-items: center;
    padding: var(--sp-6);
    background: color-mix(in srgb, #000 38%, transparent);
    animation: app-overlay-in var(--dur-fast) var(--ease-out);
  }
  .sheet {
    width: min(30rem, 100%);
    max-height: calc(100vh - var(--sp-12));
    overflow-y: auto;
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius-lg);
    animation: app-sheet-in var(--dur) var(--ease-out);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--hairline);
    position: sticky;
    top: 0;
    background: var(--bg);
    z-index: 1;
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
  .close:active:not(:disabled) {
    transform: scale(0.94);
  }
  .close:hover:not(:disabled) {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .close:disabled {
    opacity: 0.5;
    cursor: default;
  }
  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    padding: var(--sp-4);
  }

  .cover-row {
    display: flex;
    gap: var(--sp-4);
    align-items: flex-start;
  }
  .cover-preview {
    flex-shrink: 0;
    width: 6rem;
    aspect-ratio: 2 / 3;
    border-radius: var(--radius);
    overflow: hidden;
    background: var(--surface);
    border: 1px solid var(--hairline);
  }
  .cover-preview img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }
  .cover-placeholder {
    width: 100%;
    height: 100%;
    display: grid;
    place-items: center;
    padding: var(--sp-2);
    text-align: center;
    font-family: var(--font-display);
    font-size: var(--text-xs);
    color: var(--muted);
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }
  .cover-actions {
    display: flex;
    flex-direction: column;
    gap: var(--sp-2);
    min-width: 0;
  }
  .file-btn {
    cursor: pointer;
    align-self: flex-start;
  }
  .cover-name {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--fg);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 14rem;
  }
  .hint {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--muted);
    line-height: 1.4;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .field > span {
    font-size: var(--text-sm);
    color: var(--fg);
  }
  .field input {
    height: 2.4rem;
    padding: 0 0.7rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    transition:
      border-color var(--dur) var(--ease-out),
      box-shadow var(--dur) var(--ease-out);
  }
  .field input:hover {
    border-color: var(--accent);
  }
  .field input:focus-visible {
    outline: none;
    border-color: var(--accent);
    box-shadow: var(--focus);
  }

  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }
  .note {
    margin: 0;
    margin-top: calc(var(--sp-3) * -1);
    color: var(--danger);
    font-size: var(--text-xs);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--sp-2);
    margin-top: var(--sp-1);
  }
  .btn {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
    height: 2.4rem;
    padding: 0 1rem;
    border: 1px solid transparent;
    border-radius: var(--radius);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      opacity var(--dur) var(--ease-out),
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .btn:active:not(:disabled) {
    transform: scale(0.97);
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .btn.ghost {
    background: transparent;
    border-color: var(--hairline-strong);
    color: var(--fg);
  }
  .btn.ghost:hover:not(:disabled) {
    background: var(--surface-hover);
  }
  .btn.primary {
    background: var(--accent);
    color: var(--accent-fg);
  }
  .btn.primary:hover:not(:disabled) {
    opacity: 0.88;
  }
</style>
