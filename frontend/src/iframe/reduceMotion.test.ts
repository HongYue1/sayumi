import { afterEach, describe, expect, it } from "vitest";
import { prefersReducedMotion } from "./reduceMotion";

type MatchMedia = typeof window.matchMedia;

const realMatchMedia: MatchMedia = window.matchMedia;

function mql(matches: boolean, query: string): MediaQueryList {
  return {
    matches,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  } as unknown as MediaQueryList;
}

afterEach(() => {
  window.matchMedia = realMatchMedia;
});

describe("prefersReducedMotion", () => {
  it("reports the current setting", () => {
    window.matchMedia = (q: string) => mql(true, q);
    expect(prefersReducedMotion()).toBe(true);
  });

  it("is false where the DOM has no matchMedia", () => {
    (window as unknown as { matchMedia?: MatchMedia }).matchMedia = undefined;
    expect(prefersReducedMotion()).toBe(false);
  });

  it("reuses one MediaQueryList while matchMedia is unchanged", () => {
    let built = 0;
    window.matchMedia = (q: string) => {
      built++;
      return mql(true, q);
    };
    prefersReducedMotion();
    prefersReducedMotion();
    expect(built).toBe(1);
  });

  it("re-reads the live query rather than caching the value", () => {
    const live = mql(false, "(prefers-reduced-motion: reduce)");
    window.matchMedia = () => live;
    expect(prefersReducedMotion()).toBe(false);
    (live as { matches: boolean }).matches = true;
    expect(prefersReducedMotion()).toBe(true);
  });

  // boundary.test.ts replaces window.matchMedia partway through its file,
  // after earlier cases have already read the setting. A cache that ignored
  // the swap would serve it a query built by the old implementation.
  it("rebuilds when window.matchMedia is replaced", () => {
    window.matchMedia = (q: string) => mql(true, q);
    expect(prefersReducedMotion()).toBe(true);
    window.matchMedia = (q: string) => mql(false, q);
    expect(prefersReducedMotion()).toBe(false);
  });
});
