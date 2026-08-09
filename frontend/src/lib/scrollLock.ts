interface ScrollLockState {
  owners: number;
  scrollX: number;
  scrollY: number;
  rootOverflow: string;
  bodyOverflow: string;
  bodyPosition: string;
  bodyTop: string;
  bodyLeft: string;
  bodyRight: string;
  bodyWidth: string;
  bodyPaddingRight: string;
}

const DOCUMENT_LOCKS = new WeakMap<Document, ScrollLockState>();

/**
 * Freeze the owning document without losing its scroll position. Locks are
 * reference-counted per Document so a dialog nested inside a reader panel can
 * release independently without waking the background page.
 */
export function lockDocumentScroll(doc: Document): () => void {
  const existing = DOCUMENT_LOCKS.get(doc);
  if (existing !== undefined) {
    existing.owners += 1;
    return releaseOwner(doc, existing);
  }

  const root = doc.documentElement;
  const body = doc.body;
  const view = doc.defaultView;
  if (root === null || body === null) return () => {};

  const state: ScrollLockState = {
    owners: 1,
    scrollX: view?.scrollX ?? 0,
    scrollY: view?.scrollY ?? 0,
    rootOverflow: root.style.overflow,
    bodyOverflow: body.style.overflow,
    bodyPosition: body.style.position,
    bodyTop: body.style.top,
    bodyLeft: body.style.left,
    bodyRight: body.style.right,
    bodyWidth: body.style.width,
    bodyPaddingRight: body.style.paddingRight,
  };
  DOCUMENT_LOCKS.set(doc, state);

  // Keep the viewport width stable when hiding a classic scrollbar. Browsers
  // with overlay scrollbars report a zero gap and need no compensation.
  const viewportGap =
    view !== null && root.clientWidth > 0
      ? Math.max(0, view.innerWidth - root.clientWidth)
      : 0;
  if (viewportGap > 0 && view !== null) {
    const currentPadding = Number.parseFloat(
      view.getComputedStyle(body).paddingRight,
    );
    body.style.paddingRight = `${(Number.isFinite(currentPadding) ? currentPadding : 0) + viewportGap}px`;
  }

  root.style.overflow = "hidden";
  body.style.overflow = "hidden";
  body.style.position = "fixed";
  body.style.top = `${-state.scrollY}px`;
  body.style.left = `${-state.scrollX}px`;
  body.style.right = "0";
  body.style.width = "auto";

  return releaseOwner(doc, state);
}

function releaseOwner(doc: Document, state: ScrollLockState): () => void {
  let released = false;
  return () => {
    if (released) return;
    released = true;

    if (DOCUMENT_LOCKS.get(doc) !== state) return;
    state.owners -= 1;
    if (state.owners > 0) return;
    DOCUMENT_LOCKS.delete(doc);

    const root = doc.documentElement;
    const body = doc.body;
    if (root === null || body === null) return;

    root.style.overflow = state.rootOverflow;
    body.style.overflow = state.bodyOverflow;
    body.style.position = state.bodyPosition;
    body.style.top = state.bodyTop;
    body.style.left = state.bodyLeft;
    body.style.right = state.bodyRight;
    body.style.width = state.bodyWidth;
    body.style.paddingRight = state.bodyPaddingRight;
    doc.defaultView?.scrollTo(state.scrollX, state.scrollY);
  };
}
