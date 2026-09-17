import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  reserveSearchMarkAttribute,
  SEARCH_MARK_ATTRIBUTE,
  SEARCH_MARK_SELECTOR,
  SEARCH_MARK_VALUE,
} from "~/lib/searchMarks";

describe("search mark ownership", () => {
  it("strips the reserved attribute without changing authored structure", () => {
    const root = document.createElement("div");
    root.innerHTML =
      '<p><mark id="a" data-search-mark="book">authored</mark></p><span data-search-mark></span>';

    reserveSearchMarkAttribute(root);

    expect(root.innerHTML).toBe(
      '<p><mark id="a">authored</mark></p><span></span>',
    );
  });

  it("defines one exact identity for CSS, CFI, and clearing", () => {
    const mark = document.createElement("mark");
    mark.setAttribute(SEARCH_MARK_ATTRIBUTE, SEARCH_MARK_VALUE);
    expect(mark.matches(SEARCH_MARK_SELECTOR)).toBe(true);

    mark.setAttribute(SEARCH_MARK_ATTRIBUTE, "book");
    expect(mark.matches(SEARCH_MARK_SELECTOR)).toBe(false);
  });

  it("keeps the cheap attribute compare equivalent to the selector", () => {
    // cfi.ts asks this of every element sibling at every depth, so it compares
    // localName plus attribute rather than matching the selector per
    // candidate. The two forms must agree on every candidate: a mark that one
    // counts and the other skips shifts every path after it onto a different
    // real element, silently.
    const cheap = (el: Element): boolean =>
      el.localName === "mark" &&
      el.getAttribute(SEARCH_MARK_ATTRIBUTE) === SEARCH_MARK_VALUE;

    const root = document.createElement("div");
    root.innerHTML = [
      `<mark ${SEARCH_MARK_ATTRIBUTE}="${SEARCH_MARK_VALUE}">ours</mark>`,
      `<mark ${SEARCH_MARK_ATTRIBUTE}="book">authored lookalike</mark>`,
      `<mark class="search-highlight">authored</mark>`,
      `<span ${SEARCH_MARK_ATTRIBUTE}="${SEARCH_MARK_VALUE}">text</span>`,
      `<p>plain</p>`,
    ].join("");

    const candidates = Array.from(root.children);
    expect(candidates.map(cheap)).toEqual([true, false, false, false, false]);
    for (const el of candidates) {
      expect(cheap(el)).toBe(el.matches(SEARCH_MARK_SELECTOR));
    }
  });

  it("keeps frame CSS on the same exact private selector", () => {
    const css = readFileSync(
      join(process.cwd(), "src/iframe/frame.css"),
      "utf8",
    );
    expect(css).toContain(`${SEARCH_MARK_SELECTOR} {`);
    expect(css).not.toContain("mark[data-search-mark] {");
  });
});
