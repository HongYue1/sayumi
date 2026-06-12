import type { ChapterData } from "~/api/client";
import type { IframeSettings } from "~/lib/settings.svelte";

export interface KeyEvent {
  key: string;
  code: string;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  metaKey: boolean;
}

/** Imperative handle the ChapterFrame exposes to its parent (ReaderView). */
export interface ChapterFrameAPI {
  loadChapter: (
    data: ChapterData,
    settings: IframeSettings,
    scrollTo?: "top" | "end",
    fragment?: string,
    hasPrev?: boolean,
    hasNext?: boolean,
    restorePercent?: number,
    restoreCfi?: string,
  ) => void;
  applySettings: (settings: IframeSettings) => void;
  scrollTo: (percent: number) => void;
  scrollToEnd: () => void;
  scrollToFragment: (id: string) => void;
  scrollToCfi: (cfi: string) => void;
  requestPosition: () => void;
  nextPage: () => void;
  prevPage: () => void;
  goToPage: (page: number) => void;
  goToLastPage: () => void;
  highlightSearch: (charOffset: number, matchLen: number, query: string) => void;
  clearHighlights: () => void;
  /** Replace the reader @font-face CSS (embedded + user fonts) in the iframe. */
  setFontFaces: (css: string) => void;
}
