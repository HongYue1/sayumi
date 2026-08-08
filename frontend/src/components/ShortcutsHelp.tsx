// Modal keyboard-shortcut reference, toggled from anywhere via the ui store's
// `shortcuts` flag. Ported from ShortcutsHelp.svelte.
//
// Solid 2.0 notes:
//   - {@attach focusTrap} -> ref={trap()} (two-phase factory — beta.29 ref callbacks are unowned, so the old ref + onCleanup(...) form never tore the trap down), registered with
//     the Show branch's owner so the trap's cleanup runs when the sheet
//     unmounts (restoring focus to whatever opened it).
//   - The conditional <svelte:window onkeydown> becomes a compute/apply
//     createEffect that attaches the listener only while the sheet is open,
//     in the CAPTURE phase -- deliberately stronger than the Svelte original,
//     whose conditionally attached bubble listener registered after App's and
//     Read's, so its stopImmediatePropagation could not beat them. Capture
//     runs before any window bubble listener: the modal consumes Esc first,
//     always.
//   - close() is ui.closeOverlays(): palette and shortcuts are mutually
//     exclusive in the ui store, so closing both is exactly the old
//     `ui.shortcuts = false`.
//   - Probed in-sandbox (b28): the capture listener does beat both window
//     bubble handlers that would otherwise act on Esc -- App's global shortcut
//     listener and Read's reader keys -- so this sheet always consumes Esc
//     first, and one Esc never both closes the sheet and pages the reader.
//   - Focus is owned entirely by trap() on the sheet element below. The close
//     button used to carry a self-focusing ref, but a probe showed that such a
//     ref fires while the button is still DETACHED (connected=false), where
//     focus() is a no-op: focusTrap's queued microtask was doing all the work,
//     picking the first focusable, which is that same button. The dead ref was
//     removed rather than repaired -- trap() is the single owner of
//     focus-on-mount across every dialog here, and it also captures the true
//     opener, so closing restores focus to whatever opened the sheet. Keep
//     trap() on this element; the suite pins all of it.
import { createEffect, For, Show } from "solid-js";
import Icon from "~/lib/Icon";
import { X } from "~/lib/icons";
import { trap } from "~/lib/focusTrap";
import { ui } from "~/lib/ui";

const groups: { title: string; items: { keys: string[]; desc: string }[] }[] = [
  {
    title: "Global",
    items: [
      { keys: ["Ctrl / ⌘", "K"], desc: "Open command palette" },
      { keys: ["?"], desc: "Show this help" },
      {
        keys: ["Esc"],
        desc: "Close overlay / panel; back to library in the reader",
      },
    ],
  },
  {
    title: "Reader",
    items: [
      { keys: ["←"], desc: "Navigate left" },
      { keys: ["→"], desc: "Navigate right" },
      // Paged mode binds six more keys in Read.tsx's handleKeyAction, and
      // frame.ts suppresses their native scroll there, so the parent handler is
      // the only thing that can act on them. They were undocumented until b28.
      { keys: ["Space"], desc: "Page forward" },
      { keys: ["Shift", "Space"], desc: "Page back" },
      { keys: ["PageDown"], desc: "Page forward" },
      { keys: ["PageUp"], desc: "Page back" },
      { keys: ["End"], desc: "Page forward" },
      { keys: ["Home"], desc: "Page back" },
      { keys: ["T"], desc: "Table of contents" },
      { keys: ["S"], desc: "Settings" },
      { keys: ["F"], desc: "Search in book" },
      { keys: ["B"], desc: "Toggle bookmark" },
      { keys: ["Shift", "B"], desc: "Bookmarks panel" },
    ],
  },
];

function close(): void {
  ui.closeOverlays();
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === "Escape") {
    e.preventDefault();
    // Consume the event so the reader's separate window key handler doesn't
    // also act on this Esc and navigate back to the library.
    e.stopImmediatePropagation();
    close();
  }
}

export default function ShortcutsHelp() {
  createEffect(
    () => ui.shortcuts,
    (open) => {
      if (!open) return undefined;
      window.addEventListener("keydown", onKeydown, true);
      return () => window.removeEventListener("keydown", onKeydown, true);
    },
  );

  return (
    <Show when={ui.shortcuts}>
      <div class="shortcuts-overlay" role="presentation">
        {/* Backdrop dismiss is pointer-only by design, via a real (but
            untabbable) button: Esc and the Close button cover keyboard, and
            the a11y click rules stay satisfied without suppressions. */}
        <button
          type="button"
          class="backdrop-dismiss"
          aria-label="Close"
          tabindex="-1"
          onClick={close}
        />
        {/* eslint-disable jsx-a11y/prefer-tag-over-role -- div+role kept over a native <dialog>: visual parity with the Svelte original is the port's contract. */}
        <div
          class="shortcuts-sheet"
          role="dialog"
          tabindex="-1"
          aria-modal="true"
          aria-label="Keyboard shortcuts"
          ref={trap()}
        >
          <header>
            <div class="shortcuts-head-text">
              <p class="eyebrow">Help</p>
              <h2 class="display">Keyboard shortcuts</h2>
            </div>
            <button
              class="icon-btn press shortcuts-close"
              aria-label="Close"
              onClick={close}
            >
              <Icon icon={X} size={18} labelFromParent />
            </button>
          </header>
          <div class="shortcuts-groups">
            <For each={groups}>
              {(g) => (
                <section>
                  <h3 class="eyebrow">{g.title}</h3>
                  <dl>
                    <For each={g.items}>
                      {(it) => (
                        <div class="shortcuts-row">
                          <dt>
                            <For each={it.keys}>
                              {(k) => <kbd class="kbd">{k}</kbd>}
                            </For>
                          </dt>
                          {/* The leader sits inside <dd>: a div row inside a
                              <dl> may contain only <dt>s followed by <dd>s, so
                              a bare <span> between them broke the structure
                              assistive tech reads off the list. */}
                          <dd>
                            <span class="shortcuts-leader" aria-hidden="true" />
                            {it.desc}
                          </dd>
                        </div>
                      )}
                    </For>
                  </dl>
                </section>
              )}
            </For>
          </div>
        </div>
        {/* eslint-enable jsx-a11y/prefer-tag-over-role */}
      </div>
    </Show>
  );
}
