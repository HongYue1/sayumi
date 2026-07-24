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

const ACTIVE_TRAPS = new WeakMap<Document, HTMLElement[]>();

export function focusTrap(node: HTMLElement): () => void {
  const doc = node.ownerDocument;
  const previouslyFocused = doc.activeElement as HTMLElement | null;
  const traps = ACTIVE_TRAPS.get(doc) ?? [];
  if (traps.length === 0) ACTIVE_TRAPS.set(doc, traps);
  traps.push(node);

  let mounted = true;
  let addedTabIndex = false;

  function focusables(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.getClientRects().length > 0,
    );
  }

  function focusContainer(): void {
    // A dialog without loaded controls still needs a reliable focus target.
    // Keep the fallback programmatic-only and restore the original markup.
    if (!node.hasAttribute("tabindex")) {
      node.tabIndex = -1;
      addedTabIndex = true;
    }
    node.focus();
  }

  // Move focus inside on mount — but only if the component hasn't already done
  // so (CommandPalette focuses its input, SearchPanel focuses its query field).
  queueMicrotask(() => {
    if (!mounted || traps[traps.length - 1] !== node) return;
    if (!node.contains(doc.activeElement)) {
      const first = focusables()[0];
      if (first) first.focus();
      else focusContainer();
    }
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Tab" || traps[traps.length - 1] !== node) return;
    const items = focusables();
    if (items.length === 0) {
      // Nothing focusable inside: keep focus on the container itself.
      e.preventDefault();
      focusContainer();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = doc.activeElement as HTMLElement | null;
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

  // Capture at the document so a Tab that starts outside can be recovered after
  // a focused control is removed or disabled. Only the topmost nested trap acts.
  doc.addEventListener("keydown", onKeydown, true);

  return () => {
    mounted = false;
    doc.removeEventListener("keydown", onKeydown, true);
    const index = traps.lastIndexOf(node);
    if (index !== -1) traps.splice(index, 1);
    if (traps.length === 0) ACTIVE_TRAPS.delete(doc);
    if (addedTabIndex && node.getAttribute("tabindex") === "-1") {
      node.removeAttribute("tabindex");
    }
    // Return focus to whatever triggered the overlay, if it's still around.
    if (previouslyFocused?.isConnected) previouslyFocused.focus();
  };
}
