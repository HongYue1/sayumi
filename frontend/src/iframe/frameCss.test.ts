// frame.css contract: the paged multicol scroller must not be user-scrollable.
// Page turns drive scrollLeft programmatically; a natively scrollable #content
// lets a touchpad horizontal swipe move the columns underneath the controller
// with no page turn registering (currentPage, indicator, and position report
// all desync). Reads the shipped stylesheet from disk — the ?raw bundler
// import resolves empty under vitest, so this asserts against the real file.
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";
import { PAGE_INDICATOR_CLEARANCE } from "./pagination";

const frameCSS = readFileSync("src/iframe/frame.css", "utf8");

function mountShell(): HTMLElement {
  document.head.innerHTML = `<style>${frameCSS}</style>`;
  document.body.innerHTML =
    '<div id="paged-clip"><div id="content"><div id="content-inner"></div></div></div>';
  const content = document.getElementById("content");
  if (!content) throw new Error("shell fixture missing");
  return content;
}

afterEach(() => {
  document.documentElement.className = "";
  document.head.innerHTML = "";
  document.body.innerHTML = "";
});

describe("paged #content overflow", () => {
  it.each(["paged", "paged-two"] as const)(
    "is user-locked in %s mode",
    (mode) => {
      const content = mountShell();
      document.documentElement.classList.add(mode);
      expect(getComputedStyle(content).overflowX).toBe("hidden");
    },
  );

  it("stays natively scrollable in scroll mode", () => {
    const content = mountShell();
    // No paged class: the scroll-mode #content has no overflow-x rule, so a
    // wheel keeps driving the chapter the way scroll mode expects.
    expect(getComputedStyle(content).overflowX).not.toBe("hidden");
  });
});

describe("search-hit highlight", () => {
  it("survives a book rule aimed at marks", () => {
    mountShell();
    // A book stylesheet loads after the base slot: without !important its
    // `#content mark` would switch the active hit off.
    const book = document.createElement("style");
    book.textContent = "#content mark { background: red; color: red; }";
    document.head.appendChild(book);
    const mark = document.createElement("mark");
    mark.setAttribute("data-search-mark", "sayumi");
    mark.textContent = "hit";
    document.getElementById("content-inner")?.appendChild(mark);
    const style = getComputedStyle(mark);
    // happy-dom reports the unresolved var, not rgb(): what matters is the
    // book rule lost. A resolved engine paints the light accent here.
    expect(style.backgroundColor).not.toBe("rgb(255, 0, 0)");
    expect(style.color).not.toBe("rgb(255, 0, 0)");
    expect(style.backgroundColor).toContain("2563eb");
  });
});

describe("page indicator clearance", () => {
  it("reserves a paged bottom inset the whole pill fits inside", () => {
    mountShell();
    document.documentElement.classList.add("paged");
    const pill = document.createElement("div");
    pill.id = "page-indicator";
    document.body.append(pill);

    const style = getComputedStyle(pill);
    const px = (value: string): number => Number.parseFloat(value) || 0;
    // The pill is fixed against the viewport bottom, so the strip it covers is
    // its offset plus its own box. There is no layout here, so the box is
    // summed from the declarations; a line box is never shorter than the font
    // size, which makes the sum a floor on the rendered pill rather than a
    // guess at it.
    expect(px(style.bottom)).toBeGreaterThan(0);
    expect(px(style.fontSize)).toBeGreaterThan(0);
    expect(px(style.paddingBottom)).toBeGreaterThan(0);

    const covered =
      px(style.bottom) +
      px(style.fontSize) +
      px(style.paddingTop) +
      px(style.paddingBottom) +
      px(style.borderTopWidth) +
      px(style.borderBottomWidth);

    expect(PAGE_INDICATOR_CLEARANCE).toBeGreaterThanOrEqual(covered);
  });

  it("draws the pill in the UI face, not in the reader's book font", () => {
    mountShell();
    document.documentElement.classList.add("paged");
    const pill = document.createElement("div");
    pill.id = "page-indicator";
    document.body.append(pill);

    // The reader's own font override is what the pill has to survive: it is
    // emitted at runtime, after this sheet, and carries !important.
    const override = document.createElement("style");
    override.textContent =
      "body, body * { font-family: 'Petrona', serif !important; }";
    document.head.append(override);

    // The pill is chrome the frame paints over the book. Inheriting body's
    // serif made it change shape with every reading-font switch.
    const family = getComputedStyle(pill).fontFamily;
    expect(family).toContain("Hanken Grotesk");
    expect(family).not.toContain("Petrona");
    expect(frameCSS).toContain("HankenGrotesk-VariableFont.woff2");
  });

  it("caps paged media at the column box, clearing the indicator strip", () => {
    // The column box floors its bottom inset at the clearance (see
    // getPagedVerticalInsets); the media cap must floor at the same strip, or
    // a full-page plate overshoots the box by the clearance at default margins
    // and loses its bottom rows to the paged clip. Matched against the
    // whitespace-flattened source so wrapping never breaks the pin.
    const flat = frameCSS.replace(/\s+/g, " ");
    expect(flat).toContain(
      `max(var(--paged-padding-bottom, 24px), ${PAGE_INDICATOR_CLEARANCE}px)`,
    );
  });
});
