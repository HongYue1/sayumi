import { describe, expect, it } from "vitest";

import {
  clampPage,
  elementLogicalX,
  logicalOffsetForPage,
  pageAtOffset,
  pageCountFrom,
  pageForRatio,
  pagePercent,
  pageStrideFrom,
} from "./pagination";

// Fixtures mirror frame.css: single-column paged is column-width:100vw with
// column-gap:0; the two-page spread is column-count:2 with column-gap:1px (the
// column-rule paints inside the gap and adds no width). #content itself carries
// no padding -- the reading inset lives on #content-inner -- so the stride
// between page origins is always the container width plus the gap.
const SINGLE = { width: 800, gap: 0 };
const SPREAD = { width: 1000, gap: 1 };

describe("pageStrideFrom", () => {
  it("is the column box plus the multicol gap", () => {
    expect(pageStrideFrom(SINGLE.width, SINGLE.gap)).toBe(800);
    expect(pageStrideFrom(SPREAD.width, SPREAD.gap)).toBe(1001);
  });

  it("ignores a non-numeric gap and never returns a zero stride", () => {
    expect(pageStrideFrom(800, NaN)).toBe(800);
    expect(pageStrideFrom(0, 0)).toBe(1);
    expect(pageStrideFrom(NaN, 0)).toBe(1);
  });
});

describe("pageCountFrom", () => {
  it("counts stride-aligned pages", () => {
    expect(pageCountFrom(0, 800)).toBe(1);
    expect(pageCountFrom(2400, 800)).toBe(4);
    expect(pageCountFrom(3003, 1001)).toBe(4);
  });

  it("rounds instead of ceiling, so sub-pixel drift adds no phantom page", () => {
    expect(pageCountFrom(2400.4, 800)).toBe(4);
    expect(pageCountFrom(2399.6, 800)).toBe(4);
  });

  it("survives a zero or non-finite stride", () => {
    expect(pageCountFrom(2400, 0)).toBe(1);
    expect(pageCountFrom(NaN, 800)).toBe(1);
  });
});

describe("clampPage", () => {
  it("clamps into range and treats an unpaginated chapter as page 0", () => {
    expect(clampPage(-3, 5)).toBe(0);
    expect(clampPage(9, 5)).toBe(4);
    expect(clampPage(2, 5)).toBe(2);
    expect(clampPage(2, 0)).toBe(0);
    expect(clampPage(NaN, 5)).toBe(0);
  });
});

describe("logicalOffsetForPage / pageAtOffset", () => {
  it("round-trips every page in a chapter", () => {
    const stride = 800;
    const total = 6;
    const max = (total - 1) * stride;
    for (let page = 0; page < total; page++) {
      const offset = logicalOffsetForPage(page, stride, max);
      expect(offset).toBe(page * stride);
      expect(pageAtOffset(offset, stride, total)).toBe(page);
    }
  });

  it("clamps to the real scroll range", () => {
    expect(logicalOffsetForPage(9, 800, 2400)).toBe(2400);
    expect(logicalOffsetForPage(-1, 800, 2400)).toBe(0);
  });

  it("floors within a page, so a boundary belongs to the page it starts", () => {
    expect(pageAtOffset(799.5, 800, 4)).toBe(0);
    expect(pageAtOffset(800, 800, 4)).toBe(1);
    expect(pageAtOffset(1599.9, 800, 4)).toBe(1);
  });

  it("round-trips spreads, where the 1px gap accumulates per page", () => {
    const stride = pageStrideFrom(SPREAD.width, SPREAD.gap);
    const max = 3 * stride;
    for (let page = 0; page < 4; page++) {
      const offset = logicalOffsetForPage(page, stride, max);
      expect(pageAtOffset(offset, stride, 4)).toBe(page);
    }
  });
});

describe("elementLogicalX", () => {
  const stride = 800;

  it("measures from the container's leading edge in LTR", () => {
    expect(
      elementLogicalX({
        containerLeft: 0,
        containerRight: 800,
        elementLeft: 0,
        elementRight: 400,
        domScrollLeft: 2 * stride,
        rtl: false,
      }),
    ).toBe(1600);
  });

  it("mirrors RTL's negative scroll offsets back into reading order", () => {
    expect(
      elementLogicalX({
        containerLeft: 0,
        containerRight: 800,
        elementLeft: 400,
        elementRight: 800,
        domScrollLeft: -2 * stride,
        rtl: true,
      }),
    ).toBe(1600);
  });

  it("resolves the same page on both axes", () => {
    const ltr = elementLogicalX({
      containerLeft: 0,
      containerRight: 800,
      elementLeft: 10,
      elementRight: 200,
      domScrollLeft: 800,
      rtl: false,
    });
    const rtl = elementLogicalX({
      containerLeft: 0,
      containerRight: 800,
      elementLeft: 600,
      elementRight: 790,
      domScrollLeft: -800,
      rtl: true,
    });
    expect(pageAtOffset(ltr, stride, 4)).toBe(1);
    expect(pageAtOffset(rtl, stride, 4)).toBe(1);
  });
});

describe("pageForRatio / pagePercent", () => {
  it("maps the endpoints exactly", () => {
    expect(pageForRatio(0, 10)).toBe(0);
    expect(pageForRatio(1, 10)).toBe(9);
    expect(pagePercent(0, 10)).toBe(0);
    expect(pagePercent(9, 10)).toBe(1);
  });

  it("collapses to page 0 when there is nothing to page through", () => {
    expect(pageForRatio(0.7, 1)).toBe(0);
    expect(pageForRatio(NaN, 12)).toBe(0);
    expect(pagePercent(0, 1)).toBe(0);
    expect(pagePercent(3, 0)).toBe(0);
  });

  it("round-trips a page through percent and back", () => {
    for (const total of [2, 5, 13, 40]) {
      for (let page = 0; page < total; page++) {
        expect(pageForRatio(pagePercent(page, total), total)).toBe(page);
      }
    }
  });

  it("lands on the nearest page when a relayout changes the count", () => {
    // 10 pages -> 12 after a font settles: ratio remapping rounds to a
    // neighbour, which is why a resolved anchor element is preferred.
    expect(pageForRatio(pagePercent(3, 10), 12)).toBe(4);
    expect(pageForRatio(pagePercent(9, 10), 12)).toBe(11);
  });
});
