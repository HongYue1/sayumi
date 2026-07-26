<script lang="ts">
  import { onDestroy } from "svelte";
  import {
    ApiError,
    getDownloadUrl,
    uploadToGofile,
    type BookMeta,
  } from "~/api/client";
  import { toast } from "~/lib/toast.svelte";
  import { focusTrap } from "~/lib/focusTrap";
  import Icon from "~/lib/Icon.svelte";
  import { X, UploadCloud, Copy, Check, Download } from "@lucide/svelte";

  interface Props {
    book: BookMeta;
    onclose: () => void;
  }
  let { book, onclose }: Props = $props();

  // Direct local download: a same-origin <a download> hitting the file endpoint
  // streams the .epub with Content-Disposition: attachment, so the browser
  // saves it without any JS. The download attribute is just a filename hint;
  // the server's Content-Disposition is authoritative.
  const downloadUrl = $derived(getDownloadUrl(book.id));
  const downloadName = $derived(`${book.title || "book"}.epub`);

  let busy = $state(false);
  let url = $state<string | null>(null);
  let error = $state<string | null>(null);
  let copied = $state(false);
  let uploadController: AbortController | null = null;
  let copiedResetTimer: ReturnType<typeof setTimeout> | null = null;

  async function upload(): Promise<void> {
    if (busy) return;
    const controller = new AbortController();
    uploadController = controller;
    busy = true;
    error = null;
    try {
      const { downloadPage } = await uploadToGofile(book.id, controller.signal);
      url = downloadPage;
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      error =
        err instanceof ApiError ? err.message : "Upload to gofile failed.";
    } finally {
      if (uploadController === controller) {
        uploadController = null;
        busy = false;
      }
    }
  }

  async function copyLink(): Promise<void> {
    if (!url) return;
    try {
      await navigator.clipboard.writeText(url);
      copied = true;
      toast.show("Link copied");
      if (copiedResetTimer !== null) clearTimeout(copiedResetTimer);
      copiedResetTimer = setTimeout(() => {
        copied = false;
        copiedResetTimer = null;
      }, 1500);
    } catch {
      toast.show("Could not copy link");
    }
  }

  function close(): void {
    uploadController?.abort();
    onclose();
  }

  onDestroy(() => {
    uploadController?.abort();
    if (copiedResetTimer !== null) clearTimeout(copiedResetTimer);
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on it.
      e.stopImmediatePropagation();
      close();
    }
  }
</script>

<!-- Capture phase: the dialog mounts after the page's own window key
     listeners, so a bubble listener here runs last and can't pre-empt them;
     capture runs first regardless of registration order. -->
<svelte:window onkeydowncapture={onKeydown} />

<div class="overlay" role="presentation" onclick={close}>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="sheet"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label="Share book"
    aria-busy={busy}
    onclick={(e) => e.stopPropagation()}
    {@attach focusTrap}
  >
    <header>
      <div class="head-text">
        <p class="eyebrow">Share</p>
        <h2 class="display" title={book.title}>“{book.title}”</h2>
      </div>
      <button
        class="icon-btn press close"
        aria-label={busy ? "Cancel upload and close" : "Close"}
        onclick={close}
      >
        <Icon icon={X} size={18} />
      </button>
    </header>

    <div class="body">
      <p class="lead">Download the original .epub to this device.</p>
      <a
        class="btn-ghost press download-btn"
        href={downloadUrl}
        download={downloadName}
      >
        <Icon icon={Download} size={16} />
        Download EPUB
      </a>

      <hr class="divider" />

      <p class="lead">
        Or upload the .epub to gofile.io and get a shareable link.
      </p>
      <p class="hint">
        Anonymous upload — anyone with the link can download the file.
      </p>

      <button class="btn press upload-btn" onclick={upload} disabled={busy}>
        <Icon icon={UploadCloud} size={16} />
        {busy ? "Uploading…" : url ? "Upload again" : "Upload to gofile"}
      </button>

      {#if url}
        <div class="result">
          <a href={url} target="_blank" rel="noopener noreferrer">{url}</a>
          <button
            type="button"
            class="icon-btn press copy-btn"
            aria-label={copied ? "Link copied" : "Copy link"}
            title={copied ? "Link copied" : "Copy link"}
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
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .sheet {
    width: min(28rem, 100%);
    max-height: calc(100dvh - var(--sp-12));
    overflow-y: auto;
    background: var(--raised);
    border: 1px solid var(--hairline);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-3);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  header {
    position: sticky;
    top: 0;
    z-index: 1;
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-5) var(--sp-5) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
    background: var(--raised);
  }
  .head-text {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
    min-width: 0;
  }
  .head-text .eyebrow {
    margin: 0;
  }
  h2 {
    margin: 0;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--text-lg);
    font-style: italic;
    font-weight: 520;
    line-height: var(--lh-snug);
  }
  .close {
    flex-shrink: 0;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
    padding: var(--sp-5);
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
    border-radius: var(--radius);
    background: var(--surface);
    border: 1px solid var(--hairline);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  .result a {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--accent-ink);
    font-size: var(--text-xs);
    font-weight: 560;
  }
  .copy-btn {
    width: 1.9rem;
    height: 1.9rem;
  }
  .error {
    margin: 0;
    padding: var(--sp-2) var(--sp-3);
    border-radius: var(--radius);
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
    font-size: var(--text-sm);
    overflow-wrap: anywhere;
  }
  .download-btn,
  .upload-btn {
    align-self: flex-start;
    text-decoration: none;
  }
  .divider {
    align-self: stretch;
    width: 100%;
    margin: var(--sp-2) 0;
    border: none;
    border-top: 1px solid var(--hairline);
    position: relative;
  }
</style>
