import { beforeEach, describe, expect, it } from "vitest";
import type {
  SearchHighlightDeps,
  SearchHighlighter,
  SearchTextIndex,
} from "./searchHighlight";
import { SEARCH_MARK_SELECTOR } from "~/lib/searchMarks";
import {
  buildSearchTextIndex,
  createSearchHighlight,
  findFoldedMatch,
  foldQuery,
  matchesFoldedAt,
} from "./searchHighlight";

// The index assertions below double as the frontend half of the offset
// contract with internal/epub/search.go: each expected string is what the Go
// extractor produces for the same markup (verified against chapterPlainText).

const EMOJI = String.fromCodePoint(0x1f600);

let scrolled: Element[] = [];

beforeEach(() => {
  scrolled = [];
  document.body.innerHTML = "";
});

function indexOf(html: string): SearchTextIndex {
  const root = document.createElement("div");
  root.innerHTML = html;
  return buildSearchTextIndex(root);
}

function indexText(html: string): string {
  return indexOf(html).foldedChars.join("");
}

interface Harness {
  content: HTMLElement;
  highlighter: SearchHighlighter;
  pages: number[];
  pagedFor: Element[];
}

function setup(html: string, over: Partial<SearchHighlightDeps> = {}): Harness {
  document.body.innerHTML = '<div id="content"></div>';
  const content = document.getElementById("content") as HTMLElement;
  content.innerHTML = html;

  const pages: number[] = [];
  const pagedFor: Element[] = [];

  // happy-dom has no layout, so the reveal is observed rather than measured.
  Element.prototype.scrollIntoView = function scrollIntoViewStub(
    this: Element,
  ) {
    scrolled.push(this);
  };

  const deps: SearchHighlightDeps = {
    getContentEl: () => content,
    isContentReady: () => true,
    isPagedMode: () => false,
    goToPageInternal: (page) => {
      pages.push(page);
    },
    getElementPageIndex: (el) => {
      pagedFor.push(el);
      return 3;
    },
    ...over,
  };

  return { content, highlighter: createSearchHighlight(deps), pages, pagedFor };
}

function markTexts(content: HTMLElement): string[] {
  return Array.from(
    content.querySelectorAll(SEARCH_MARK_SELECTOR),
    (mark) => mark.textContent ?? "",
  );
}

describe("buildSearchTextIndex", () => {
  it("collapses a whitespace run into a single character", () => {
    expect(indexText("<p>Hello   World</p>")).toBe("hello world");
  });

  it("scores one boundary between blocks and for <br>", () => {
    expect(indexText("<p>one</p><p>two</p>")).toBe("one two");
    expect(indexText("<p>a<br>b</p>")).toBe("a b");
  });

  it("emits no leading or trailing space", () => {
    expect(indexText("  <p>  hello  </p>  ")).toBe("hello");
  });

  it("skips script, style and noscript text", () => {
    expect(
      indexText(
        "<p>a</p><script>var x = 1;</script><style>i{color:red}</style><p>b</p>",
      ),
    ).toBe("a b");
  });

  it("indexes svg text inline, matching the backend atom table", () => {
    // Go: <p>foo<svg><text>x</text></svg>bar</p> extracts as "fooxbar" because
    // atom.Svg is not in search.go's boundary switch.
    expect(indexText("<p>foo<svg><text>x</text></svg>bar</p>")).toBe("fooxbar");
  });

  it("indexes MathML inline, matching the backend atom table", () => {
    const root = document.createElement("div");
    root.innerHTML = "<p>foo<math><mi>x</mi></math>bar</p>";
    // happy-dom reports tagName "MATH" where browsers report "math"; assert
    // through localName so this fixture cannot drift into testing the DOM.
    expect(root.querySelector("math")?.localName).toBe("math");
    expect(buildSearchTextIndex(root).foldedChars.join("")).toBe("fooxbar");
  });

  it("does not score a boundary for <form>", () => {
    // sanitize.go unwraps <form>, so the frame never sees one; search.go drops
    // atom.Form from its switch for the same reason. If either list grows it
    // back, this offset shifts by one.
    expect(indexText("a<form>b</form>c")).toBe("abc");
  });

  it("treats Unicode-only whitespace as space, like unicode.IsSpace", () => {
    expect(indexText("<p>a\u00a0b</p>")).toBe("a b");
    expect(indexText("<p>a\u0085b</p>")).toBe("a b");
  });

  it("folds one code point per character, never expanding", () => {
    // \u0130 lowercases to i + combining dot in JavaScript; Go maps it to one
    // rune, so only the first code point may be kept.
    expect(indexText("<p>\u0130stanbul</p>")).toBe("istanbul");
  });

  it("counts an astral character as one index entry", () => {
    const index = indexOf("<p>a" + EMOJI + "b</p>");
    expect(index.length).toBe(3);
    expect(index.foldedChars.join("")).toBe("a" + EMOJI + "b");
  });

  it("stops the walk at the requested limit", () => {
    const root = document.createElement("div");
    root.innerHTML = "<p>alpha beta</p>";
    expect(buildSearchTextIndex(root).length).toBe(10);
    expect(buildSearchTextIndex(root, 5).length).toBe(5);
    expect(buildSearchTextIndex(root, 5).foldedChars.join("")).toBe("alpha");
  });
});

describe("folded scan", () => {
  it("trims and folds a query", () => {
    expect(foldQuery("  Beta ").join("")).toBe("beta");
    expect(foldQuery("   ")).toHaveLength(0);
  });

  it("finds the first occurrence, or -1", () => {
    const haystack = Array.from("alpha beta alpha");
    expect(findFoldedMatch(haystack, Array.from("alpha"))).toBe(0);
    expect(findFoldedMatch(haystack, Array.from("beta"))).toBe(6);
    expect(findFoldedMatch(haystack, Array.from("gamma"))).toBe(-1);
    expect(findFoldedMatch(haystack, [])).toBe(-1);
  });

  it("refuses an out-of-bounds compare", () => {
    const haystack = Array.from("abc");
    expect(matchesFoldedAt(haystack, 1, Array.from("bc"))).toBe(true);
    expect(matchesFoldedAt(haystack, 2, Array.from("bc"))).toBe(false);
    expect(matchesFoldedAt(haystack, -1, Array.from("a"))).toBe(false);
  });
});

describe("highlightSearchMatch", () => {
  it("wraps a multi-word match as one contiguous mark", () => {
    const h = setup("<p>hello world</p>");
    h.highlighter.highlightSearchMatch(0, 11, "hello world");
    expect(markTexts(h.content)).toEqual(["hello world"]);
  });

  it("merges across a collapsed non-breaking space", () => {
    const h = setup("<p>a\u00a0b</p>");
    h.highlighter.highlightSearchMatch(0, 3, "a b");
    expect(markTexts(h.content)).toEqual(["a\u00a0b"]);
  });

  it("splits per Text node so inline markup survives", () => {
    const h = setup("<p>ab<em>cd</em>ef</p>");
    h.highlighter.highlightSearchMatch(0, 6, "abcdef");
    expect(markTexts(h.content)).toEqual(["ab", "cd", "ef"]);
    expect(h.content.querySelector("em")).not.toBeNull();
  });

  it("splits a match that crosses a block boundary", () => {
    const h = setup("<p>one</p><p>two</p>");
    h.highlighter.highlightSearchMatch(0, 7, "one two");
    expect(markTexts(h.content)).toEqual(["one", "two"]);
  });

  it("trusts backend offsets when no query is supplied", () => {
    const h = setup("<p>alpha beta</p>");
    h.highlighter.highlightSearchMatch(6, 4, "");
    expect(markTexts(h.content)).toEqual(["beta"]);
  });

  it("falls back to a query scan when offsets overrun the chapter", () => {
    const h = setup("<p>alpha beta</p>");
    h.highlighter.highlightSearchMatch(500, 4, "beta");
    expect(markTexts(h.content)).toEqual(["beta"]);
  });

  it("falls back when the offset points at the wrong words", () => {
    const h = setup("<p>alpha beta gamma</p>");
    h.highlighter.highlightSearchMatch(0, 4, "beta");
    expect(markTexts(h.content)).toEqual(["beta"]);
  });

  it("does nothing when offsets are unusable and there is no query", () => {
    const h = setup("<p>alpha beta</p>");
    h.highlighter.highlightSearchMatch(500, 3, "");
    expect(markTexts(h.content)).toEqual([]);
  });

  it("rejects unusable arguments without touching the DOM", () => {
    const h = setup("<p>alpha beta</p>");
    const before = h.content.innerHTML;
    h.highlighter.highlightSearchMatch(-1, 5, "alpha");
    h.highlighter.highlightSearchMatch(0, 0, "alpha");
    h.highlighter.highlightSearchMatch(0.5, 5, "alpha");
    h.highlighter.highlightSearchMatch(Number.NaN, 5, "alpha");
    h.highlighter.highlightSearchMatch(Number.MAX_SAFE_INTEGER, 5, "alpha");
    expect(h.content.innerHTML).toBe(before);
  });

  it("waits until the chapter is ready", () => {
    const h = setup("<p>alpha</p>", { isContentReady: () => false });
    h.highlighter.highlightSearchMatch(0, 5, "alpha");
    expect(markTexts(h.content)).toEqual([]);
  });

  it("survives a missing content element", () => {
    const h = setup("<p>alpha</p>", { getContentEl: () => null });
    expect(() =>
      h.highlighter.highlightSearchMatch(0, 5, "alpha"),
    ).not.toThrow();
    expect(markTexts(h.content)).toEqual([]);
  });

  it("replaces the previous highlight", () => {
    const h = setup("<p>alpha beta</p>");
    h.highlighter.highlightSearchMatch(0, 5, "alpha");
    h.highlighter.highlightSearchMatch(6, 4, "beta");
    expect(markTexts(h.content)).toEqual(["beta"]);
  });
});

describe("reveal", () => {
  it("scrolls the mark into view in scroll mode", () => {
    const h = setup("<p>hello world</p>");
    h.highlighter.highlightSearchMatch(0, 11, "hello world");
    expect(scrolled.at(-1)?.textContent).toBe("hello world");
    expect(h.pages).toEqual([]);
  });

  it("pages to the first mark of the match, not the last", () => {
    const h = setup("<p>one</p><p>two</p>", { isPagedMode: () => true });
    h.highlighter.highlightSearchMatch(0, 7, "one two");
    expect(h.pagedFor.map((el) => el.textContent)).toEqual(["one"]);
    expect(h.pages).toEqual([3]);
    expect(scrolled).toEqual([]);
  });
});

describe("clearSearchHighlights", () => {
  it("restores the original markup exactly", () => {
    const h = setup("<p>hello world</p>");
    const before = h.content.innerHTML;
    h.highlighter.highlightSearchMatch(0, 11, "hello world");
    expect(h.content.innerHTML).not.toBe(before);
    h.highlighter.clearSearchHighlights();
    expect(h.content.innerHTML).toBe(before);
  });

  it("leaves book-authored marks alone", () => {
    const h = setup('<p><mark class="search-highlight">book</mark> text</p>');
    const before = h.content.innerHTML;
    h.highlighter.highlightSearchMatch(5, 4, "text");
    expect(markTexts(h.content)).toEqual(["text"]);
    h.highlighter.clearSearchHighlights();
    expect(h.content.innerHTML).toBe(before);
  });

  it("never adopts or unwraps a book-authored search marker", () => {
    const h = setup(
      '<p><mark id="authored" data-search-mark="book-owned">book</mark> text</p>',
    );
    const authored = h.content.querySelector<HTMLElement>("#authored");
    expect(authored).not.toBeNull();

    h.highlighter.highlightSearchMatch(5, 4, "text");
    expect(authored?.isConnected).toBe(true);
    expect(authored?.getAttribute("data-search-mark")).toBe("book-owned");
    h.highlighter.clearSearchHighlights();
    expect(authored?.isConnected).toBe(true);
    expect(authored?.outerHTML).toBe(
      '<mark id="authored" data-search-mark="book-owned">book</mark>',
    );
  });
});
