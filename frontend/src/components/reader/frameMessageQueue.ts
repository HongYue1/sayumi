// Pre-ready outbound message queue for ChapterFrame, extracted so its coalescing
// + cap behaviour can be unit-tested without mounting the iframe component.
// Single source of truth — ChapterFrame imports this; do NOT re-inline a copy.
import type { ParentToFrameMessage } from "~/lib/frameMessages";

// Pre-ready, these carry "latest wins" state: a newer message of one of these
// types makes any earlier queued message of the same type obsolete (a fresh
// `load` replaces the chapter; for apply-settings/set-font-faces only the latest
// value matters).
//
// What actually reaches this queue is NOT these three. routes/Read.tsx gates
// every load / apply-settings / set-font-faces behind frameReady or
// initialLoadDone, and both are only set from the onready handler that
// ChapterFrame fires after it has already flushed. What can arrive pre-ready is
// the ungated interaction commands -- scroll, page, fragment, highlight --
// pressed between the api handoff and ready. Coalescing is therefore
// defence-in-depth for a caller that does not gate, not a live path. Readiness
// being solved twice, once by the parent's gate and once by this buffer, is the
// X54 candidate; until an owner is picked this file still has to be correct for
// the case it exists for.
const COALESCE_TYPES = new Set<ParentToFrameMessage["type"]>([
  "load",
  "apply-settings",
  "set-font-faces",
]);
// Safety valve: if the frame never signals ready (e.g. its script is blocked),
// bound the queue instead of letting it grow with every interaction.
const MAX_QUEUED = 64;

export interface FrameMessageQueue {
  enqueue(message: ParentToFrameMessage): void;
  /** Returns the queued messages in order and empties the queue. */
  drain(): ParentToFrameMessage[];
  clear(): void;
  readonly size: number;
}

export function createFrameMessageQueue(
  maxQueued: number = MAX_QUEUED,
): FrameMessageQueue {
  let queue: ParentToFrameMessage[] = [];
  return {
    enqueue(message: ParentToFrameMessage): void {
      // Coalesce IN PLACE: earliest position, newest payload. Dropping the old
      // entry and pushing the newcomer to the tail reorders the queue, and
      // (load, apply-settings) is order-critical. A settings message that beats
      // its load into the frame is applied at once -- loadCommitTimer is still
      // null -- writing #font-face-css and #book-css from prepared CSS that is
      // still empty, and caching that in _lastFontFaceContent/_lastBookCSS.
      // commitLoad re-applies settings only when pendingSettingsMessage is set,
      // which it is not when the settings arrived first, so the chapter paints
      // with no book styles and the memo suppresses the repair until a setting
      // genuinely changes.
      if (COALESCE_TYPES.has(message.type)) {
        const at = queue.findIndex((m) => m.type === message.type);
        if (at >= 0) {
          // Length is unchanged, so the cap below cannot have been breached.
          queue[at] = message;
          return;
        }
      }
      queue.push(message);
      // Only reachable when the frame never readied. Preserve the bounded
      // latest-wins state (load/settings/font faces) and evict the oldest stray
      // non-coalesced interaction first; otherwise a burst of scroll/page input
      // before ready could drop the only queued chapter load. The shift()
      // fallback needs a queue holding nothing but coalesce types, which takes
      // maxQueued below COALESCE_TYPES.size; no caller passes that, so the
      // suite is the only thing that reaches it.
      if (queue.length > maxQueued) {
        const dropIndex = queue.findIndex((m) => !COALESCE_TYPES.has(m.type));
        if (dropIndex >= 0) queue.splice(dropIndex, 1);
        else queue.shift();
      }
    },
    drain(): ParentToFrameMessage[] {
      const out = queue;
      queue = [];
      return out;
    },
    clear(): void {
      queue = [];
    },
    get size(): number {
      return queue.length;
    },
  };
}
