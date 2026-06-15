// Single source of truth for the parent <-> reader-iframe postMessage protocol.
//
// frame.ts is compiled standalone into the srcdoc sandbox, so it can only
// share *types* with the parent (type-only imports are erased at build time).
// Keeping both directions here lets the parent (ChapterFrame.svelte) and the
// frame engine (frame.ts) stay in lockstep: adding a message kind that only one
// side handles becomes a compile error via the exhaustive switches below.

import type { IframeSettings } from "~/lib/settings.svelte";

export type { IframeSettings };

/** Chapter payload the parent sends to render content inside the frame. */
export interface LoadMessage {
  type: "load";
  seq: number;
  chapterIndex: number;
  css?: string;
  fontFaceCSS?: string;
  direction?: string;
  writingMode?: string;
  language?: string;
  html?: string;
  // Base URL the frame resolves relative resource links against. Sent by the
  // parent from ChapterData.resourceBase; optional because not every chapter
  // carries one.
  resourceBase?: string | null;
  scrollTo?: "top" | "end";
  fragment?: string | null;
  hasNext?: boolean;
  hasPrev?: boolean;
  restorePercent?: number | null;
  restoreCfi?: string | null;
  origin?: string;
}

/** Messages sent parent -> frame (posted into the iframe window). */
export type ParentToFrameMessage =
  | { type: "destroy" }
  | { type: "set-font-faces"; fontFaces?: string }
  | LoadMessage
  | { type: "apply-settings"; settings: IframeSettings }
  | { type: "scroll-to"; percent: number }
  | { type: "scroll-to-end" }
  | { type: "next-page" }
  | { type: "prev-page" }
  | { type: "go-to-page"; page?: number }
  | { type: "go-to-last-page" }
  | { type: "scroll-to-fragment"; id?: string }
  | { type: "scroll-to-cfi"; cfi?: string }
  | { type: "get-position" }
  | {
      type: "highlight-search";
      seq?: number;
      charOffset?: number;
      matchLen?: number;
      query?: unknown;
    }
  | { type: "clear-highlights" };

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
  | { type: "link-clicked"; href: string }
  | FrameKeyMessage
  | { type: "click"; seq: number; region: "left" | "center" | "right" }
  | { type: "load-error"; seq: number; error: string }
  | { type: "page-changed"; seq: number; current: number; total: number };
