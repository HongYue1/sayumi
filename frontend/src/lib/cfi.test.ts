import { describe, it, expect } from "vitest";
import {
  generateCFI,
  resolveCFI,
  resolveCFIPoint,
  elementTextLength,
  cfiElementPath,
} from "~/lib/cfi";
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
  // wrong-but-valid index.
  for (const bad of [
    "cfi:3x",
    "cfi:1/1.5",
    "cfi:",
    "cfi:abc",
    "cfi:0",
    "cfi:1/0",
    // Only the last segment may carry `:C`. A mid-path offset is a value the
    // generator never mints, so it must not resolve to its element path.
    "cfi:1:1/1",
  ]) {
    it(`rejects the malformed CFI "${bad}"`, () => {
      setBody(`<div><p><span>x</span></p></div>`);
      expect(resolveCFI(bad, document)).toBeNull();
    });
  }
});

describe("CFI text offsets", () => {
  it("appends a character offset to the element path", () => {
    setBody(`<p id="t">abcdef</p>`);
    const target = document.getElementById("t")!;
    expect(generateCFI(target, document, 4)).toBe("cfi:1:4");
    // No offset measured: the plain element path, as before.
    expect(generateCFI(target, document)).toBe("cfi:1");
  });

  it("clamps out-of-range offsets instead of failing", () => {
    setBody(`<p id="t">abcdef</p>`);
    const target = document.getElementById("t")!;
    expect(generateCFI(target, document, 99)).toBe("cfi:1:6");
    expect(generateCFI(target, document, -5)).toBe("cfi:1:0");
    // A non-measurement degrades to the element path.
    expect(generateCFI(target, document, NaN)).toBe("cfi:1");
  });

  it("counts text across inline descendants as one run", () => {
    setBody(`<p id="t">ab<em>cd</em>ef</p>`);
    expect(elementTextLength(document.getElementById("t")!)).toBe(6);
    // A boundary offset sticks to the end of the left run — the same caret
    // point as the start of the right run, deterministically.
    const edge = resolveCFIPoint("cfi:1:4", document)!.range;
    expect(edge.collapsed).toBe(true);
    expect(edge.startContainer.textContent).toBe("cd");
    expect(edge.startOffset).toBe(2);
    const inner = resolveCFIPoint("cfi:1:5", document)!.range;
    expect(inner.startContainer.textContent).toBe("ef");
    expect(inner.startOffset).toBe(1);
  });

  it("maps an offset identically with a live highlight as without", () => {
    // Marks wrap the original text in place, so the element-text count is
    // highlight-stable by construction: no invalidation dance needed.
    setBody(`<p id="t">abcdef</p>`);
    const target = document.getElementById("t")!;
    const clean = generateCFI(target, document, 4);
    wrapMark(target, 1, 2);
    expect(generateCFI(target, document, 4)).toBe(clean);
    const range = resolveCFIPoint(clean!, document)!.range;
    expect(range.startContainer.textContent).toBe("def");
    expect(range.startOffset).toBe(1);
    clearSearchMarks();
    const cleared = resolveCFIPoint(clean!, document)!.range;
    expect(cleared.startContainer.textContent).toBe("abcdef");
    expect(cleared.startOffset).toBe(4);
  });

  it("resolves an offset-less value to the start of the element", () => {
    setBody(`<div><p id="t">xy</p></div>`);
    const target = document.getElementById("t")!;
    // Element and range come from ONE resolve, so the paged restore that
    // needs both never walks the same path twice.
    const point = resolveCFIPoint("cfi:1/1", document)!;
    expect(point.element).toBe(target);
    expect(point.range.startContainer).toBe(target);
    expect(point.range.startOffset).toBe(0);
  });

  it("resolveCFI tolerates the suffix for element-only callers", () => {
    setBody(`<div><p id="t">xy</p></div>`);
    const target = document.getElementById("t")!;
    expect(resolveCFI("cfi:1/1:1", document)).toBe(target);
  });

  it("clamps a resolve past shrunken text to the end instead of failing", () => {
    setBody(`<p id="t">abcdef</p>`);
    const range = resolveCFIPoint("cfi:1:99", document)!.range;
    expect(range.startContainer.textContent).toBe("abcdef");
    expect(range.startOffset).toBe(6);
  });

  it("falls back to the element's own start when it holds no text", () => {
    // The offset maps nowhere inside an empty element, but the element itself
    // did resolve — the caller gets a point to scroll to rather than null.
    setBody(`<div><p id="t"></p></div>`);
    const target = document.getElementById("t")!;
    const point = resolveCFIPoint("cfi:1/1:3", document)!;
    expect(point.element).toBe(target);
    expect(point.range.startContainer).toBe(target);
    expect(point.range.startOffset).toBe(0);
    expect(point.range.collapsed).toBe(true);
  });

  it("returns null only when the element itself is gone", () => {
    setBody(`<p id="t">abcdef</p>`);
    expect(resolveCFIPoint("cfi:2:1", document)).toBeNull();
    expect(resolveCFIPoint("cfi:1:1x", document)).toBeNull();
    expect(resolveCFIPoint("cfi:1:", document)).toBeNull();
  });
});

describe("cfiElementPath", () => {
  it("drops a text offset from the last segment", () => {
    expect(cfiElementPath("cfi:1/2:40")).toBe("cfi:1/2");
    expect(cfiElementPath("cfi:1/2/3:0")).toBe("cfi:1/2/3");
  });

  it("keeps a body-level index that only looks like an offset", () => {
    // "cfi:3" addresses the third child of <body>, so that number IS the
    // element index. Trimming a trailing ":N" would collapse every top-level
    // block onto one path and make unrelated anchors compare equal.
    expect(cfiElementPath("cfi:3")).toBe("cfi:3");
    expect(cfiElementPath("cfi:7")).toBe("cfi:7");
    expect(cfiElementPath("cfi:3:40")).toBe("cfi:3");
  });

  it("passes offset-less and foreign values through untouched", () => {
    expect(cfiElementPath("cfi:1/2")).toBe("cfi:1/2");
    expect(cfiElementPath("cfi:1/2:x")).toBe("cfi:1/2:x");
    expect(cfiElementPath("nonsense")).toBe("nonsense");
  });

  it("agrees with resolveCFI on which element a value addresses", () => {
    // A stripped path has to resolve to the same element as the value it came
    // from, or bookmark identity and navigation would disagree.
    setBody(`<p id="first">a</p><div><p id="t">b</p></div>`);
    const nested = document.getElementById("t")!;
    const withOffset = generateCFI(nested, document, 1)!;
    expect(resolveCFI(cfiElementPath(withOffset), document)).toBe(nested);
    const first = document.getElementById("first")!;
    const bodyLevel = generateCFI(first, document)!;
    expect(bodyLevel).toBe("cfi:1");
    expect(resolveCFI(cfiElementPath(bodyLevel), document)).toBe(first);
  });
});
