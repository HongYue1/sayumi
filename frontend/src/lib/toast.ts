// Global transient-feedback toasts. A single store instance is mounted once by
// <Toaster /> in App.tsx; anything can call `toast.show(...)`.
// Lifecycle is unchanged from the Svelte revision: enter -> exit -> remove.
//
// Solid 2.0 note: this uses a draft-first `createStore` rather than replacing an
// array signal. That is load-bearing, not stylistic -- writes are batched, so
// two `show()` calls in the same tick would both read the pre-write array under
// a replace-the-whole-array approach and the first toast would be silently
// dropped. Draft mutations accumulate on the same draft within a batch, so
// bursts compose correctly. It also makes the exit transition a single-property
// write instead of rebuilding every item.
import { createStore } from "solid-js";

export interface ToastItem {
  id: number;
  message: string;
  exiting: boolean;
}

const DEFAULT_DURATION_MS = 2000;
const EXIT_MS = 200;
const MAX_TOASTS = 4;

function createToastStore() {
  const [items, setItems] = createStore<ToastItem[]>([]);

  let nextId = 0;
  const enterTimers = new Map<number, ReturnType<typeof setTimeout>>();
  const exitTimers = new Map<number, ReturnType<typeof setTimeout>>();

  function clearTimers(id: number): void {
    const enter = enterTimers.get(id);
    if (enter !== undefined) {
      clearTimeout(enter);
      enterTimers.delete(id);
    }
    const exit = exitTimers.get(id);
    if (exit !== undefined) {
      clearTimeout(exit);
      exitTimers.delete(id);
    }
  }

  return {
    get items(): readonly ToastItem[] {
      return items;
    },

    show(message: string, duration = DEFAULT_DURATION_MS): void {
      const id = nextId++;
      let dropped: number[] = [];

      setItems((s) => {
        s.push({ id, message, exiting: false });
        // Cap the stack so a burst of show() calls can't pile up an unbounded
        // column; drop the oldest toasts and clear their pending timers.
        if (s.length > MAX_TOASTS) {
          dropped = s.splice(0, s.length - MAX_TOASTS).map((t) => t.id);
        }
      });

      // Timer bookkeeping is plain (non-reactive) state, so it is settled
      // outside the draft callback.
      for (const droppedId of dropped) clearTimers(droppedId);

      const enter = setTimeout(() => {
        enterTimers.delete(id);
        setItems((s) => {
          const item = s.find((t) => t.id === id);
          if (item) item.exiting = true;
        });

        const exit = setTimeout(() => {
          exitTimers.delete(id);
          setItems((s) => {
            const index = s.findIndex((t) => t.id === id);
            if (index !== -1) s.splice(index, 1);
          });
        }, EXIT_MS);
        exitTimers.set(id, exit);
      }, duration);

      enterTimers.set(id, enter);
    },

    // Clears all pending timers. Useful for tests and on full teardown (the
    // store itself lives for the app's lifetime).
    dispose(): void {
      for (const t of enterTimers.values()) clearTimeout(t);
      for (const t of exitTimers.values()) clearTimeout(t);
      enterTimers.clear();
      exitTimers.clear();
      setItems((s) => {
        s.splice(0, s.length);
      });
    },
  };
}

export const toast = createToastStore();
