// "About Sayumi": the one place the app names itself, its stack and the
// licences it carries. Toggled from anywhere via the ui store's `about` flag.
//
// Structurally a sibling of ShortcutsHelp, and for the same reasons -- read
// that file for the long form:
//   - ref={trap()} is the single owner of focus-on-mount and of the restore to
//     whatever opened the dialog (here, the profile menu item, which hands
//     focus back to its trigger before running the action).
//   - The Escape listener attaches only while open, in the CAPTURE phase, so
//     this dialog consumes the key before App's and Read's window handlers and
//     one Esc never both closes the dialog and navigates the reader back.
//   - close() is ui.closeOverlays(): the three global overlays are mutually
//     exclusive in the store, so closing all of them is closing this one.
//
// No Portal here, unlike CustomThemeDialog: this mounts at the App root, which
// has no filtered ancestor to blur it or to capture its fixed positioning.
//
// The build stamp is read from GET /api/version on every open rather than once
// at mount: restarting the server onto a newer binary under a tab that never
// reloads is the normal upgrade path, and this sheet is where someone checks
// which build they are actually talking to.
import { createEffect, createSignal, Show } from "solid-js";
import { getVersion, type VersionInfo } from "~/api/client";
import Icon from "~/lib/Icon";
import { X } from "~/lib/icons";
import { trap } from "~/lib/focusTrap";
import { ui } from "~/lib/ui";

// build.sh and release.sh stamp an RFC 3339 instant; the sheet shows the
// calendar day, in UTC to match the stamp. A binary built outside those scripts
// reports "unknown", which is better omitted than printed as a fact.
function buildStamp(raw: string): string {
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return "";
  return parsed.toISOString().slice(0, 10);
}

function close(): void {
  ui.closeOverlays();
}

function onKeydown(e: KeyboardEvent): void {
  // Window-capture: do not consume the Escape that ends an IME composition
  // somewhere else in the document.
  if (e.key !== "Escape" || e.isComposing) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  close();
}

export default function AboutDialog() {
  const [build, setBuild] = createSignal<VersionInfo | null>(null);

  // One effect for both jobs the open state owns: the Escape listener and the
  // version read, torn down together.
  createEffect(
    () => ui.about,
    (open) => {
      if (!open) return undefined;
      window.addEventListener("keydown", onKeydown, true);
      const pending = new AbortController();
      void getVersion(pending.signal)
        .then((info) => setBuild(info))
        // A failed or aborted read is not worth a toast: the sheet keeps its
        // previous answer, or shows no build line at all.
        .catch(() => undefined);
      return () => {
        window.removeEventListener("keydown", onKeydown, true);
        pending.abort();
      };
    },
  );

  return (
    <Show when={ui.about}>
      <div class="about-overlay" role="presentation">
        {/* Pointer-only backdrop dismiss via a real but untabbable button:
            Esc and the Close buttons cover the keyboard. */}
        <button
          type="button"
          class="backdrop-dismiss"
          aria-label="Close"
          tabindex="-1"
          onClick={close}
        />
        {/* eslint-disable jsx-a11y/prefer-tag-over-role -- div+role kept over a native <dialog>: visual parity with the established design is the port's contract. */}
        <div
          class="about-sheet paper"
          role="dialog"
          tabindex="-1"
          aria-modal="true"
          aria-label="About Sayumi"
          ref={trap()}
        >
          <button
            class="icon-btn press about-close"
            aria-label="Close"
            onClick={close}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>

          <p class="eyebrow">About</p>
          <h2 class="display about-title">
            <span class="wordmark">Sayumi</span>
            <span class="about-mark" aria-hidden="true">
              ❦
            </span>
          </h2>
          <p class="about-tagline">
            A self-hosted EPUB library, and a reader built for long sessions.
          </p>
          <Show when={build()}>
            {(info) => (
              <p class="about-build tnum">
                <span>{info().version}</span>
                <Show when={buildStamp(info().buildDate)}>
                  {(stamp) => (
                    <span class="about-build-date">built {stamp()}</span>
                  )}
                </Show>
              </p>
            )}
          </Show>

          <dl class="about-facts">
            <div class="about-fact">
              <dt class="eyebrow">Server</dt>
              <dd>
                One Go binary. The library, the reader and the API ship inside
                it, with no external services to sign up for.
              </dd>
            </div>
            <div class="about-fact">
              <dt class="eyebrow">Reader</dt>
              <dd>
                Solid and plain CSS. Every book renders in a sandboxed frame, so
                a publisher's stylesheet can never reach the app around it.
              </dd>
            </div>
            <div class="about-fact">
              <dt class="eyebrow">Your library</dt>
              <dd>
                Books, reading progress, bookmarks and themes stay in your own
                library folder and database.
              </dd>
            </div>
          </dl>

          <p class="about-credit">
            Icons are Lucide geometry, used under the ISC licence. Reading fonts
            are served by the binary itself, never from a third party.
          </p>

          <div class="about-actions">
            <button
              type="button"
              class="btn-ghost press"
              onClick={() => ui.openShortcuts()}
            >
              Keyboard shortcuts
            </button>
            <button type="button" class="btn press" onClick={close}>
              Close
            </button>
          </div>
        </div>
        {/* eslint-enable jsx-a11y/prefer-tag-over-role */}
      </div>
    </Show>
  );
}
