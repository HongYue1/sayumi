import { describe, it, expect } from "vitest";
import { generateCFI, resolveCFI } from "~/lib/cfi";
import {
  SEARCH_MARK_ATTRIBUTE,
  SEARCH_MARK_SELECTOR,
  SEARCH_MARK_VALUE,
} from "~/lib/searchMarks";

function setBody(html: string): void {
  document.body.innerHTML = html;
}

// Mirrors iframe/searchHighlight.ts: a match is wrapped in place with
// Range.surroundContents, and clearing unwraps and normalises the parent.
function wrapMark(container: Element, start: number, length: number): Element {
  const text = container.firstChild as Text;
  const mark = document.createElement("mark");
  mark.className = "search-highlight";
  mark.setAttribute(SEARCH_MARK_ATTRIBUTE, SEARCH_MARK_VALUE);
  const range = document.createRange();
  range.setStart(text, start);
  range.setEnd(text, start + length);
  range.surroundContents(mark);
  return mark;
}

function clearSearchMarks(): void {
  const marks = Array.from(
    document.body.querySelectorAll(SEARCH_MARK_SELECTOR),
  );
  for (const mark of marks) {
    const parent = mark.parentNode as Element;
    mark.replaceWith(...Array.from(mark.childNodes));
    parent.normalize();
  }
}

describe("generateCFI / resolveCFI", () => {
  it("round-trips a nested element to a 1-based element path", () => {
    setBody(`<div><p>a</p><p><span id="t">x</span></p></div>`);
    const target = document.getElementById("t")!;
    const cfi = generateCFI(target, document);
    // body > div(1) > p(2) > span(1)
    expect(cfi).toBe("cfi:1/2/1");
    expect(resolveCFI(cfi!, document)).toBe(target);
  });

  it("resolves the same path after the chapter body is reconstructed", () => {
    const chapter = `<article><section><p id="t">x</p></section></article>`;
    setBody(chapter);
    const original = document.getElementById("t")!;
    const cfi = generateCFI(original, document);

    setBody(chapter);
    const replacement = document.getElementById("t")!;

    expect(replacement).not.toBe(original);
    expect(resolveCFI(cfi!, document)).toBe(replacement);
  });

  it("ignores text and comment nodes when indexing element siblings", () => {
    setBody(
      `<div>lead<!-- first --><p>one</p>between<!-- second --><p id="t">two</p></div>`,
    );
    const target = document.getElementById("t")!;
    const cfi = generateCFI(target, document);

    expect(cfi).toBe("cfi:1/2");
    expect(resolveCFI(cfi!, document)).toBe(target);
  });

  it("returns null when generating for body itself", () => {
    setBody(`<p>a</p>`);
    expect(generateCFI(document.body, document)).toBeNull();
  });

  it("returns null when generating for a detached element", () => {
    expect(generateCFI(document.createElement("div"), document)).toBeNull();
  });

  it("returns null when the CFI prefix is missing", () => {
    setBody(`<div></div>`);
    expect(resolveCFI("1/2/1", document)).toBeNull();
  });

  it("returns null when an index points past the children", () => {
    setBody(`<div></div>`);
    expect(resolveCFI("cfi:5", document)).toBeNull();
  });

  it("generates the same path with a live search mark as without", () => {
    setBody(`<div>intro hit tail<p id="t">anchor</p></div>`);
    const target = document.getElementById("t")!;
    const clean = generateCFI(target, document);

    wrapMark(document.body.firstElementChild!, 6, 3);

    expect(generateCFI(target, document)).toBe(clean);
    expect(resolveCFI(clean!, document)).toBe(target);
  });

  it("resolves a path minted under a live highlight after it is cleared", () => {
    setBody(
      `<div>intro hit tail<p id="t">anchor</p><p id="after">second</p></div>`,
    );
    const target = document.getElementById("t")!;

    wrapMark(document.body.firstElementChild!, 6, 3);
    const cfi = generateCFI(target, document);
    clearSearchMarks();

    // Counting the mark used to shift this path onto #after: a different real
    // element, so the caller got no null and never fell back to percent.
    expect(resolveCFI(cfi!, document)).toBe(target);
  });

  it("keeps book-authored marks in the index", () => {
    // searchHighlight.ts only unwraps its own attribute-tagged marks, so a
    // <mark> shipped by the book is permanent structure and must be counted.
    setBody(
      `<div><mark class="search-highlight">quoted</mark><p id="t">x</p></div>`,
    );
    const target = document.getElementById("t")!;

    expect(generateCFI(target, document)).toBe("cfi:1/2");
    expect(resolveCFI("cfi:1/2", document)).toBe(target);
  });

  it("counts an authored marker lookalike that lacks the private value", () => {
    setBody(
      `<div><mark data-search-mark="book-owned">quoted</mark><p id="t">x</p></div>`,
    );
    const target = document.getElementById("t")!;

    expect(generateCFI(target, document)).toBe("cfi:1/2");
    expect(resolveCFI("cfi:1/2", document)).toBe(target);
  });

  it("returns null when generating for a search mark itself", () => {
    setBody(`<div>intro hit tail</div>`);
    const mark = wrapMark(document.body.firstElementChild!, 6, 3);

    expect(generateCFI(mark, document)).toBeNull();
  });

  // Strict integer parse: a malformed/foreign segment must fail to null so
  // callers fall back to percent, rather than parseInt coercing it to a
  // wrong-but-valid index. Mirrors the (now deleted) inlined resolveCFILocal.
  for (const bad of [
    "cfi:3x",
    "cfi:1/1.5",
    "cfi:",
    "cfi:abc",
    "cfi:0",
    "cfi:1/0",
  ]) {
    it(`rejects the malformed CFI "${bad}"`, () => {
      setBody(`<div><p><span>x</span></p></div>`);
      expect(resolveCFI(bad, document)).toBeNull();
    });
  }
});
