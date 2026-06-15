// Simplified EPUB CFI utilities for position tracking.
//
// We encode only element paths within the chapter body using a compact
// custom format: "cfi:N/N/N" where each N is the 1-based index of the
// element among its element siblings at that depth, starting from <body>.
//
// Text offsets are intentionally omitted. We find the target element and
// scroll it into view. Percent-based position is always the reliable
// fallback when CFI resolution fails.
//
// These functions are the canonical reference implementation. The identical
// logic is also inlined into frame.ts (IIFE) since that file cannot import
// modules at runtime.

/**
 * Generates a CFI string for the given element within document.body.
 * Returns null if the element is not inside body or the path cannot be built.
 */
export function generateCFI(el: Element, doc: Document): string | null {
  const body = doc.body;
  if (!body || !body.contains(el) || el === body) return null;

  const path: number[] = [];
  let current: Element | null = el;

  while (current && current !== body) {
    const parent: Element | null = current.parentElement;
    if (!parent) return null;

    let index = 0;
    for (let i = 0; i < parent.children.length; i++) {
      if (parent.children[i] === current) {
        index = i + 1;
        break;
      }
    }
    if (index === 0) return null;

    path.unshift(index);
    current = parent === body ? null : parent;
  }

  if (path.length === 0) return null;
  return "cfi:" + path.join("/");
}

/**
 * Resolves a CFI string back to an element within document.body.
 * Returns null if the CFI is malformed or the element no longer exists.
 */
export function resolveCFI(cfi: string, doc: Document): Element | null {
  if (!cfi.startsWith("cfi:")) return null;
  const body = doc.body;
  if (!body) return null;

  const parts = cfi.slice(4).split("/");
  let current: Element = body;

  for (const part of parts) {
    // Strict integer parse: a malformed/foreign segment (e.g. "3x", "", "1.5")
    // must fail to null so callers fall back to percent, rather than parseInt
    // leniently coercing it to a wrong-but-valid index. Matches the inlined
    // resolveCFILocal in frame.ts.
    if (!/^\d+$/.test(part)) return null;
    const index = parseInt(part, 10);
    if (index < 1) return null;
    const child = current.children[index - 1];
    if (!child) return null;
    current = child;
  }

  return current;
}
