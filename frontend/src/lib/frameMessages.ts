// Single source of truth for the parent <-> reader-iframe postMessage protocol.
//
// frame.ts is compiled standalone into the srcdoc sandbox, so it can only
// share *types* with the parent (type-only imports are erased at build time).
// Keeping both directions here lets the parent (ChapterFrame.tsx) and the frame
// engine (frame.ts) stay in lockstep.
//
// This file declares types only: it must stay free of runtime code so both
// bundles can import it with `import type` and erase it completely.
//
// Exhaustiveness is enforced at the switch sites, not here:
//   - parent -> frame: the message switch in frame.ts ends in a `never` guard.
//   - frame -> parent: the dispatch switch in ChapterFrame.tsx ends in one too.
// A new frame -> parent kind also needs a validator case in that file's
// `isInbound` runtime guard. The compile error at the dispatch switch is what
// sends you there, since an unvalidated kind is dropped silently at runtime.

import type { IframeSettings } from "~/lib/settings";

export type { IframeSettings };

/** Reading direction. Mirrors the Go enum: internal/epub/parser.go emits
 *  exactly "ltr" or "rtl", defaulting to "ltr" -- never a raw OPF string. */
export type ReadingDirection = "ltr" | "rtl";

/** Chapter writing mode. Mirrors the Go enum: internal/epub/chapter.go starts
 *  from "horizontal-tb" and only ever overrides it with a vertical value. */
export type WritingMode = "horizontal-tb" | "vertical-rl" | "vertical-lr";

/** Chapter payload the parent sends to render content inside the frame. */
export interface LoadMessage {
  type: "load";
  seq: number;
  chapterIndex: number;
  // These come straight from ChapterData, which the API client types as
  // required. Optional here would only invite defensive reads for a shape no
  // producer can build.
  css: string;
  fontFaceCSS: string;
  direction: ReadingDirection;
  writingMode: WritingMode;
  html: string;
  /** BCP-47 tag from the book metadata; absent when the book declares none. */
  language?: string;
  // Base URL the frame resolves relative resource links against. Sent by the
  // parent from ChapterData.resourceBase; null when the chapter carries none.
  resourceBase: string | null;
  scrollTo: "top" | "end";
  fragment: string | null;
  hasNext: boolean;
  hasPrev: boolean;
  restorePercent: number | null;
  restoreCfi: string | null;
  // Belt-and-braces only. frame.ts pins the parent origin from the first
  // message it accepts, and that is always the set-font-faces sent on "ready",
  // so a load is already origin-checked before this field is read. Kept
  // because it documents the expectation; optional because nothing depends on
  // it being present.
  origin?: string;
}

/** Messages sent parent -> frame (posted into the iframe window). */
export type ParentToFrameMessage =
  | { type: "destroy" }
  | { type: "set-font-faces"; fontFaces: string }
  | LoadMessage
  | { type: "apply-settings"; settings: IframeSettings }
  // Positional commands carry the seq of the chapter load they were computed
  // against; the parent stamps the live seq and frame.ts drops anything older
  // than the committed chapter (isStaleCommand). Without it a scroll or page
  // command aimed at chapter N still lands after a fast turn and moves
  // chapter N+1 instead. highlight-search already worked this way; these are
  // the rest of the same family.
  | { type: "scroll-to"; seq: number; percent: number }
  | { type: "scroll-to-end"; seq: number }
  | { type: "next-page"; seq: number }
  | { type: "prev-page"; seq: number }
  // page is 1-based on the wire; frame.ts converts to pagination's 0-based
  // index. Keep the bases distinct: pagination.goToLastPage() is totalPages-1.
  | { type: "go-to-page"; seq: number; page: number }
  | { type: "go-to-last-page"; seq: number }
  | { type: "scroll-to-fragment"; seq: number; id: string }
  | { type: "scroll-to-cfi"; seq: number; cfi: string }
  // A query, not a positional command: the reply carries the frame's own
  // activeSeq and the parent drops it when that no longer matches.
  | { type: "get-position" }
  | {
      type: "highlight-search";
      seq: number;
      charOffset: number;
      matchLen: number;
      query: string;
    }
  | { type: "clear-highlights" }
  // The reader iframe is sandboxed without allow-same-origin, so the browser
  // gives it an opaque origin and composites it as its own surface. A zoom
  // gesture rescales that surface on the compositor without asking the frame to
  // redraw, so its text stays a stretched bitmap. The parent sends this once the
  // gesture settles, to make the frame repaint at the new scale.
  | { type: "refresh-raster" };

/** Key event forwarded from inside the frame up to the parent. */
export interface FrameKeyMessage {
  type: "key";
  seq: number;
  key: string;
  code: string;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  metaKey: boolean;
}

/** Messages sent frame -> parent. */
export type FrameToParentMessage =
  | { type: "ready" }
  | { type: "loaded"; seq: number }
  | {
      type: "position";
      seq: number;
      chapterIndex: number;
      percent: number;
      cfi?: string | null;
    }
  | { type: "at-boundary"; seq: number; boundary: "start" | "end" }
  | { type: "link-clicked"; seq: number; href: string }
  | FrameKeyMessage
  | { type: "click"; seq: number; region: "left" | "center" | "right" }
  | { type: "load-error"; seq: number; error: string };
