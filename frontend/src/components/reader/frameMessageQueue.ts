// Bounded outbound queue used by ChapterFrame for both iframe-handshake state
// and commands waiting on a chapter's settled `loaded` event. Extracted so the
// ordering, coalescing, cancellation, and cap are tested without mounting.
import type { ParentToFrameMessage } from "~/lib/frameMessages";

// These carry latest-wins state. A load already contains its settings snapshot;
// standalone settings remain coalesced for live updates, and font CSS is state.
// Replacing in place keeps the queue's causal slots stable.
const COALESCE_TYPES = new Set<ParentToFrameMessage["type"]>([
  "load",
  "apply-settings",
  "set-font-faces",
]);
// Safety valve for a far side that goes silent: the handshake queue fills when
// the frame never signals ready (e.g. its script is blocked), the chapter queue
// when a load never settles. Bound both instead of letting them grow with every
// command.
const MAX_QUEUED = 64;

export interface FrameMessageQueue {
  enqueue(message: ParentToFrameMessage): void;
  /** Returns the queued messages in order and empties the queue. */
  drain(): ParentToFrameMessage[];
  clear(): void;
  discard(type: ParentToFrameMessage["type"]): void;
  readonly size: number;
}

export function createFrameMessageQueue(
  maxQueued: number = MAX_QUEUED,
): FrameMessageQueue {
  let queue: ParentToFrameMessage[] = [];
  return {
    enqueue(message: ParentToFrameMessage): void {
      // Coalesce in place: earliest causal slot, newest payload. That also
      // moves the newest payload ahead of anything queued after the slot it
      // reuses; what keeps it safe is the caller, since ChapterFrame clears the
      // chapter queue on every new load and only frame-level state shares this
      // queue.
      if (COALESCE_TYPES.has(message.type)) {
        const at = queue.findIndex((m) => m.type === message.type);
        if (at >= 0) {
          // Length is unchanged, so the cap below cannot have been breached.
          queue[at] = message;
          return;
        }
      }
      queue.push(message);
      // Reached only while the far side is silent (see MAX_QUEUED). Preserve
      // the bounded latest-wins state (load/settings/font faces) and evict the
      // oldest stray non-coalesced command first; otherwise a burst of one-off
      // commands could drop the only queued chapter load. The shift()
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
    discard(type): void {
      queue = queue.filter((message) => message.type !== type);
    },
    get size(): number {
      return queue.length;
    },
  };
}
