/**
 * Svelte attachment that makes an overlay / dialog / slide-over panel keyboard
 * accessible, per the fixing-accessibility skill (priority 3: focus & dialogs):
 *
 *   - moves focus into the node on mount (first focusable, or the node itself),
 *     unless the component already placed focus inside (e.g. a search input),
 *   - traps Tab / Shift+Tab within the node while it is mounted,
 *   - restores focus to the previously-focused element when the node unmounts.
 *
 * It deliberately does NOT handle Escape — each overlay owns its own Esc logic
 * (and the consume-vs-bubble semantics that go with it).
 *
 * Usage:  <div role="dialog" aria-modal="true" {@attach focusTrap}>…</div>
 */
const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export function focusTrap(node: HTMLElement): () => void {
  const previouslyFocused = document.activeElement as HTMLElement | null;

  function focusables(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.getClientRects().length > 0,
    );
  }

  // Move focus inside on mount — but only if the component hasn't already done
  // so (CommandPalette focuses its input, SearchPanel focuses its query field).
  queueMicrotask(() => {
    if (!node.contains(document.activeElement)) {
      (focusables()[0] ?? node).focus();
    }
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Tab") return;
    const items = focusables();
    if (items.length === 0) {
      // Nothing focusable inside: keep focus on the container itself.
      e.preventDefault();
      node.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey) {
      if (active === first || !node.contains(active)) {
        e.preventDefault();
        last.focus();
      }
    } else if (active === last || !node.contains(active)) {
      e.preventDefault();
      first.focus();
    }
  }

  node.addEventListener("keydown", onKeydown);

  return () => {
    node.removeEventListener("keydown", onKeydown);
    // Return focus to whatever triggered the overlay, if it's still around.
    previouslyFocused?.focus?.();
  };
}
