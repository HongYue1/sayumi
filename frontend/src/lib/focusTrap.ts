import { onCleanup } from "solid-js";
import { lockDocumentScroll } from "~/lib/scrollLock";

/**
 * Focus trap for overlay / dialog / slide-over panels:
 *
 *   - moves focus into the node on mount (first tab stop, or the node itself),
 *     unless the component already placed focus inside (e.g. a search input),
 *     retrying once real tab stops arrive if the fallback parked focus on the
 *     container,
 *   - traps Tab / Shift+Tab within the node while it is mounted, counting only
 *     elements that are genuinely in the tab order,
 *   - restores focus to the previously-focused element when the node unmounts,
 *     but only while the node still owns focus,
 *   - holds the shared, reference-counted document scroll lock for its lifetime.
 *
 * It deliberately does NOT handle Escape — each overlay owns its own Esc logic
 * (and the consume-vs-bubble semantics that go with it).
 *
 * Usage:  <div role="dialog" aria-modal="true" ref={trap()}>…</div>
 *
 * Solid 2.0 ownership (probe-verified, beta.29): an arrow ref callback runs
 * UNOWNED — onCleanup inside ref={(el) => onCleanup(focusTrap(el))} never
 * registered, so the trap never tore down: the closed panel stayed topmost in
 * ACTIVE_TRAPS and swallowed Tab app-wide, and focus was never restored.
 * trap() is the two-phase factory: the outer call runs in the component's
 * owned body (onCleanup registers there), the returned function is the ref
 * callback. Call trap() once per element — it captures teardown state.
 */
const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "summary",
  "iframe",
  "audio[controls]",
  "video[controls]",
  "[contenteditable]",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

const ACTIVE_TRAPS = new WeakMap<Document, HTMLElement[]>();

export function focusTrap(node: HTMLElement): () => void {
  const doc = node.ownerDocument;
  const previouslyFocused = doc.activeElement as HTMLElement | null;
  const unlockScroll = lockDocumentScroll(doc);
  const traps = ACTIVE_TRAPS.get(doc) ?? [];
  if (traps.length === 0) ACTIVE_TRAPS.set(doc, traps);
  traps.push(node);

  let mounted = true;
  let addedTabIndex = false;
  let observer: MutationObserver | undefined;

  // tabIndex, not the selector, decides membership. `button:not([disabled])`
  // also matches the tabindex="-1" rows of a roving-tabindex widget — TocPanel's
  // virtualized list and SearchPanel's results both sit inside a trap — and
  // counting those put first/last on elements the browser never tabs to, so the
  // wrap either let Tab walk out of the dialog or drove focus onto a row that
  // could not hand it back.
  function focusables(): HTMLElement[] {
    return Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => isInTabOrder(el) && el.getClientRects().length > 0,
    );
  }

  function isInTabOrder(element: HTMLElement): boolean {
    // An explicit negative tabindex always wins, including on native controls.
    if (element.hasAttribute("tabindex")) return element.tabIndex >= 0;
    if (element.tabIndex >= 0) return true;

    // Chromium exposes sequentially focusable editing hosts with a reflected
    // tabIndex of -1. happy-dom does the same for <summary>; model those
    // platform tab stops explicitly. Invalid and false contenteditable values
    // are not editing hosts and must not become accidental wrap points.
    const editable = element.getAttribute("contenteditable")?.toLowerCase();
    return (
      element.localName === "summary" ||
      editable === "" ||
      editable === "true" ||
      editable === "plaintext-only"
    );
  }

  // node.contains(node) is true, so a container-focused dialog read as "focus is
  // already inside the ring" and both Tab branches stood down.
  function inside(el: Element | null): boolean {
    return el !== null && el !== node && node.contains(el);
  }

  function focusContainer(): void {
    // A dialog without loaded controls still needs a reliable focus target.
    // Keep the fallback programmatic-only and restore the original markup.
    if (!node.hasAttribute("tabindex")) {
      node.tabIndex = -1;
      addedTabIndex = true;
    }
    node.focus();
    // The reader panels are clientOnly with a one-microtask fallback
    // (Read.tsx), so first open reaches here with nothing to focus and, with no
    // second attempt, focus stayed parked on the container for the life of the
    // panel. Watch for the real controls rather than guessing at a delay.
    if (doc.activeElement === node) watchForTabStops();
  }

  // Only ever started by the fallback above, and it stands down the moment
  // focus is anywhere but the container, so a dialog that places focus itself
  // (EditBookDialog, ProfileDialog, ShareDialog, CustomThemeDialog and
  // CommandPalette each focus the control they exist for) is never overridden.
  function watchForTabStops(): void {
    if (observer !== undefined || typeof MutationObserver === "undefined") {
      return;
    }
    observer = new MutationObserver(() => {
      if (!mounted || doc.activeElement !== node) {
        stopWatching();
        return;
      }
      const first = focusables()[0];
      if (first === undefined) return;
      stopWatching();
      first.focus();
    });
    observer.observe(node, { childList: true, subtree: true });
  }

  function stopWatching(): void {
    observer?.disconnect();
    observer = undefined;
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
      if (active === first || !inside(active)) {
        e.preventDefault();
        last.focus();
      }
    } else if (active === last || !inside(active)) {
      e.preventDefault();
      first.focus();
    }
  }

  // Capture at the document so a Tab that starts outside can be recovered after
  // a focused control is removed or disabled. Only the topmost nested trap acts.
  doc.addEventListener("keydown", onKeydown, true);

  return () => {
    mounted = false;
    stopWatching();
    doc.removeEventListener("keydown", onKeydown, true);
    const index = traps.lastIndexOf(node);
    if (index !== -1) traps.splice(index, 1);
    if (traps.length === 0) ACTIVE_TRAPS.delete(doc);
    if (addedTabIndex && node.getAttribute("tabindex") === "-1") {
      node.removeAttribute("tabindex");
    }
    // Return focus to whatever triggered the overlay, if it's still around --
    // but only while this dialog still owns focus. Something that deliberately
    // took focus before teardown keeps it, and this is also what makes a second
    // dispose harmless: no separate idempotence flag, which would be a second
    // guard on the same symptom.
    const active = doc.activeElement;
    const ownsFocus =
      active === null || active === doc.body || node.contains(active);
    if (ownsFocus && previouslyFocused?.isConnected) previouslyFocused.focus();
    unlockScroll();
  };
}

/** Two-phase ref factory: registers focusTrap's teardown on the component's
 *  owner (see the header note on beta.29 ref-callback ownership). */
export function trap(): (el: HTMLElement) => void {
  let teardown: (() => void) | undefined;
  onCleanup(() => teardown?.());
  return (el) => {
    teardown?.();
    teardown = focusTrap(el);
  };
}
