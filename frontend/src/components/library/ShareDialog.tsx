// Share dialog: direct .epub download, or anonymous gofile upload for a
// shareable link. Ported from ShareDialog.svelte.
//
// Solid 2.0 notes:
//   - onDestroy -> onCleanup; <svelte:window onkeydowncapture> -> an
//     onSettled-scoped capture listener (the Svelte original already used
//     capture for exactly the registration-order reason documented below).
//   - {@attach focusTrap} -> ref={trap()} (two-phase factory — beta.29 ref callbacks are unowned, so the old ref + onCleanup(...) form never tore the trap down); bind:this is not
//     needed -- the abort controller and timers are plain (non-reactive) vars.
//   - The backdrop dismiss is the shared .backdrop-dismiss button instead of
//     the sheet stopPropagation trick, which jsx-a11y rejects.
import { createMemo, createSignal, onCleanup, onSettled, Show } from "solid-js";
import { getDownloadUrl, uploadToGofile, type BookMeta } from "~/api/client";
import { getErrorMessage } from "~/lib/errors";
import { toast } from "~/lib/toast";
import { trap } from "~/lib/focusTrap";
import Icon from "~/lib/Icon";
import { Check, Copy, Download, UploadCloud, X } from "~/lib/icons";

interface Props {
  book: BookMeta;
  onclose: () => void;
}

export default function ShareDialog(props: Props) {
  // Direct local download: a same-origin <a download> hitting the file
  // endpoint streams the .epub with Content-Disposition: attachment, so the
  // browser saves it without any JS. The download attribute is just a
  // filename hint; the server's Content-Disposition is authoritative.
  const downloadUrl = createMemo(() => getDownloadUrl(props.book.id));
  const downloadName = createMemo(() => `${props.book.title || "book"}.epub`);

  const [busy, setBusy] = createSignal(false);
  const [url, setUrl] = createSignal<string | null>(null);
  const [error, setError] = createSignal<string | null>(null);
  const [copied, setCopied] = createSignal(false);
  let uploadController: AbortController | null = null;
  let copiedResetTimer: ReturnType<typeof setTimeout> | null = null;

  async function upload(): Promise<void> {
    if (busy()) return;
    const controller = new AbortController();
    uploadController = controller;
    setBusy(true);
    setError(null);
    try {
      const { downloadPage } = await uploadToGofile(
        props.book.id,
        controller.signal,
      );
      setUrl(downloadPage);
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      setError(getErrorMessage(err, "Upload to gofile failed."));
    } finally {
      if (uploadController === controller) {
        uploadController = null;
        setBusy(false);
      }
    }
  }

  async function copyLink(): Promise<void> {
    const link = url();
    if (!link) return;
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      toast.show("Link copied");
      if (copiedResetTimer !== null) clearTimeout(copiedResetTimer);
      copiedResetTimer = setTimeout(() => {
        setCopied(false);
        copiedResetTimer = null;
      }, 1500);
    } catch {
      toast.show("Could not copy link");
    }
  }

  function close(): void {
    uploadController?.abort();
    props.onclose();
  }

  onCleanup(() => {
    uploadController?.abort();
    if (copiedResetTimer !== null) clearTimeout(copiedResetTimer);
  });

  // Focus the download action, the control this dialog exists for. A ref
  // cannot do it: refs run while the node is still detached (b28 probe), so
  // a self-focusing ref would be a silent no-op and focusTrap's fallback
  // would take the first focusable in the sheet -- the header close button,
  // where Enter dismisses (the b30 EditBookDialog defect). Deferring one
  // microtask lands after the trap's own queueMicrotask; if this runs first
  // instead, the trap's !node.contains(activeElement) guard stands down.
  let downloadEl: HTMLAnchorElement | undefined;
  onSettled(() => {
    queueMicrotask(() => downloadEl?.focus());
  });

  function onKeydown(e: KeyboardEvent): void {
    // An IME uses Escape to abandon a composition, and capture at window beats
    // the focused control, so without this guard the dialog closed
    // mid-composition (EditBookDialog, BookmarksPanel, SearchPanel and
    // TocPanel guard the same way).
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on
      // it.
      e.stopImmediatePropagation();
      close();
    }
  }

  // Capture phase: the dialog mounts after the page's own window key
  // listeners, so a bubble listener here runs last and can't pre-empt them;
  // capture runs first regardless of registration order. Attached only while
  // the dialog is mounted.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    return () => window.removeEventListener("keydown", onKeydown, true);
  });

  return (
    <div class="sd-overlay" role="presentation">
      <button
        type="button"
        class="backdrop-dismiss"
        aria-label="Close"
        tabindex="-1"
        onClick={close}
      />
      {/* eslint-disable jsx-a11y/prefer-tag-over-role -- div+role kept over a native <dialog>: visual parity with the Svelte original is the port's contract. */}
      <div
        class="sd-sheet"
        role="dialog"
        tabindex="-1"
        aria-modal="true"
        aria-label="Share book"
        aria-busy={busy() ? "true" : "false"}
        ref={trap()}
      >
        <header>
          <div class="sd-head-text">
            <p class="eyebrow">Share</p>
            <h2 class="display" title={props.book.title}>
              “{props.book.title}”
            </h2>
          </div>
          <button
            class="icon-btn press sd-close"
            aria-label={busy() ? "Cancel upload and close" : "Close"}
            onClick={close}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>
        </header>

        <div class="sd-body">
          <p class="sd-lead">Download the original .epub to this device.</p>
          <a
            class="btn-ghost press sd-download-btn"
            href={downloadUrl()}
            download={downloadName()}
            ref={(el) => (downloadEl = el)}
          >
            <Icon icon={Download} size={16} decorative />
            Download EPUB
          </a>

          <hr class="sd-divider" />

          <p class="sd-lead">
            Or upload the .epub to gofile.io and get a shareable link.
          </p>
          <p class="sd-hint">
            Anonymous upload — anyone with the link can download the file.
          </p>

          {/* aria-disabled, not disabled: an upload can run for the whole
              gofile timeout (30 minutes), and a real disabled attribute blurs
              the button the moment it is pressed, dropping the keyboard user
              out of the dialog for the duration. upload() entry-guards the
              busy state, so the attribute only has to say so. */}
          <button
            class="btn press sd-upload-btn"
            onClick={() => void upload()}
            aria-disabled={busy() ? "true" : "false"}
          >
            <Icon icon={UploadCloud} size={16} decorative />
            {busy()
              ? "Uploading…"
              : url()
                ? "Upload again"
                : "Upload to gofile"}
          </button>

          <Show when={url()}>
            {(link) => (
              <div class="sd-result">
                <a href={link()} target="_blank" rel="noopener noreferrer">
                  {link()}
                </a>
                <button
                  type="button"
                  class="icon-btn press sd-copy-btn"
                  aria-label={copied() ? "Link copied" : "Copy link"}
                  title={copied() ? "Link copied" : "Copy link"}
                  onClick={() => void copyLink()}
                >
                  <Icon
                    icon={copied() ? Check : Copy}
                    size={15}
                    labelFromParent
                  />
                </button>
              </div>
            )}
          </Show>
          <Show when={error()}>
            {(message) => <p class="sd-error">{message()}</p>}
          </Show>

          {/* Pre-mounted live region, same as EditBookDialog/ProfileDialog:
              the visible error above is inserted in the same tick as its
              text, which NVDA and JAWS do not announce (b27, WCAG 4.1.3) --
              this region exists from first paint and only its text changes,
              so the paragraph carries no role="alert". */}
          <p class="sr-only" role="alert">
            {error() ?? ""}
          </p>
        </div>
      </div>
      {/* eslint-enable jsx-a11y/prefer-tag-over-role */}
    </div>
  );
}
