import type { ChapterData } from "~/api/client";
import type { FrameKeyMessage } from "~/lib/frameMessages";
import type { IframeSettings } from "~/lib/settings";

/** The key payload frame.ts forwards, minus the transport fields the parent
 *  consumes before the callback runs. Derived from FrameKeyMessage rather than
 *  restated: a hand-written twin let a new wire field compile at all three
 *  frame.ts send sites and then be dropped in silence by ChapterFrame's
 *  dispatch, which rebuilds this object field by field. Deriving it makes that
 *  dispatch the one site that fails to compile. */
export type KeyEvent = Omit<FrameKeyMessage, "type" | "seq">;

/** Imperative handle the ChapterFrame exposes to its parent (routes/Read.tsx). */
export interface ChapterFrameAPI {
  loadChapter: (
    data: ChapterData,
    settings: IframeSettings,
    scrollTo?: "top" | "end",
    fragment?: string,
    // The wire declares these the other way round (hasNext then hasPrev, see
    // LoadMessage in frameMessages.ts). They are the only adjacent same-typed
    // pair here and no test pins the mapping, so a transposition would reach
    // the frame silently: ChapterFrame must keep passing them through by name.
    hasPrev?: boolean,
    hasNext?: boolean,
    restorePercent?: number,
    restoreCfi?: string,
    language?: string,
  ) => void;
  applySettings: (settings: IframeSettings) => void;
  scrollTo: (percent: number) => void;
  scrollToEnd: () => void;
  scrollToFragment: (id: string) => void;
  scrollToCfi: (cfi: string) => void;
  requestPosition: () => void;
  nextPage: () => void;
  prevPage: () => void;
  /** 1-based page number; the frame converts to pagination's 0-based index. */
  goToPage: (page: number) => void;
  goToLastPage: () => void;
  highlightSearch: (
    charOffset: number,
    matchLen: number,
    query: string,
    /** Seq of the chapter load this highlight was computed for. When set, the
     *  iframe drops the highlight if a newer chapter has since loaded. */
    forSeq?: number,
  ) => void;
  clearHighlights: () => void;
  /** Replace the reader @font-face CSS (embedded + user fonts) in the iframe. */
  setFontFaces: (css: string) => void;
}
