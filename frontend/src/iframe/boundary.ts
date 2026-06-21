import type { FrameToParentMessage } from "~/lib/frameMessages";

// Pull-past-the-edge "next/previous chapter" affordance. WHEEL_THRESHOLD stays
// in frame.ts because the wheel handler passes it into accumulate() per gesture;
// these two are only used internally here.
const TOUCH_THRESHOLD = 200;
const BOUNDARY_RESET_MS = 600;

export interface BoundaryDeps {
  sendMessage: (msg: FrameToParentMessage) => void;
  getActiveSeq: () => number;
  hasPrevChapter: () => boolean;
  hasNextChapter: () => boolean;
}

export interface BoundaryController {
  /** Lazily create the top/bottom indicator elements (once content is in the DOM). */
  ensureElements(): void;
  /** Accumulate a wheel gesture past an edge; emits `at-boundary` once `threshold` is reached. */
  accumulate(
    delta: number,
    direction: "start" | "end",
    threshold: number,
  ): void;
  /** Touch equivalent of `accumulate`, driven by absolute pull distance. */
  processTouch(
    direction: "start" | "end",
    hasAdjacentChapter: boolean,
    boundaryDelta: number,
  ): void;
  /** Clear accumulation + hide the indicators. */
  reset(): void;
  /** True while a pull is in progress (used by the scroll/wheel handlers). */
  hasActiveDirection(): boolean;
  /** True once `at-boundary` has been emitted for the current pull. */
  isSent(): boolean;
  /** Clear pending reset/fire timers (call from frame teardown). */
  disposeTimers(): void;
}

export function createBoundary(deps: BoundaryDeps): BoundaryController {
  let _boundaryTop: HTMLElement | null = null;
  let _boundaryBottom: HTMLElement | null = null;
  let _boundaryTopLabel: HTMLElement | null = null;
  let _boundaryBottomLabel: HTMLElement | null = null;

  let boundaryAccum = 0;
  let boundaryDirection: "start" | "end" | null = null;
  let boundaryResetTimer: ReturnType<typeof setTimeout> | null = null;
  let boundaryFireTimer: ReturnType<typeof setTimeout> | null = null;
  let boundarySent = false;

  function ensureElements(): void {
    if (!_boundaryTop) {
      const el = document.createElement("div");
      el.id = "boundary-indicator-top";
      el.className = "boundary-indicator boundary-top";
      const span = document.createElement("span");
      span.className = "boundary-label";
      el.appendChild(span);
      document.body.appendChild(el);
      _boundaryTop = el;
      _boundaryTopLabel = span;
    }
    if (!_boundaryBottom) {
      const el = document.createElement("div");
      el.id = "boundary-indicator-bottom";
      el.className = "boundary-indicator boundary-bottom";
      const span = document.createElement("span");
      span.className = "boundary-label";
      el.appendChild(span);
      document.body.appendChild(el);
      _boundaryBottom = el;
      _boundaryBottomLabel = span;
    }
  }

  function updateBoundaryIndicator(
    dir: "start" | "end" | "edge-start" | "edge-end" | null,
    progress: number,
  ): void {
    const topEl = _boundaryTop;
    const bottomEl = _boundaryBottom;
    if (!topEl || !bottomEl) return;

    // _boundaryTopLabel and _boundaryBottomLabel are always co-assigned with
    // _boundaryTop/_boundaryBottom inside ensureElements, so if the elements
    // exist the labels exist too. Explicit checks make this invariant
    // self-evident instead of relying on non-null assertions.
    const topLabel = _boundaryTopLabel;
    const bottomLabel = _boundaryBottomLabel;
    if (!topLabel || !bottomLabel) return;

    topEl.style.opacity = "0";
    topEl.style.transform = "translateY(-40px)";
    topEl.classList.remove("edge");
    bottomEl.style.opacity = "0";
    bottomEl.style.transform = "translateY(40px)";
    bottomEl.classList.remove("edge");

    if (dir === "start" || dir === "edge-start") {
      topLabel.textContent =
        dir === "start" ? "Previous chapter" : "Beginning of book";
      if (dir === "edge-start") topEl.classList.add("edge");
      topEl.style.opacity = String(
        dir === "edge-start"
          ? Math.min(0.8, progress)
          : Math.min(1, progress * 1.5),
      );
      topEl.style.transform = `translateY(${-40 + progress * 40}px)`;
    } else if (dir === "end" || dir === "edge-end") {
      bottomLabel.textContent = dir === "end" ? "Next chapter" : "End of book";
      if (dir === "edge-end") bottomEl.classList.add("edge");
      bottomEl.style.opacity = String(
        dir === "edge-end"
          ? Math.min(0.8, progress)
          : Math.min(1, progress * 1.5),
      );
      bottomEl.style.transform = `translateY(${40 - progress * 40}px)`;
    }
  }

  function reset(): void {
    boundaryAccum = 0;
    boundaryDirection = null;
    boundarySent = false;
    if (boundaryResetTimer) {
      clearTimeout(boundaryResetTimer);
      boundaryResetTimer = null;
    }
    if (boundaryFireTimer) {
      clearTimeout(boundaryFireTimer);
      boundaryFireTimer = null;
    }
    updateBoundaryIndicator(null, 0);
  }

  function scheduleBoundaryReset(): void {
    if (boundaryResetTimer) clearTimeout(boundaryResetTimer);
    boundaryResetTimer = setTimeout(reset, BOUNDARY_RESET_MS);
  }

  function accumulate(
    delta: number,
    direction: "start" | "end",
    threshold: number,
  ): void {
    if (boundarySent) return;
    if (direction === "start" && !deps.hasPrevChapter()) {
      updateBoundaryIndicator("edge-start", Math.min(1, Math.abs(delta) / 40));
      scheduleBoundaryReset();
      return;
    }
    if (direction === "end" && !deps.hasNextChapter()) {
      updateBoundaryIndicator("edge-end", Math.min(1, Math.abs(delta) / 40));
      scheduleBoundaryReset();
      return;
    }
    if (boundaryDirection !== direction) {
      boundaryAccum = 0;
      boundaryDirection = direction;
    }
    boundaryAccum += Math.abs(delta);
    scheduleBoundaryReset();
    const progress = Math.min(1, boundaryAccum / threshold);
    updateBoundaryIndicator(direction, progress);
    if (boundaryAccum >= threshold) {
      boundarySent = true;
      updateBoundaryIndicator(direction, 1);
      deps.sendMessage({
        type: "at-boundary",
        seq: deps.getActiveSeq(),
        boundary: direction,
      });
      if (boundaryFireTimer) clearTimeout(boundaryFireTimer);
      boundaryFireTimer = setTimeout(reset, 300);
    }
  }

  function processTouch(
    direction: "start" | "end",
    hasAdjacentChapter: boolean,
    boundaryDelta: number,
  ): void {
    if (boundaryDirection !== direction) {
      boundaryAccum = 0;
      boundaryDirection = direction;
    }
    boundaryAccum = boundaryDelta;
    scheduleBoundaryReset();
    if (!hasAdjacentChapter) {
      updateBoundaryIndicator(
        direction === "end" ? "edge-end" : "edge-start",
        Math.min(1, boundaryDelta / 40),
      );
      return;
    }
    updateBoundaryIndicator(
      direction,
      Math.min(1, boundaryAccum / TOUCH_THRESHOLD),
    );
    if (boundaryAccum >= TOUCH_THRESHOLD && !boundarySent) {
      boundarySent = true;
      updateBoundaryIndicator(direction, 1);
      deps.sendMessage({
        type: "at-boundary",
        seq: deps.getActiveSeq(),
        boundary: direction,
      });
      if (boundaryFireTimer) clearTimeout(boundaryFireTimer);
      boundaryFireTimer = setTimeout(reset, 300);
    }
  }

  return {
    ensureElements,
    accumulate,
    processTouch,
    reset,
    hasActiveDirection: () => boundaryDirection !== null,
    isSent: () => boundarySent,
    disposeTimers() {
      if (boundaryResetTimer) {
        clearTimeout(boundaryResetTimer);
        boundaryResetTimer = null;
      }
      if (boundaryFireTimer) {
        clearTimeout(boundaryFireTimer);
        boundaryFireTimer = null;
      }
    },
  };
}
