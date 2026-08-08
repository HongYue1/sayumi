import { describe, it, expect } from "vitest";
import {
  isProgressDuplicate,
  chooseBootProgress,
  isBookmarkAtPosition,
  findBookmarkAtPosition,
  BOOKMARK_EPSILON,
  PROGRESS_EPSILON,
  PROGRESS_UNSET,
} from "~/lib/progress";
import type { ProgressData } from "~/api/client";

const p = (chapter: number, percent: number, cfi?: string): ProgressData =>
  ({ chapter, percent, cfi }) as ProgressData;

describe("isProgressDuplicate", () => {
  it("is true for the same chapter within the epsilon", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5 },
        { chapter: 2, percent: 0.5004 },
      ),
    ).toBe(true);
  });
  it("is false when the chapter differs", () => {
    expect(
      isProgressDuplicate(
        { chapter: 3, percent: 0.5 },
        { chapter: 2, percent: 0.5 },
      ),
    ).toBe(false);
  });
  it("is false when the percent moved beyond the epsilon", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5 },
        { chapter: 2, percent: 0.52 },
      ),
    ).toBe(false);
  });
  it("is false at exactly the epsilon -- the bound is strict", () => {
    // Built off zero so the delta is exactly PROGRESS_EPSILON in binary64;
    // 0.5 + 0.001 is not, and would pass either way.
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0 },
        { chapter: 2, percent: PROGRESS_EPSILON },
      ),
    ).toBe(false);
  });
  it("does not dedupe a delta five times the epsilon", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0 },
        { chapter: 2, percent: 0.005 },
      ),
    ).toBe(false);
  });
  it("treats the unset sentinel as not a duplicate", () => {
    expect(PROGRESS_UNSET).toBe(-1);
    expect(
      isProgressDuplicate(
        { chapter: 0, percent: 0 },
        { chapter: PROGRESS_UNSET, percent: PROGRESS_UNSET },
      ),
    ).toBe(false);
  });

  // The anchor is part of the key. Paged percent is quantized to
  // page/(totalPages-1), so an anchor-preserving relayout can hold the ratio
  // while the anchoring block moves; dropping the CFI here swallowed that
  // write and left the reader restoring from a stale anchor.
  it("is false when only the anchor moved", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5, cfi: "cfi:/4/2" },
        { chapter: 2, percent: 0.5, cfi: "cfi:/4/8" },
      ),
    ).toBe(false);
  });
  it("is false when an anchor first resolves over the empty marker", () => {
    expect(
      isProgressDuplicate(
        { chapter: 0, percent: 0, cfi: "cfi:/4/2" },
        { chapter: 0, percent: 0, cfi: "" },
      ),
    ).toBe(false);
  });
  it("is false when one side carries no anchor at all", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5, cfi: "cfi:/4/2" },
        { chapter: 2, percent: 0.5 },
      ),
    ).toBe(false);
  });
  it("is still true when the anchor is identical", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5, cfi: "cfi:/4/2" },
        { chapter: 2, percent: 0.5004, cfi: "cfi:/4/2" },
      ),
    ).toBe(true);
  });
  it("is still true when neither side carries an anchor", () => {
    expect(
      isProgressDuplicate(
        { chapter: 2, percent: 0.5 },
        { chapter: 2, percent: 0.5 },
      ),
    ).toBe(true);
  });
});

describe("chooseBootProgress", () => {
  it("uses the page-hide cache as the newer position", () => {
    expect(chooseBootProgress(p(1, 0.1), p(1, 0.5))).toMatchObject({
      chapter: 1,
      percent: 0.5,
    });
  });
  it("keeps a newer cached backward navigation over an ahead server", () => {
    expect(chooseBootProgress(p(5, 0.9), p(2, 0.25))).toMatchObject({
      chapter: 2,
      percent: 0.25,
    });
  });
  it("preserves the cached semantic anchor", () => {
    expect(
      chooseBootProgress(p(5, 0.9, "cfi:5"), p(2, 0.25, "cfi:1/3")),
    ).toMatchObject({ cfi: "cfi:1/3" });
  });
  it("never consults the server value, even at the origin", () => {
    // Documents the policy rather than endorsing it: a cache written by a
    // page hide outlives having been persisted, so this rewinds a client that
    // read on elsewhere. Arbitration needs a server timestamp the API does
    // not currently expose.
    expect(chooseBootProgress(p(9, 0.99, "cfi:server"), p(0, 0, ""))).toEqual({
      chapter: 0,
      percent: 0,
      cfi: "",
    });
  });
});

describe("isBookmarkAtPosition", () => {
  it("is true at the same chapter within 0.02", () => {
    expect(isBookmarkAtPosition({ chapter: 1, percent: 0.5 }, 1, 0.51)).toBe(
      true,
    );
  });
  it("is false on a different chapter", () => {
    expect(isBookmarkAtPosition({ chapter: 2, percent: 0.5 }, 1, 0.5)).toBe(
      false,
    );
  });
  it("is false when the percent is farther than 0.02", () => {
    expect(isBookmarkAtPosition({ chapter: 1, percent: 0.5 }, 1, 0.6)).toBe(
      false,
    );
  });
  it("is false at exactly the epsilon -- the bound is strict", () => {
    // Exact binary64 delta, for the same reason as the progress bound above.
    expect(
      isBookmarkAtPosition({ chapter: 1, percent: 0 }, 1, BOOKMARK_EPSILON),
    ).toBe(false);
  });
});

// The delete path flows from this predicate, so the anchor rules bias every
// doubt toward NO match: a wrong create is recoverable with a second tap; a
// wrong delete loses the label and note.
describe("isBookmarkAtPosition anchor paths", () => {
  it("matches an exact anchor pair at the same percent", () => {
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:a" },
        1,
        0.5,
        "cfi:a",
      ),
    ).toBe(true);
  });

  it("refuses a different anchor even inside the legacy bucket (long chapter)", () => {
    // One page away in a 100-page chapter: the 0.0101 delta is inside the
    // legacy 0.02 bucket, but the anchor moved -- the toggle must create.
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:a" },
        1,
        0.5101,
        "cfi:b",
      ),
    ).toBe(false);
  });

  it("refuses a shared anchor when the percent moved past the same-spot bucket", () => {
    // The degenerate one-element chapter: several pages anchor the same cfi,
    // so a page of travel keeps the anchor and only the percent tells.
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:a" },
        1,
        0.5101,
        "cfi:a",
      ),
    ).toBe(false);
  });

  it("falls back to the legacy bucket when either side lacks an anchor", () => {
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:a" },
        1,
        0.51,
        undefined,
      ),
    ).toBe(true);
    expect(
      isBookmarkAtPosition({ chapter: 1, percent: 0.5 }, 1, 0.51, "cfi:a"),
    ).toBe(true);
  });
});

describe("findBookmarkAtPosition", () => {
  it("returns the nearest match, not the first", () => {
    const far = { chapter: 1, percent: 0.515, id: "far" };
    const near = { chapter: 1, percent: 0.505, id: "near" };
    expect(findBookmarkAtPosition([far, near], 1, 0.5)?.id).toBe("near");
  });

  it("returns null when nothing matches", () => {
    expect(
      findBookmarkAtPosition([{ chapter: 2, percent: 0.5 }], 1, 0.5),
    ).toBeNull();
  });
});
