// Shared reduced-motion probe for the frame engine.
//
// frame.ts, pagination.ts, boundary.ts and searchHighlight.ts each carried a
// copy of this helper, with three different cache lifetimes between them. This
// is the single implementation; two properties are load-bearing.
//
// LIVE. A MediaQueryList tracks the OS setting, so a cached instance still
// reports the current value on every read. Caching only avoids allocating one
// per call -- which is worth keeping, because boundary.show() runs on every
// touchmove of a pull gesture.
//
// GUARDED. window.matchMedia is absent in some DOMs (the test DOM among them),
// and the typeof check is the whole reason this is safe there.
//
// The cache is keyed on the matchMedia function it was built from, so a caller
// that replaces window.matchMedia is never served a MediaQueryList built by
// the implementation it replaced. That is not hypothetical: boundary.test.ts
// swaps in a reduce:true stub partway through the file, after earlier tests
// have already read the setting, and a bare module-level cache would hand it a
// stale instance and quietly assert nothing.
let cachedQuery: MediaQueryList | null = null;
let cachedFrom: typeof window.matchMedia | null = null;

export function prefersReducedMotion(): boolean {
  const matchMedia = window.matchMedia;
  if (typeof matchMedia !== "function") return false;
  if (cachedQuery === null || cachedFrom !== matchMedia) {
    // .call keeps `this` bound to window: a detached matchMedia reference
    // throws "Illegal invocation" in real browsers.
    cachedQuery = matchMedia.call(window, "(prefers-reduced-motion: reduce)");
    cachedFrom = matchMedia;
  }
  return cachedQuery.matches;
}
