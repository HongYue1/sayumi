// Pure progress-persistence predicates extracted from routes/Read.svelte so they
// can be unit-tested without mounting the reader. Read.svelte imports these;
// do NOT re-inline copies (single source of truth).
import type { ProgressData } from "~/api/client";

export interface ProgressPosition {
  chapter: number;
  percent: number;
}

/** Below this percent delta (same chapter) a change isn't worth a server write. */
export const PROGRESS_EPSILON = 0.001;

/** True when `next` is effectively the last-persisted position, so a non-forced
 *  flush can be skipped. Mirrors the lastPersistedChapter/Percent dedupe in
 *  Read.svelte.flushProgress. */
export function isProgressDuplicate(
  next: ProgressPosition,
  last: ProgressPosition,
  eps: number = PROGRESS_EPSILON,
): boolean {
  return next.chapter === last.chapter && Math.abs(next.percent - last.percent) < eps;
}

/** Boot-time merge of the server progress and a (possibly newer) local cache
 *  written on the last page-hide beacon. Returns whichever position should be
 *  restored: the cache wins unless the server was loaded AND is strictly ahead. */
export function chooseBootProgress(
  server: ProgressData,
  cached: ProgressData,
  serverLoaded: boolean,
): ProgressData {
  const serverAhead =
    cached.chapter < server.chapter ||
    (cached.chapter === server.chapter && cached.percent <= server.percent);
  return !serverAhead || !serverLoaded ? cached : server;
}

/** Below this percent delta a bookmark counts as "at the current position". */
export const BOOKMARK_EPSILON = 0.02;

/** True when a bookmark sits at (or very near) the given chapter + percent. */
export function isBookmarkAtPosition(
  bookmark: { chapter: number; percent: number },
  chapter: number,
  percent: number,
  eps: number = BOOKMARK_EPSILON,
): boolean {
  return bookmark.chapter === chapter && Math.abs(bookmark.percent - percent) < eps;
}
