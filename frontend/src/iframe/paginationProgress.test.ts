import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FrameToParentMessage } from "~/lib/frameMessages";
import { createPagination, type PaginationController } from "./pagination";

const layouts = [
  { mode: "paged", gap: 0, rtl: false },
  { mode: "paged", gap: 0, rtl: true },
  { mode: "paged-two", gap: 1, rtl: false },
  { mode: "paged-two", gap: 1, rtl: true },
];

describe.each(layouts)("paged progress: $mode, rtl=$rtl", ({ gap, rtl }) => {
  const width = 800;
  const stride = width + gap;
  const sign = rtl ? -1 : 1;
  let content: HTMLElement;
  let pagination: PaginationController;
  let sent: FrameToParentMessage[];
  let seq: number;

  function lastPosition() {
    const position = sent.findLast((message) => message.type === "position");
    if (!position || position.type !== "position") {
      throw new Error("expected a position report");
    }
    return position;
  }

  function rectAt(logicalX: number, rectWidth = 20): DOMRect {
    const scroll = Math.abs(content.scrollLeft);
    const left = rtl
      ? width - logicalX + scroll - rectWidth
      : logicalX - scroll;
    return new DOMRect(left, 40, rectWidth, 20);
  }

  beforeEach(() => {
    vi.useFakeTimers();
    content = document.createElement("div");
    document.body.append(content);
    content.style.columnGap = `${gap}px`;
    Object.defineProperties(content, {
      clientWidth: { value: width, configurable: true },
      scrollWidth: { value: width + stride * 3, configurable: true },
    });
    content.getBoundingClientRect = () => new DOMRect(0, 24, width, 600);
    Object.defineProperty(document, "fonts", {
      configurable: true,
      value: { ready: Promise.resolve() },
    });
    vi.spyOn(window, "matchMedia").mockReturnValue({
      matches: false,
    } as MediaQueryList);
    sent = [];
    seq = 7;
    pagination = createPagination({
      getContentEl: () => content,
      getClipEl: () => null,
      sendMessage: (message) => sent.push(message),
      getActiveSeq: () => seq,
      getActiveChapterIndex: () => 3,
      isDestroyed: () => false,
      isContentReady: () => true,
      isRestorePending: () => false,
      // Deliberately derived from the rendered page, not the turn target.
      getPositionCfi: () =>
        `cfi:${Math.round(Math.abs(content.scrollLeft) / stride) + 1}`,
      isPagedMode: () => true,
      hasNextChapter: () => false,
      hasPrevChapter: () => false,
      setChapterHidden: () => {},
      ensureBoundaryElements: () => {},
      flashBoundaryEdge: () => {},
      updateBoundaryState: () => {},
      takePendingFragment: () => null,
    });
    pagination.resetForLoad(rtl);
  });

  afterEach(() => {
    pagination.dispose();
    expect(vi.getTimerCount()).toBe(0);
    vi.useRealTimers();
    vi.restoreAllMocks();
    document.body.innerHTML = "";
    document.documentElement.className = "";
    Reflect.deleteProperty(document, "fonts");
  });

  it("never pairs the new percent with the outgoing page's anchor", async () => {
    pagination.enterPagedFromScroll(null, 1 / 3);
    sent.length = 0;
    pagination.nextPage();
    expect(content.scrollLeft).toBe(sign * stride);
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "" });

    // Escape requests a fresh report even if the fade has not swapped yet.
    pagination.reportPagePosition();
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "" });
    await vi.advanceTimersByTimeAsync(500);
    expect(content.scrollLeft).toBe(sign * stride * 2);
    // No extra get-position request: completing the turn must refresh CFI.
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "cfi:3" });
    expect(
      sent.every(
        (message) => message.type !== "position" || message.cfi !== "cfi:2",
      ),
    ).toBe(true);
  });

  it("reports only the latest target after rapid retargets", async () => {
    pagination.enterPagedFromScroll(null, 0);
    pagination.nextPage();
    await vi.advanceTimersByTimeAsync(120);
    pagination.nextPage();
    pagination.nextPage();
    pagination.prevPage();
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "" });
    await vi.advanceTimersByTimeAsync(500);
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "cfi:3" });
    expect(content.scrollLeft).toBe(sign * stride * 2);
  });

  it("reports the current anchor immediately with reduced motion", () => {
    vi.mocked(window.matchMedia).mockReturnValue({
      matches: true,
    } as MediaQueryList);
    pagination.enterPagedFromScroll(null, 1 / 3);
    pagination.nextPage();
    expect(content.scrollLeft).toBe(sign * stride * 2);
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "cfi:3" });
  });

  it.each(["restore", "mode switch"])(
    "keeps the text offset after a %s and font relayout",
    async (entry) => {
      const anchor = document.createElement("p");
      anchor.textContent = "A paragraph spanning several pages.";
      content.append(anchor);
      anchor.getBoundingClientRect = () => rectAt(stride + 10, stride * 2);
      const range = document.createRange();
      range.setStart(anchor.firstChild!, 12);
      range.collapse(true);
      let logicalX = stride * 2 + 10;
      range.getBoundingClientRect = () => rectAt(logicalX);

      if (entry === "restore") {
        pagination.restorePagedPosition("top", 2 / 3, anchor, range);
      } else {
        pagination.enterPagedFromScroll(anchor, 2 / 3, range);
        pagination.relayout();
      }
      expect(content.scrollLeft).toBe(sign * stride * 2);
      await vi.advanceTimersByTimeAsync(500);
      // The post-reveal fonts.ready pass used to drop the range and go back
      // to the beginning of this paragraph (page 2 instead of page 3).
      expect(content.scrollLeft).toBe(sign * stride * 2);

      Object.defineProperty(content, "scrollWidth", {
        value: width + stride * 5,
        configurable: true,
      });
      logicalX = stride * 3 + 10;
      pagination.relayout();
      await vi.advanceTimersByTimeAsync(500);
      expect(content.scrollLeft).toBe(sign * stride * 3);

      // An explicit turn drops both the element and its intra-block offset.
      pagination.nextPage();
      await vi.advanceTimersByTimeAsync(500);
      pagination.relayout();
      await vi.advanceTimersByTimeAsync(500);
      expect(content.scrollLeft).toBe(sign * stride * 4);
    },
  );

  it("does not publish an old animation after the chapter changes", async () => {
    pagination.enterPagedFromScroll(null, 1 / 3);
    pagination.nextPage();
    seq++;
    pagination.resetForLoad(rtl);
    sent.length = 0;
    await vi.advanceTimersByTimeAsync(500);
    expect(sent).toEqual([]);
  });
});
