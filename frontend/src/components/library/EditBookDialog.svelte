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

  const MAX_META_BYTES = 512;
  const MAX_COVER_BYTES = 20 * 1024 * 1024;
  const textEncoder = new TextEncoder();

  // Start from the values at open, then advance these baselines after a
  // successful metadata stage. If a following cover upload fails, the dialog
  // retries only that failed stage instead of presenting saved details as dirty.
  // svelte-ignore state_referenced_locally
  let savedTitle = $state(book.title);
  // svelte-ignore state_referenced_locally
  let savedAuthor = $state(book.author);

  // svelte-ignore state_referenced_locally
  let title = $state(book.title);
  // svelte-ignore state_referenced_locally
  let author = $state(book.author);

  let coverFile = $state<File | null>(null);
  let coverPreview = $state<string | null>(null);

  let busy = $state(false);
  let error = $state<string | null>(null);
  let coverPickError = $state<string | null>(null);

  const trimmedTitle = $derived(title.trim());
  const trimmedAuthor = $derived(author.trim());
  const titleTooLong = $derived(
    textEncoder.encode(trimmedTitle).byteLength > MAX_META_BYTES,
  );
  const authorTooLong = $derived(
    textEncoder.encode(trimmedAuthor).byteLength > MAX_META_BYTES,
  );
  const titleError = $derived(
    trimmedTitle.length === 0
      ? "Title can’t be empty."
      : titleTooLong
        ? "Title is too long (512-byte limit)."
        : null,
  );
  const dirty = $derived(
    trimmedTitle !== savedTitle ||
      trimmedAuthor !== savedAuthor ||
      coverFile !== null,
  );
  const canSubmit = $derived(
    !busy && titleError === null && !authorTooLong && dirty,
  );

  // The chosen file's object URL is revoked when replaced (below) and on
  // destroy, so a dialog opened/closed repeatedly doesn't leak blob URLs.
  onDestroy(() => {
    if (coverPreview) URL.revokeObjectURL(coverPreview);
  });

  function clearCoverSelection(): void {
    if (coverPreview) URL.revokeObjectURL(coverPreview);
    coverFile = null;
    coverPreview = null;
  }

  function onCoverPick(e: Event): void {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    if (!file) return;
    error = null;
    if (!/^image\/(jpeg|png|webp)$/.test(file.type)) {
      clearCoverSelection();
      coverPickError = "Choose a JPEG, PNG, or WebP image.";
      input.value = "";
      return;
    }
    if (file.size > MAX_COVER_BYTES) {
      clearCoverSelection();
      coverPickError = "Choose an image no larger than 20 MB.";
      input.value = "";
      return;
    }
    coverPickError = null;
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

    // Freeze one coherent submission. Controls are disabled below while busy,
    // but these snapshots also prevent a late file-picker event from changing
    // which values an already-running save commits.
    const submittedTitle = trimmedTitle;
    const submittedAuthor = trimmedAuthor;
    const submittedCover = coverFile;
    let savedDetailsThisAttempt = false;

    busy = true;
    error = null;
    coverPickError = null;
    try {
      const patch: { title?: string; author?: string } = {};
      if (submittedTitle !== savedTitle) patch.title = submittedTitle;
      if (submittedAuthor !== savedAuthor) patch.author = submittedAuthor;
      if (patch.title !== undefined || patch.author !== undefined) {
        await library.editMetadata(book.id, patch);
        savedTitle = submittedTitle;
        savedAuthor = submittedAuthor;
        savedDetailsThisAttempt = true;
      }
      if (submittedCover) await library.replaceCover(book.id, submittedCover);
      toast.show("Saved changes");
      onclose();
    } catch (err) {
      const message =
        err instanceof ApiError ? err.message : "Something went wrong.";
      error =
        savedDetailsThisAttempt && submittedCover
          ? `Book details were saved, but the cover could not be replaced: ${message}`
          : message;
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

    <form onsubmit={submit} aria-busy={busy}>
      <div class="cover-row">
        <div class="cover-preview">
          {#if currentCover}
            <img src={currentCover} alt="" />
          {:else}
            <div class="cover-placeholder">{title}</div>
          {/if}
        </div>
        <div class="cover-actions">
          <label class="btn ghost file-btn" class:disabled={busy}>
            <Icon icon={ImageUp} size={16} />
            {coverFile ? "Change image" : "Replace cover"}
            <input
              class="file-input"
              type="file"
              accept="image/jpeg,image/png,image/webp"
              aria-label={coverFile ? "Change cover image" : "Replace cover"}
              aria-invalid={coverPickError !== null}
              aria-describedby={coverPickError
                ? "cover-hint cover-pick-error"
                : "cover-hint"}
              disabled={busy}
              onchange={onCoverPick}
            />
          </label>
          {#if coverFile}
            <p class="cover-name" title={coverFile.name}>{coverFile.name}</p>
          {/if}
          <p class="hint" id="cover-hint">
            JPEG, PNG, or WebP · up to 20 MB · resized on save.
          </p>
          {#if coverPickError}
            <p class="error" id="cover-pick-error" role="alert">
              {coverPickError}
            </p>
          {/if}
        </div>
      </div>

      <label class="field">
        <span>Title</span>
        <input
          type="text"
          bind:value={title}
          maxlength="512"
          autocomplete="off"
          aria-invalid={titleError !== null}
          aria-describedby={titleError ? "book-title-error" : undefined}
          disabled={busy}
          {@attach (el) => (el as HTMLInputElement).focus()}
        />
      </label>
      {#if titleError}
        <p class="note" id="book-title-error" role="alert">{titleError}</p>
      {/if}

      <label class="field">
        <span>Author</span>
        <input
          type="text"
          bind:value={author}
          maxlength="512"
          autocomplete="off"
          aria-invalid={authorTooLong}
          aria-describedby={authorTooLong ? "book-author-error" : undefined}
          disabled={busy}
        />
      </label>
      {#if authorTooLong}
        <p class="note" id="book-author-error" role="alert">
          Author is too long (512-byte limit).
        </p>
      {/if}

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
    position: relative;
    cursor: pointer;
    align-self: flex-start;
    overflow: hidden;
  }
  .file-btn:has(.file-input:focus-visible) {
    box-shadow: var(--focus);
  }
  .file-btn.disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .file-input {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    opacity: 0;
    cursor: pointer;
  }
  .file-input:disabled {
    cursor: not-allowed;
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
  .field input:disabled {
    opacity: 0.7;
    cursor: not-allowed;
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
