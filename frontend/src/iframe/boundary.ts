import type { FrameToParentMessage } from "~/lib/frameMessages";

/**
 * Pull-past-the-edge "previous/next chapter" affordance.
 *
 * INVARIANTS
 * - One hand-off per gesture. A touch pull latches on send and re-arms only on
 *   endGesture() (finger lift, cancel, or a fresh touchstart) -- never on the
 *   hide timer. The parent debounces boundaries too (Read.tsx
 *   BOUNDARY_COOLDOWN_MS 400 / POST_SWAP_BOUNDARY_GRACE_MS 650), but that is a
 *   second line of defence: an emitter that re-arms faster than its consumer
 *   walks the reader through chapters on one sustained drag.
 * - Every visual write goes through applySide(), which diffs against the last
 *   applied state per element. Both pull handlers run once per animation frame,
 *   so unconditional writes dirtied two fixed-position elements every frame of
 *   every gesture.
 * - The indicators are decoration: aria-hidden, textContent only (book content
 *   is untrusted), and cleared on reset so no stale label lingers in the
 *   accessibility tree.
 * - Axis follows the writing mode. A vertical chapter's pull is a horizontal
 *   drag (see frame.ts flushTouchPull), so the pills sit on the inline edges
 *   and slide along X; frame.css mirrors this via the html.vertical-* classes.
 */

/** Touch pull distance, in px, that hands off to the adjacent chapter. */
export const TOUCH_THRESHOLD = 200;
/** A pull with no further movement is abandoned after this long. */
export const BOUNDARY_RESET_MS = 600;
/**
 * How long the pill stays up after a hand-off is emitted. It hides the pill
 * only: the gesture stays latched until endGesture().
 */
export const BOUNDARY_SENT_HIDE_MS = 300;
/**
 * Travel that fills the end-stop at the first/last chapter. The pull threshold
 * can't be reused there -- there is no hand-off to earn, and the wheel
 * threshold (600) would leave the end-stop invisible for a whole second.
 */
export const EDGE_FULL_TRAVEL = 120;
/** How long a keyboard/paged end-stop flash stays up (no gesture to track). */
export const EDGE_FLASH_MS = 700;
/** Distance the pill slides in from its screen edge. */
export const INDICATOR_SLIDE_PX = 40;

type AtBoundaryMessage = Extract<FrameToParentMessage, { type: "at-boundary" }>;

export type BoundaryDirection = "start" | "end";
type IndicatorDirection = BoundaryDirection | "edge-start" | "edge-end";
export type IndicatorSide = "top" | "bottom";

/** Resolved visual state for the pair of indicators. Pure, hence testable. */
export interface IndicatorVisual {
  side: IndicatorSide | null;
  label: string;
  edge: boolean;
  opacity: number;
  offset: number;
}

export interface BoundaryDeps {
  sendMessage: (msg: AtBoundaryMessage) => void;
  getActiveSeq: () => number;
  hasPrevChapter: () => boolean;
  hasNextChapter: () => boolean;
  /** "" for horizontal chapters; "rl"/"lr" put the pull on the X axis. */
  getWritingMode: () => "" | "rl" | "lr";
}

export interface BoundaryController {
  /** Lazily create the two indicator elements (once content is in the DOM). */
  ensureElements(): void;
  /** Accumulate a wheel gesture past an edge; emits at-boundary at `threshold`. */
  accumulate(
    delta: number,
    direction: BoundaryDirection,
    threshold: number,
  ): void;
  /** Touch equivalent, driven by absolute pull distance from the edge. */
  processTouch(direction: BoundaryDirection, pullDistance: number): void;
  /**
   * Show the end-stop for a hand-off that cannot happen (first/last chapter),
   * for callers with no pull to track: keyboard paging and paged turns.
   * No-ops when an adjacent chapter exists.
   */
  flashEdge(direction: BoundaryDirection): void;
  /** Clear accumulation + hide the indicators. Does NOT clear the gesture latch. */
  reset(): void;
  /** Re-arm the one-hand-off-per-gesture latch (touchstart/touchend/touchcancel). */
  endGesture(): void;
  /** True while a pull or end-stop is on screen (used by the scroll/wheel handlers). */
  hasActiveDirection(): boolean;
  /** True once at-boundary has been emitted for the current pull. */
  isSent(): boolean;
  /** Remove the indicators and clear every timer (call from frame teardown). */
  dispose(): void;
}

function round2(n: number): number {
  return Math.round(n * 100) / 100;
}

function roundHalf(n: number): number {
  return Math.round(n * 2) / 2;
}

/**
 * Resolve the indicator state for a direction + progress. Quantised so a pull
 * that barely moves produces an identical result and applySide can skip the
 * write; unrounded values made every frame a new sub-pixel transform.
 */
export function indicatorVisual(
  dir: IndicatorDirection | null,
  progress: number,
  reduceMotion: boolean,
): IndicatorVisual {
  if (!dir) {
    return {
      side: null,
      label: "",
      edge: false,
      opacity: 0,
      offset: INDICATOR_SLIDE_PX,
    };
  }
  const p = Number.isFinite(progress) ? Math.min(1, Math.max(0, progress)) : 0;
  const edge = dir === "edge-start" || dir === "edge-end";
  const side: IndicatorSide =
    dir === "start" || dir === "edge-start" ? "top" : "bottom";
  const label =
    dir === "start"
      ? "Previous chapter"
      : dir === "edge-start"
        ? "Beginning of book"
        : dir === "end"
          ? "Next chapter"
          : "End of book";
  return {
    side,
    label,
    edge,
    // An end-stop is a muted acknowledgement, a real pull ramps to full early
    // so the affordance reads before the threshold is reached.
    opacity: round2(edge ? Math.min(0.8, p) : Math.min(1, p * 1.5)),
    offset: reduceMotion ? 0 : roundHalf(INDICATOR_SLIDE_PX * (1 - p)),
  };
}

export function createBoundary(deps: BoundaryDeps): BoundaryController {
  let topEl: HTMLElement | null = null;
  let bottomEl: HTMLElement | null = null;
  let topLabel: HTMLElement | null = null;
  let bottomLabel: HTMLElement | null = null;

  let accum = 0;
  let direction: BoundaryDirection | null = null;
  let resetTimer: ReturnType<typeof setTimeout> | null = null;
  let fireTimer: ReturnType<typeof setTimeout> | null = null;
  let flashTimer: ReturnType<typeof setTimeout> | null = null;
  let sent = false;
  let gestureLatched = false;
  let appliedTop = "";
  let appliedBottom = "";
  let reduceMotionQuery: MediaQueryList | null = null;

  // Cached per controller, and guarded so the test DOM (no matchMedia) is safe.
  function prefersReducedMotion(): boolean {
    if (typeof window.matchMedia !== "function") return false;
    reduceMotionQuery ??= window.matchMedia("(prefers-reduced-motion: reduce)");
    return reduceMotionQuery.matches;
  }

  function createIndicator(
    id: string,
    modifier: string,
  ): { el: HTMLElement; label: HTMLElement } {
    const el = document.createElement("div");
    el.id = id;
    el.className = `boundary-indicator ${modifier}`;
    // Decoration only: the pill duplicates an affordance the parent already
    // announces, so it must not reach the book's accessibility tree.
    el.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.className = "boundary-label";
    el.appendChild(label);
    document.body.appendChild(el);
    return { el, label };
  }

  function ensureElements(): void {
    if (!topEl) {
      const made = createIndicator("boundary-indicator-top", "boundary-top");
      topEl = made.el;
      topLabel = made.label;
    }
    if (!bottomEl) {
      const made = createIndicator(
        "boundary-indicator-bottom",
        "boundary-bottom",
      );
      bottomEl = made.el;
      bottomLabel = made.label;
    }
  }

  // Which way the pill slides in from. Vertical chapters pull along X, and
  // "start" sits on the side the reader came from: the right edge in
  // vertical-rl, the left in vertical-lr.
  function slideSign(side: IndicatorSide): number {
    const wm = deps.getWritingMode();
    if (wm === "rl") return side === "top" ? 1 : -1;
    return side === "top" ? -1 : 1;
  }

  function applySide(
    el: HTMLElement | null,
    labelEl: HTMLElement | null,
    side: IndicatorSide,
    visual: IndicatorVisual,
  ): void {
    if (!el || !labelEl) return;
    const active = visual.side === side;
    const opacity = active ? visual.opacity : 0;
    const offset = active ? visual.offset : INDICATOR_SLIDE_PX;
    const label = active ? visual.label : "";
    const edge = active && visual.edge;
    const axis = deps.getWritingMode() ? "X" : "Y";
    const key = `${axis}|${opacity}|${offset}|${label}|${edge}`;
    if (side === "top") {
      if (key === appliedTop) return;
      appliedTop = key;
    } else {
      if (key === appliedBottom) return;
      appliedBottom = key;
    }
    labelEl.textContent = label;
    el.classList.toggle("edge", edge);
    el.style.opacity = String(opacity);
    el.style.transform = `translate${axis}(${offset * slideSign(side)}px)`;
  }

  function show(dir: IndicatorDirection | null, progress: number): void {
    const visual = indicatorVisual(dir, progress, prefersReducedMotion());
    applySide(topEl, topLabel, "top", visual);
    applySide(bottomEl, bottomLabel, "bottom", visual);
  }

  function stopTimers(): void {
    if (resetTimer) {
      clearTimeout(resetTimer);
      resetTimer = null;
    }
    if (fireTimer) {
      clearTimeout(fireTimer);
      fireTimer = null;
    }
    if (flashTimer) {
      clearTimeout(flashTimer);
      flashTimer = null;
    }
  }

  function reset(): void {
    accum = 0;
    direction = null;
    sent = false;
    stopTimers();
    show(null, 0);
  }

  function scheduleReset(): void {
    if (resetTimer) clearTimeout(resetTimer);
    resetTimer = setTimeout(reset, BOUNDARY_RESET_MS);
  }

  function hasAdjacent(dir: BoundaryDirection): boolean {
    return dir === "start" ? deps.hasPrevChapter() : deps.hasNextChapter();
  }

  function edgeDirection(dir: BoundaryDirection): IndicatorDirection {
    return dir === "start" ? "edge-start" : "edge-end";
  }

  function fire(dir: BoundaryDirection): void {
    sent = true;
    show(dir, 1);
    deps.sendMessage({
      type: "at-boundary",
      seq: deps.getActiveSeq(),
      boundary: dir,
    });
    if (fireTimer) clearTimeout(fireTimer);
    fireTimer = setTimeout(reset, BOUNDARY_SENT_HIDE_MS);
  }

  function accumulate(
    delta: number,
    dir: BoundaryDirection,
    threshold: number,
  ): void {
    if (sent) return;
    if (direction !== dir) {
      accum = 0;
      direction = dir;
    }
    accum += Math.abs(delta);
    scheduleReset();
    // The end-stop accumulates like a pull instead of tracking the current
    // frame's delta: a trackpad emits a few px per frame, which rendered the
    // "Beginning/End of book" pill as an invisible flicker 30px off-screen.
    if (!hasAdjacent(dir)) {
      show(edgeDirection(dir), accum / EDGE_FULL_TRAVEL);
      return;
    }
    show(dir, threshold > 0 ? accum / threshold : 1);
    if (threshold > 0 && accum >= threshold) fire(dir);
  }

  function processTouch(dir: BoundaryDirection, pullDistance: number): void {
    if (sent || gestureLatched) return;
    direction = dir;
    accum = Math.max(0, pullDistance);
    scheduleReset();
    if (!hasAdjacent(dir)) {
      show(edgeDirection(dir), accum / EDGE_FULL_TRAVEL);
      return;
    }
    show(dir, accum / TOUCH_THRESHOLD);
    if (accum >= TOUCH_THRESHOLD) {
      // Latched for the rest of the gesture: the hide timer below clears
      // `sent` after 300ms, and the caller measures absolute pull distance, so
      // a finger still held past the threshold would hand off again and again.
      gestureLatched = true;
      fire(dir);
    }
  }

  function flashEdge(dir: BoundaryDirection): void {
    if (hasAdjacent(dir)) return;
    ensureElements();
    accum = 0;
    sent = false;
    direction = dir;
    stopTimers();
    show(edgeDirection(dir), 1);
    flashTimer = setTimeout(reset, EDGE_FLASH_MS);
  }

  function dispose(): void {
    stopTimers();
    topEl?.remove();
    bottomEl?.remove();
    topEl = null;
    bottomEl = null;
    topLabel = null;
    bottomLabel = null;
    appliedTop = "";
    appliedBottom = "";
    accum = 0;
    direction = null;
    sent = false;
    gestureLatched = false;
  }

  return {
    ensureElements,
    accumulate,
    processTouch,
    flashEdge,
    reset,
    endGesture() {
      gestureLatched = false;
    },
    hasActiveDirection: () => direction !== null,
    isSent: () => sent,
    dispose,
  };
}
