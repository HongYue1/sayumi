/** Private DOM identity for reader-owned search highlights. */
export const SEARCH_MARK_ATTRIBUTE = "data-search-mark";
export const SEARCH_MARK_VALUE = "sayumi";
export const SEARCH_MARK_SELECTOR = `mark[${SEARCH_MARK_ATTRIBUTE}="${SEARCH_MARK_VALUE}"]`;

/**
 * Book markup is deny-list sanitized and may carry arbitrary data attributes.
 * Strip this reserved attribute once, immediately after chapter HTML is
 * committed, before the reader creates any of its own marks.
 */
export function reserveSearchMarkAttribute(root: ParentNode): void {
  for (const element of root.querySelectorAll(`[${SEARCH_MARK_ATTRIBUTE}]`)) {
    element.removeAttribute(SEARCH_MARK_ATTRIBUTE);
  }
}
