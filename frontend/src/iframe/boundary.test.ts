import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { BoundaryDeps } from "./boundary";
import {
  BOUNDARY_RESET_MS,
  BOUNDARY_SENT_HIDE_MS,
  EDGE_FLASH_MS,
  INDICATOR_SLIDE_PX,
  TOUCH_THRESHOLD,
  createBoundary,
  indicatorVisual,
} from "./boundary";

// frame.ts owns this one and passes it per gesture.
const WHEEL_THRESHOLD = 600;
// Comfortably past the touch hand-off threshold.
const TOUCH_PULL = TOUCH_THRESHOLD + 10;

type Sent = { boundary: "start" | "end"; seq: number };

function setup(over: Partial<BoundaryDeps> = {}) {
  const sent: Sent[] = [];
  const deps: BoundaryDeps = {
    sendMessage: (msg) => {
      sent.push({ boundary: msg.boundary, seq: msg.seq });
    },
    getActiveSeq: () => 7,
    hasPrevChapter: () => true,
    hasNextChapter: () => true,
    getWritingMode: () => "",
    ...over,
  };
  const boundary = createBoundary(deps);
  boundary.ensureElements();
  // Write the hidden baseline so "nothing shown" is observable as "0".
  boundary.reset();
  return { boundary, sent };
}

function pill(side: "top" | "bottom"): HTMLElement {
  const el = document.getElementById(`boundary-indicator-${side}`);
  if (!el) throw new Error(`missing ${side} indicator`);
  return el as HTMLElement;
}

function label(side: "top" | "bottom"): string {
  return pill(side).textContent ?? "";
}

describe("indicatorVisual", () => {
  it("is inert with no direction", () => {
    expect(indicatorVisual(null, 0, false)).toEqual({
      side: null,
      label: "",
      edge: false,
      opacity: 0,
      offset: INDICATOR_SLIDE_PX,
    });
  });

  it("ramps a pull to full opacity before the threshold", () => {
    expect(indicatorVisual("start", 0.5, false)).toEqual({
      side: "top",
      label: "Previous chapter",
      edge: false,
      opacity: 0.75,
      offset: 20,
    });
    expect(indicatorVisual("start", 2 / 3, false).opacity).toBe(1);
  });

  it("keeps an end-stop muted and arrives fully seated", () => {
    expect(indicatorVisual("edge-end", 1, false)).toEqual({
      side: "bottom",
      label: "End of book",
      edge: true,
      opacity: 0.8,
      offset: 0,
    });
    expect(indicatorVisual("edge-start", 1, false).label).toBe(
      "Beginning of book",
    );
  });

  it("drops the slide under reduced motion", () => {
    expect(indicatorVisual("end", 0, true).offset).toBe(0);
    expect(indicatorVisual("end", 0, false).offset).toBe(INDICATOR_SLIDE_PX);
  });

  it("clamps out-of-range and non-finite progress", () => {
    expect(indicatorVisual("end", 5, false).opacity).toBe(1);
    expect(indicatorVisual("end", -3, false).offset).toBe(INDICATOR_SLIDE_PX);
    expect(indicatorVisual("end", Number.NaN, false).opacity).toBe(0);
  });

  it("quantises so a barely-moved pull resolves to the same state", () => {
    expect(indicatorVisual("end", 1 / 3, false)).toEqual(
      indicatorVisual("end", 0.3333, false),
    );
  });
});

describe("createBoundary", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("creates one aria-hidden pill per edge, idempotently", () => {
    const { boundary } = setup();
    boundary.ensureElements();
    expect(document.querySelectorAll(".boundary-indicator")).toHaveLength(2);
    expect(pill("top").getAttribute("aria-hidden")).toBe("true");
    expect(pill("bottom").getAttribute("aria-hidden")).toBe("true");
  });

  it("hands off once the wheel threshold is crossed", () => {
    const { boundary, sent } = setup();
    boundary.accumulate(300, "end", WHEEL_THRESHOLD);
    expect(sent).toHaveLength(0);
    expect(label("bottom")).toBe("Next chapter");
    boundary.accumulate(300, "end", WHEEL_THRESHOLD);
    expect(sent).toEqual([{ boundary: "end", seq: 7 }]);
    boundary.accumulate(300, "end", WHEEL_THRESHOLD);
    expect(sent).toHaveLength(1);
  });

  it("requires a fresh threshold of wheel travel to hand off again", () => {
    const { boundary, sent } = setup();
    boundary.accumulate(WHEEL_THRESHOLD, "end", WHEEL_THRESHOLD);
    vi.advanceTimersByTime(BOUNDARY_SENT_HIDE_MS + 1);
    boundary.accumulate(300, "end", WHEEL_THRESHOLD);
    expect(sent).toHaveLength(1);
    boundary.accumulate(300, "end", WHEEL_THRESHOLD);
    expect(sent).toHaveLength(2);
  });

  it("abandons an unfinished pull after the idle timeout", () => {
    const { boundary, sent } = setup();
    boundary.accumulate(100, "start", WHEEL_THRESHOLD);
    expect(boundary.hasActiveDirection()).toBe(true);
    vi.advanceTimersByTime(BOUNDARY_RESET_MS + 1);
    expect(boundary.hasActiveDirection()).toBe(false);
    expect(pill("top").style.opacity).toBe("0");
    expect(label("top")).toBe("");
    expect(sent).toHaveLength(0);
  });

  it("accumulates the end-stop instead of tracking one frame's delta", () => {
    const { boundary, sent } = setup({ hasNextChapter: () => false });
    boundary.accumulate(12, "end", WHEEL_THRESHOLD);
    const first = Number(pill("bottom").style.opacity);
    boundary.accumulate(12, "end", WHEEL_THRESHOLD);
    const second = Number(pill("bottom").style.opacity);
    expect(second).toBeGreaterThan(first);
    expect(label("bottom")).toBe("End of book");
    expect(pill("bottom").classList.contains("edge")).toBe(true);
    // The pull handlers clear the pill on scroll-away only while a direction
    // is active, so an end-stop has to register as one.
    expect(boundary.hasActiveDirection()).toBe(true);
    expect(sent).toHaveLength(0);
  });

  it("hands off once per touch gesture even while the finger stays down", () => {
    const { boundary, sent } = setup();
    boundary.processTouch("end", TOUCH_PULL);
    expect(sent).toHaveLength(1);
    // The hide timer clears `sent`, but the caller keeps reporting absolute
    // pull distance from the same finger -- that must not hand off again.
    vi.advanceTimersByTime(BOUNDARY_SENT_HIDE_MS + 1);
    boundary.processTouch("end", TOUCH_PULL + 60);
    expect(sent).toHaveLength(1);
    boundary.endGesture();
    boundary.processTouch("end", TOUCH_PULL + 60);
    expect(sent).toHaveLength(2);
  });

  it("never hands off a touch pull at the book edge", () => {
    const { boundary, sent } = setup({ hasPrevChapter: () => false });
    boundary.processTouch("start", TOUCH_PULL * 2);
    expect(sent).toHaveLength(0);
    expect(label("top")).toBe("Beginning of book");
  });

  it("flashes the end-stop only where there is nothing to hand off to", () => {
    const { boundary, sent } = setup({ hasNextChapter: () => false });
    boundary.flashEdge("start");
    expect(pill("top").style.opacity).toBe("0");
    boundary.flashEdge("end");
    expect(pill("bottom").style.opacity).toBe("0.8");
    expect(label("bottom")).toBe("End of book");
    vi.advanceTimersByTime(EDGE_FLASH_MS + 1);
    expect(pill("bottom").style.opacity).toBe("0");
    expect(label("bottom")).toBe("");
    expect(sent).toHaveLength(0);
  });

  it("slides along the pull axis of a vertical chapter", () => {
    const { boundary } = setup({ getWritingMode: () => "rl" });
    boundary.processTouch("end", TOUCH_THRESHOLD / 2);
    // vertical-rl advances leftward, so the "next" pill enters from the left.
    expect(pill("bottom").style.transform).toBe("translateX(-20px)");
  });

  it("slides along Y for a horizontal chapter", () => {
    const { boundary } = setup();
    boundary.processTouch("end", TOUCH_THRESHOLD / 2);
    expect(pill("bottom").style.transform).toBe("translateY(20px)");
  });

  it("honours reduced motion by fading without the slide", () => {
    const original = window.matchMedia;
    window.matchMedia = ((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
    try {
      const { boundary } = setup();
      boundary.processTouch("end", TOUCH_THRESHOLD / 2);
      expect(pill("bottom").style.transform).toBe("translateY(0px)");
      expect(Number(pill("bottom").style.opacity)).toBeGreaterThan(0);
    } finally {
      window.matchMedia = original;
    }
  });

  it("removes the pills and cancels pending timers on dispose", () => {
    const { boundary, sent } = setup();
    boundary.accumulate(100, "end", WHEEL_THRESHOLD);
    boundary.dispose();
    expect(document.querySelectorAll(".boundary-indicator")).toHaveLength(0);
    expect(() => vi.advanceTimersByTime(BOUNDARY_RESET_MS * 2)).not.toThrow();
    expect(sent).toHaveLength(0);
  });
});
