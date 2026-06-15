import { describe, it, expect } from "vitest";
import {
  isProgressDuplicate,
  chooseBootProgress,
  isBookmarkAtPosition,
} from "~/lib/progress";
import type { ProgressData } from "~/api/client";

const p = (chapter: number, percent: number, cfi?: string): ProgressData =>
  ({ chapter, percent, cfi }) as ProgressData;

describe("isProgressDuplicate", () => {
  it("is true for the same chapter within the epsilon", () => {
    expect(isProgressDuplicate({ chapter: 2, percent: 0.5 }, { chapter: 2, percent: 0.5004 })).toBe(true);
  });
  it("is false when the chapter differs", () => {
    expect(isProgressDuplicate({ chapter: 3, percent: 0.5 }, { chapter: 2, percent: 0.5 })).toBe(false);
  });
  it("is false when the percent moved beyond the epsilon", () => {
    expect(isProgressDuplicate({ chapter: 2, percent: 0.5 }, { chapter: 2, percent: 0.52 })).toBe(false);
  });
  it("treats the unset sentinel (-1) as not a duplicate", () => {
    expect(isProgressDuplicate({ chapter: 0, percent: 0 }, { chapter: -1, percent: -1 })).toBe(false);
  });
});

describe("chooseBootProgress", () => {
  it("keeps the server position when it is on a later chapter", () => {
    expect(chooseBootProgress(p(2, 0.1), p(1, 0.9), true)).toMatchObject({ chapter: 2, percent: 0.1 });
  });
  it("prefers the cache when it is further into the same chapter", () => {
    expect(chooseBootProgress(p(1, 0.1), p(1, 0.5), true)).toMatchObject({ chapter: 1, percent: 0.5 });
  });
  it("keeps the server on an exact tie (cache not strictly ahead)", () => {
    expect(chooseBootProgress(p(1, 0.5), p(1, 0.5), true)).toMatchObject({ chapter: 1, percent: 0.5 });
  });
  it("falls back to the cache when the server never loaded", () => {
    expect(chooseBootProgress(p(5, 0.9), p(0, 0), false)).toMatchObject({ chapter: 0, percent: 0 });
  });
});

describe("isBookmarkAtPosition", () => {
  it("is true at the same chapter within 0.02", () => {
    expect(isBookmarkAtPosition({ chapter: 1, percent: 0.5 }, 1, 0.51)).toBe(true);
  });
  it("is false on a different chapter", () => {
    expect(isBookmarkAtPosition({ chapter: 2, percent: 0.5 }, 1, 0.5)).toBe(false);
  });
  it("is false when the percent is farther than 0.02", () => {
    expect(isBookmarkAtPosition({ chapter: 1, percent: 0.5 }, 1, 0.6)).toBe(false);
  });
});
