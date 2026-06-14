// Global transient-feedback toasts. A single store instance is mounted once by
// <Toaster /> in App.svelte; anything can call `toast.show(...)`.
// Ported behaviour from the legacy Solid `createToastState` (enter → exit →
// remove), reworked as a Svelte 5 `$state` class singleton.

export interface ToastItem {
  id: number;
  message: string;
  exiting: boolean;
}

const DEFAULT_DURATION_MS = 2000;
const EXIT_MS = 200;
const MAX_TOASTS = 4;

class ToastStore {
  items = $state<ToastItem[]>([]);

  #nextId = 0;
  #enterTimers = new Map<number, ReturnType<typeof setTimeout>>();
  #exitTimers = new Map<number, ReturnType<typeof setTimeout>>();

  show(message: string, duration = DEFAULT_DURATION_MS): void {
    const id = this.#nextId++;
    let next = [...this.items, { id, message, exiting: false }];
    // Cap the stack so a burst of show() calls can't pile up an unbounded
    // column; drop the oldest toasts and clear their pending timers.
    if (next.length > MAX_TOASTS) {
      for (const t of next.slice(0, next.length - MAX_TOASTS)) {
        this.#clearTimers(t.id);
      }
      next = next.slice(next.length - MAX_TOASTS);
    }
    this.items = next;

    const enter = setTimeout(() => {
      this.#enterTimers.delete(id);
      this.items = this.items.map((t) => (t.id === id ? { ...t, exiting: true } : t));
      const exit = setTimeout(() => {
        this.#exitTimers.delete(id);
        this.items = this.items.filter((t) => t.id !== id);
      }, EXIT_MS);
      this.#exitTimers.set(id, exit);
    }, duration);

    this.#enterTimers.set(id, enter);
  }

  #clearTimers(id: number): void {
    const enter = this.#enterTimers.get(id);
    if (enter !== undefined) {
      clearTimeout(enter);
      this.#enterTimers.delete(id);
    }
    const exit = this.#exitTimers.get(id);
    if (exit !== undefined) {
      clearTimeout(exit);
      this.#exitTimers.delete(id);
    }
  }

  // Clears all pending timers. Mirrors the legacy onCleanup; useful for tests
  // and on full teardown (the store itself lives for the app's lifetime).
  dispose(): void {
    for (const t of this.#enterTimers.values()) clearTimeout(t);
    for (const t of this.#exitTimers.values()) clearTimeout(t);
    this.#enterTimers.clear();
    this.#exitTimers.clear();
    this.items = [];
  }
}

export const toast = new ToastStore();
