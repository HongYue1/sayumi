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

class ToastStore {
  items = $state<ToastItem[]>([]);

  #nextId = 0;
  #enterTimers = new Map<number, ReturnType<typeof setTimeout>>();
  #exitTimers = new Map<number, ReturnType<typeof setTimeout>>();

  show(message: string, duration = DEFAULT_DURATION_MS): void {
    const id = this.#nextId++;
    this.items = [...this.items, { id, message, exiting: false }];

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
