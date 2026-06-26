<script lang="ts">
  import { ApiError, uploadToGofile, type BookMeta } from "~/api/client";
  import { toast } from "~/lib/toast.svelte";
  import { focusTrap } from "~/lib/focusTrap";
  import Icon from "~/lib/Icon.svelte";
  import { X, UploadCloud, Copy, Check } from "@lucide/svelte";

  interface Props {
    book: BookMeta;
    onclose: () => void;
  }
  let { book, onclose }: Props = $props();

  let busy = $state(false);
  let url = $state<string | null>(null);
  let error = $state<string | null>(null);
  let copied = $state(false);

  async function upload(): Promise<void> {
    busy = true;
    error = null;
    try {
      const { downloadPage } = await uploadToGofile(book.id);
      url = downloadPage;
    } catch (err) {
      error =
        err instanceof ApiError ? err.message : "Upload to gofile failed.";
    } finally {
      busy = false;
    }
  }

  async function copyLink(): Promise<void> {
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      toast.show("Could not copy link");
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
    aria-label="Share book"
    onclick={(e) => e.stopPropagation()}
    {@attach focusTrap}
  >
    <header>
      <h2 title={book.title}>Share “{book.title}”</h2>
      <button
        class="close"
        aria-label="Close"
        onclick={onclose}
        disabled={busy}
      >
        <Icon icon={X} size={18} />
      </button>
    </header>

    <div class="body">
      <p class="lead">
        Upload the .epub to gofile.io and get a shareable link.
      </p>
      <p class="hint">
        Anonymous upload — anyone with the link can download the file.
      </p>

      <button class="btn primary upload-btn" onclick={upload} disabled={busy}>
        <Icon icon={UploadCloud} size={16} />
        {busy ? "Uploading…" : url ? "Upload again" : "Upload to gofile"}
      </button>

      {#if url}
        <div class="result">
          <a href={url} target="_blank" rel="noopener noreferrer">{url}</a>
          <button
            type="button"
            class="icon-btn"
            aria-label="Copy link"
            title="Copy link"
            onclick={copyLink}
          >
            <Icon icon={copied ? Check : Copy} size={15} />
          </button>
        </div>
      {/if}
      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}
    </div>
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
    width: min(28rem, 100%);
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
    gap: var(--sp-3);
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--hairline);
  }
  h2 {
    margin: 0;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 500;
    line-height: 1.2;
  }
  .close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
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
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
    padding: var(--sp-4);
  }
  .lead {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--fg);
  }
  .hint {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--muted);
    line-height: 1.4;
  }
  .result {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: var(--sp-2);
    border-radius: var(--radius-sm);
    background: var(--surface);
    border: 1px solid var(--hairline);
  }
  .result a {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--accent);
    font-size: var(--text-xs);
  }
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out);
  }
  .icon-btn:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }
  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
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
      transform var(--dur-fast) var(--ease-out);
  }
  .btn:active:not(:disabled) {
    transform: scale(0.97);
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .btn.primary {
    background: var(--accent);
    color: var(--accent-fg);
  }
  .btn.primary:hover:not(:disabled) {
    opacity: 0.88;
  }
  .upload-btn {
    align-self: flex-start;
  }
</style>
