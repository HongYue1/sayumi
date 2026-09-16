import { describe, it, expect } from "vitest";
import {
  isProgressDuplicate,
  chooseBootProgress,
  isBookmarkAtPosition,
  findBookmarkAtPosition,
  calcBookProgress,
  BOOKMARK_EPSILON,
  PROGRESS_EPSILON,
  PROGRESS_UNSET,
} from "~/lib/progress";
import type { ProgressData } from "~/api/client";
import type { CachedProgress } from "~/lib/progress";

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
  // Both sides carry timestamps the SERVER issued: the cache keeps whichever
  // one was current when it was written, so no client clock is compared.
  const cachedAt = (
    position: ProgressData,
    serverUpdatedAt?: string,
  ): CachedProgress => ({ ...position, serverUpdatedAt });
  const serverAt = (
    position: ProgressData,
    updatedAt: string,
  ): ProgressData => ({ ...position, updatedAt });

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
  it("keeps the cache while the server has not moved past it", () => {
    // The page-hide beacon never landed: the server still holds the position
    // this tab was told about, so the unsaved cache is the newest thing here.
    expect(
      chooseBootProgress(
        serverAt(p(1, 0.1), "2026-02-03 04:05:06"),
        cachedAt(p(3, 0.8), "2026-02-03 04:05:06"),
      ),
    ).toMatchObject({ chapter: 3, percent: 0.8 });
  });
  it("yields to a position the server recorded after the cache was written", () => {
    // The rewind this used to cause: another client read on after this tab
    // hid, and the next boot here overwrote it with the older cached spot.
    expect(
      chooseBootProgress(
        serverAt(p(9, 0.99, "cfi:server"), "2026-02-03 04:05:07"),
        cachedAt(p(3, 0.8), "2026-02-03 04:05:06"),
      ),
    ).toMatchObject({ chapter: 9, percent: 0.99, cfi: "cfi:server" });
  });
  it("orders the fixed timestamp layout chronologically", () => {
    // Same layout on both sides, so a plain string comparison is a date
    // comparison -- but only in that direction: an older server value must
    // not win just because it sorts differently.
    expect(
      chooseBootProgress(
        serverAt(p(9, 0.99), "2026-02-03 09:00:00"),
        cachedAt(p(3, 0.8), "2026-02-03 10:00:00"),
      ),
    ).toMatchObject({ chapter: 3, percent: 0.8 });
  });
  it("keeps the cache when either side has no timestamp", () => {
    // A cache written before the baseline existed, and a book the server
    // holds no position for: neither pair can be ordered, so the crash-guard
    // copy stands -- the behaviour every earlier cache shipped with.
    expect(
      chooseBootProgress(serverAt(p(9, 0.99), "2026-02-03 04:05:07"), p(0, 0)),
    ).toMatchObject({ chapter: 0, percent: 0 });
    expect(
      chooseBootProgress(p(9, 0.99), cachedAt(p(0, 0), "2026-02-03 04:05:06")),
    ).toMatchObject({ chapter: 0, percent: 0 });
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

  it("ignores text offsets: two taps at the same spot toggle, not duplicate", () => {
    // Per-pixel caret offsets differ between taps; matching on them would
    // create a second bookmark instead of deleting the first.
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:1/2:40" },
        1,
        0.5,
        "cfi:1/2:87",
      ),
    ).toBe(true);
  });

  it("still refuses different blocks that both carry offsets", () => {
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:1/2:40" },
        1,
        0.5,
        "cfi:1/3:12",
      ),
    ).toBe(false);
  });

  it("matches an offset bookmark from an element-only position", () => {
    // A bookmark minted with an offset against a boot-time element-only
    // position (or vice versa): the block agrees, the percent is tight.
    expect(
      isBookmarkAtPosition(
        { chapter: 1, percent: 0.5, cfi: "cfi:1/2:40" },
        1,
        0.5,
        "cfi:1/2",
      ),
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

describe("calcBookProgress", () => {
  it("weights chapters uniformly like the server tile value", () => {
    expect(calcBookProgress(0, 0, 10)).toBe(0);
    expect(calcBookProgress(4, 0.5, 10)).toBe(0.45);
    expect(calcBookProgress(9, 1, 10)).toBe(1);
  });

  it("returns 0 without a chapter count", () => {
    expect(calcBookProgress(3, 0.5, 0)).toBe(0);
    expect(calcBookProgress(3, 0.5, -2)).toBe(0);
  });

  it("clamps out-of-range positions", () => {
    expect(calcBookProgress(12, 0, 10)).toBe(1);
    expect(calcBookProgress(-1, 0, 10)).toBe(0);
    expect(calcBookProgress(2, NaN, 10)).toBe(0);
  });
});
