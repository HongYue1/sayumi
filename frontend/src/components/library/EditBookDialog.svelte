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
      <div class="head-text">
        <p class="eyebrow">Library</p>
        <h2 class="display">Edit book</h2>
      </div>
      <button
        class="icon-btn press close"
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
            <div class="cover-placeholder display">{title}</div>
          {/if}
        </div>
        <div class="cover-actions">
          <label class="btn-ghost press file-btn" class:disabled={busy}>
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

      <label class="frow">
        <span class="lbl">Title</span>
        <input
          class="field"
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

      <label class="frow">
        <span class="lbl">Author</span>
        <input
          class="field"
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
          class="btn-ghost press"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button type="submit" class="btn press" disabled={!canSubmit}>
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
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .sheet {
    width: min(30rem, 100%);
    max-height: calc(100vh - var(--sp-12));
    overflow-y: auto;
    background: var(--raised);
    border: 1px solid var(--hairline);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-3);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-5) var(--sp-5) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
    position: sticky;
    top: 0;
    background: var(--raised);
    z-index: 1;
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
  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    padding: var(--sp-5);
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
    border-radius: 3px 7px 7px 3px;
    overflow: hidden;
    background: var(--surface);
    box-shadow: var(--shadow-1);
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
    font-style: italic;
    font-size: var(--text-xs);
    color: var(--muted);
    background: linear-gradient(
      160deg,
      var(--surface),
      color-mix(in srgb, var(--accent) 10%, transparent)
    );
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

  .frow {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .lbl {
    font-size: var(--text-xs);
    font-weight: 640;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .frow input {
    height: 2.5rem;
  }
  .frow input:disabled {
    opacity: 0.7;
    cursor: not-allowed;
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
    padding-top: var(--sp-4);
    border-top: 1px solid var(--hairline);
  }
</style>
