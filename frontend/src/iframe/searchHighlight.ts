// In-frame search-match highlighting.
//
// Extracted from frame.ts as the first stateful module of the #3 frame split.
// Builds a flattened, whitespace-collapsed character index of the chapter body
// so a (charOffset, matchLen) pair from the parent search can be mapped back to
// a DOM Range, wraps that range in a <mark>, and scrolls or pages it into view.
// Falls back to a direct case-insensitive substring search when the indexed
// mapping fails.
//
// This module owns no frame state of its own (only the <mark> nodes live in the
// document). Cross-cutting frame concerns — the content element, paged mode and
// pagination — are injected via SearchHighlightDeps so the module stays
// decoupled from frame.ts's module-level variables.

export interface SearchHighlightDeps {
  /** The live chapter content element (#content). */
  getContentEl: () => HTMLElement;
  /** Whether a chapter is currently loaded and ready to highlight. */
  isContentReady: () => boolean;
  /** Whether the reader is in a paged (vs scroll) layout. */
  isPagedMode: () => boolean;
  /** Navigate to the page containing the given element (paged mode). */
  goToPageInternal: (page: number, animated: boolean) => void;
  /** The page index that contains the given element (paged mode). */
  getElementPageIndex: (el: Element) => number;
}

export interface SearchHighlighter {
  highlightSearchMatch: (
    charOffset: number,
    matchLen: number,
    query: string,
  ) => void;
  clearSearchHighlights: () => void;
}

function prefersReducedMotion(): boolean {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

const TEXT_BOUNDARY_TAGS = new Set([
  "ADDRESS",
  "ARTICLE",
  "ASIDE",
  "BLOCKQUOTE",
  "CAPTION",
  "DD",
  "DIV",
  "DL",
  "DT",
  "FIGCAPTION",
  "FIGURE",
  "FOOTER",
  "FORM",
  "H1",
  "H2",
  "H3",
  "H4",
  "H5",
  "H6",
  "HEADER",
  "HR",
  "LI",
  "MAIN",
  "NAV",
  "OL",
  "P",
  "PRE",
  "SECTION",
  "TABLE",
  "TBODY",
  "TD",
  "TFOOT",
  "TH",
  "THEAD",
  "TR",
  "UL",
]);

interface DOMPoint {
  node: Text;
  offset: number;
}

interface SearchTextIndex {
  positions: Array<DOMPoint | null>;
  length: number;
}

function isSpaceLike(char: string): boolean {
  return /\s/u.test(char);
}

function isTextBoundaryElement(node: Element): boolean {
  return (
    TEXT_BOUNDARY_TAGS.has(node.tagName) ||
    node.tagName === "SVG" ||
    node.tagName === "MATH"
  );
}

function buildSearchTextIndex(root: HTMLElement): SearchTextIndex {
  const positions: Array<DOMPoint | null> = [];
  let length = 0;
  let pendingSpace = false;
  let pendingSpaceStart: DOMPoint | null = null;
  let pendingSpaceEnd: DOMPoint | null = null;
  let lastPoint: DOMPoint | null = null;

  function queueBoundary(): void {
    if (length === 0) return;
    pendingSpace = true;
    if (!pendingSpaceStart) pendingSpaceStart = lastPoint;
    if (!pendingSpaceEnd) pendingSpaceEnd = lastPoint;
  }

  function emitPendingSpace(nextPoint: DOMPoint): void {
    if (!pendingSpace || length === 0) return;
    const start = pendingSpaceStart ?? lastPoint ?? nextPoint;
    const end = pendingSpaceEnd ?? nextPoint;
    positions[length] = start;
    length += 1;
    positions[length] = end;
    lastPoint = end;
    pendingSpace = false;
    pendingSpaceStart = null;
    pendingSpaceEnd = null;
  }

  function emitTextNode(node: Text): void {
    let utf16Offset = 0;

    for (const char of node.data) {
      const startOffset = utf16Offset;
      utf16Offset += char.length;
      const endOffset = utf16Offset;

      const startPoint: DOMPoint = { node, offset: startOffset };
      const endPoint: DOMPoint = { node, offset: endOffset };

      if (isSpaceLike(char)) {
        if (length > 0) {
          pendingSpace = true;
          pendingSpaceStart = pendingSpaceStart ?? lastPoint ?? startPoint;
          pendingSpaceEnd = endPoint;
        }
        continue;
      }

      emitPendingSpace(startPoint);
      positions[length] = startPoint;
      length += 1;
      positions[length] = endPoint;
      lastPoint = endPoint;
      pendingSpaceEnd = endPoint;
    }
  }

  function walk(node: Node): void {
    if (node.nodeType === Node.TEXT_NODE) {
      emitTextNode(node as Text);
      return;
    }

    if (node.nodeType !== Node.ELEMENT_NODE) return;

    const element = node as Element;
    const tag = element.tagName;

    if (tag === "HEAD" || tag === "SCRIPT" || tag === "STYLE") return;
    if (tag === "BR") {
      queueBoundary();
      return;
    }

    const boundary = isTextBoundaryElement(element);
    if (boundary) queueBoundary();

    for (let child = node.firstChild; child; child = child.nextSibling) {
      walk(child);
    }

    if (boundary) queueBoundary();
  }

  walk(root);
  return { positions, length };
}

function wrapRangeInMark(range: Range): HTMLElement | null {
  const mark = document.createElement("mark");
  mark.className = "search-highlight search-highlight-active";

  try {
    range.surroundContents(mark);
    return mark;
  } catch {
    try {
      const contents = range.extractContents();
      mark.appendChild(contents);
      range.insertNode(mark);
      return mark;
    } catch {
      return null;
    }
  }
}

export function createSearchHighlight(
  deps: SearchHighlightDeps,
): SearchHighlighter {
  function clearSearchHighlights(): void {
    for (const mark of document.querySelectorAll("mark.search-highlight")) {
      const parent = mark.parentNode;
      mark.replaceWith(...Array.from(mark.childNodes));
      parent?.normalize();
    }
  }

  function fallbackHighlight(query: string): void {
    const content = deps.getContentEl();
    if (!content) return;

    const lowerQuery = query.trim().toLowerCase();
    if (!lowerQuery) return;

    const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT, {
      acceptNode(node) {
        const parent = node.parentElement;
        if (!parent || parent.closest("head,script,style")) {
          return NodeFilter.FILTER_REJECT;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    });
    let node = walker.nextNode() as Text | null;

    while (node) {
      const index = node.data.toLowerCase().indexOf(lowerQuery);
      if (index !== -1 && index + lowerQuery.length <= node.length) {
        try {
          const range = document.createRange();
          range.setStart(node, index);
          range.setEnd(node, index + lowerQuery.length);
          const mark = wrapRangeInMark(range);
          if (mark) {
            if (deps.isPagedMode()) {
              deps.goToPageInternal(deps.getElementPageIndex(mark), true);
            } else {
              mark.scrollIntoView({
                behavior: prefersReducedMotion() ? "auto" : "smooth",
                block: "center",
              });
            }
          }
        } catch {
          // Skip nodes that cannot be safely surrounded.
        }
        return;
      }
      node = walker.nextNode() as Text | null;
    }
  }

  function highlightSearchMatch(
    charOffset: number,
    matchLen: number,
    query: string,
  ): void {
    clearSearchHighlights();
    const content = deps.getContentEl();
    if (!deps.isContentReady() || !content || matchLen <= 0 || charOffset < 0)
      return;

    const matchEnd = charOffset + matchLen;
    const index = buildSearchTextIndex(content);
    if (matchEnd > index.length) {
      if (query) fallbackHighlight(query);
      return;
    }

    const start = index.positions[charOffset];
    const end = index.positions[matchEnd];
    if (!start || !end) {
      if (query) fallbackHighlight(query);
      return;
    }

    try {
      const range = document.createRange();
      range.setStart(start.node, start.offset);
      range.setEnd(end.node, end.offset);

      if (range.collapsed) {
        if (query) fallbackHighlight(query);
        return;
      }

      const mark = wrapRangeInMark(range);
      if (!mark) {
        if (query) fallbackHighlight(query);
        return;
      }

      if (deps.isPagedMode()) {
        deps.goToPageInternal(deps.getElementPageIndex(mark), true);
      } else {
        mark.scrollIntoView({
          behavior: prefersReducedMotion() ? "auto" : "smooth",
          block: "center",
        });
      }
    } catch {
      if (query) fallbackHighlight(query);
    }
  }

  return { highlightSearchMatch, clearSearchHighlights };
}
