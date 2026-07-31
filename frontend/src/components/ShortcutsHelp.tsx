// Modal keyboard-shortcut reference, toggled from anywhere via the ui store's
// `shortcuts` flag. Ported from ShortcutsHelp.svelte.
//
// Solid 2.0 notes:
//   - {@attach focusTrap} -> ref + onCleanup(focusTrap(el)), registered with
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
import { createEffect, For, onCleanup, Show } from "solid-js";
import Icon from "~/lib/Icon";
import { X } from "~/lib/icons";
import { focusTrap } from "~/lib/focusTrap";
import { ui } from "~/lib/ui";

const groups: { title: string; items: { keys: string[]; desc: string }[] }[] = [
  {
    title: "Global",
    items: [
      { keys: ["Ctrl / ⌘", "K"], desc: "Open command palette" },
      { keys: ["?"], desc: "Show this help" },
      { keys: ["Esc"], desc: "Close overlay / panel" },
    ],
  },
  {
    title: "Reader",
    items: [
      { keys: ["←"], desc: "Navigate left" },
      { keys: ["→"], desc: "Navigate right" },
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
          ref={(el) => onCleanup(focusTrap(el))}
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
              ref={(el) => el.focus()}
            >
              <Icon icon={X} size={18} />
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
                          <span class="shortcuts-leader" aria-hidden="true" />
                          <dd>{it.desc}</dd>
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
