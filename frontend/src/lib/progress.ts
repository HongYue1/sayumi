// Pure progress-persistence predicates extracted from routes/Read.tsx so they
// can be unit-tested without mounting the reader. Read.tsx imports these;
// do NOT re-inline copies (single source of truth).
import type { ProgressData } from "~/api/client";

export interface ProgressPosition {
  chapter: number;
  percent: number;
  /**
   * Semantic anchor for the position. Optional: the unset sentinel carries
   * none, and an absent anchor on both sides still compares equal.
   */
  cfi?: string;
}

/** Below this percent delta (same chapter) a change isn't worth a server write. */
export const PROGRESS_EPSILON = 0.001;

/**
 * The nothing-persisted-yet marker for the last-persisted position. Read.tsx
 * seeds lastPersisted* with this so the first flush of a session is never
 * deduped. It lives here because the unit suite pins the behaviour; a bare -1
 * at the call site let the two drift apart silently.
 */
export const PROGRESS_UNSET = -1;

/**
 * True when `next` is effectively the last-persisted position, so a non-forced
 * flush can be skipped. Mirrors the lastPersistedChapter/Percent/Cfi dedupe in
 * Read.tsx.flushProgress.
 *
 * The anchor is part of the key, not decoration. Paged percent is quantized to
 * page/(totalPages-1) (iframe/pagination.ts), so an anchor-preserving relayout
 * can land on a page whose ratio is unchanged while the block anchoring the
 * view genuinely moved; the empty marker resolving into a real CFI at an
 * unchanged page is the same shape. Comparing chapter+percent only swallowed
 * those writes, and the reader then restored from an anchor that no longer
 * matched the page it was saved on. The iframe sends a CFI on every position
 * report precisely so the parent can tell them apart -- do not drop it again.
 */
export function isProgressDuplicate(
  next: ProgressPosition,
  last: ProgressPosition,
  eps: number = PROGRESS_EPSILON,
): boolean {
  return (
    next.chapter === last.chapter &&
    Math.abs(next.percent - last.percent) < eps &&
    next.cfi === last.cfi
  );
}

/**
 * Returns the page-hide cache as the boot position, unconditionally. The server
 * value is never consulted, which is why its parameter is underscore-prefixed.
 *
 * This is a policy, not an arbitration, and the distinction matters: the cache
 * is removed only after a successful saveProgress from this tab, while the
 * page-hide and unmount paths write the cache and beacon WITHOUT saving. A
 * beaconed cache therefore outlives having been persisted, so a cache entry
 * does not imply the server is behind. Another client that read on after this
 * one hid will be rewound on the next boot here, and the first flush persists
 * the rewind over it. Arbitrating needs a server timestamp:
 * storage.ProgressRecord.UpdatedAt exists but api.progressBody never
 * serializes it, so neither branch of the read path can return it. Deferred --
 * the fix is a server + client change, not a predicate change.
 */
export function chooseBootProgress(
  _server: ProgressData,
  cached: ProgressData,
): ProgressData {
  return cached;
}

/**
 * Below this percent delta a bookmark counts as "at the current position".
 *
 * Chapter-relative, NOT page-relative: paged percent is page/(totalPages-1),
 * so this spans more than a whole page in any chapter past roughly fifty
 * pages -- and the toggle DELETES whatever it matches. A length-aware epsilon
 * plus nearest-match instead of first-match is deferred.
 */
export const BOOKMARK_EPSILON = 0.02;

/** True when a bookmark sits at (or very near) the given chapter + percent. */
export function isBookmarkAtPosition(
  bookmark: { chapter: number; percent: number },
  chapter: number,
  percent: number,
  eps: number = BOOKMARK_EPSILON,
): boolean {
  return (
    bookmark.chapter === chapter && Math.abs(bookmark.percent - percent) < eps
  );
}
