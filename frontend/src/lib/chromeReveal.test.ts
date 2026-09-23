import { expect, it } from "vitest";

import { CHROME_REVEAL_ZONE_PX, inChromeRevealZone } from "./chromeReveal";

it("treats only the top edge as a reach for the reader chrome", () => {
  expect(inChromeRevealZone(0)).toBe(true);
  expect(inChromeRevealZone(CHROME_REVEAL_ZONE_PX)).toBe(true);
  expect(inChromeRevealZone(CHROME_REVEAL_ZONE_PX + 1)).toBe(false);
  expect(inChromeRevealZone(500)).toBe(false);
  // A pointer captured outside the viewport is not over the bar.
  expect(inChromeRevealZone(-1)).toBe(false);
});
