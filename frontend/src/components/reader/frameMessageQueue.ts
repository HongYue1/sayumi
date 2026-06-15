// Pre-ready outbound message queue for ChapterFrame, extracted so its coalescing
// + cap behaviour can be unit-tested without mounting the iframe component.
// Single source of truth — ChapterFrame imports this; do NOT re-inline a copy.
import type { ParentToFrameMessage } from "~/lib/frameMessages";

// Pre-ready, these carry "latest wins" state: a newer message of one of these
// types makes any earlier queued message of the same type obsolete (a fresh
// `load` replaces the chapter; for apply-settings/set-font-faces only the latest
// value matters). Coalescing stops the brief startup flush from replaying
// chapters the user already navigated past.
const COALESCE_TYPES = new Set(["load", "apply-settings", "set-font-faces"]);
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
      if (COALESCE_TYPES.has(message.type)) {
        queue = queue.filter((m) => m.type !== message.type);
      }
      queue.push(message);
      // Only reachable when the frame never readied; the coalesced types can't
      // grow unbounded, so this just caps stray non-coalesced messages.
      if (queue.length > maxQueued) queue.shift();
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
