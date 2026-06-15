import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { toast } from "~/lib/toast.svelte";

// MAX_TOASTS = 4, DEFAULT_DURATION_MS = 2000, EXIT_MS = 200 (private to the store).
describe("toast store", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    toast.dispose();
  });
  afterEach(() => {
    toast.dispose();
    vi.useRealTimers();
  });

  it("shows a toast, marks it exiting after the duration, then removes it", () => {
    toast.show("hello", 1000);
    expect(toast.items).toHaveLength(1);
    expect(toast.items[0]).toMatchObject({ message: "hello", exiting: false });

    vi.advanceTimersByTime(1000); // enter timer -> exiting
    expect(toast.items[0].exiting).toBe(true);

    vi.advanceTimersByTime(200); // exit timer (EXIT_MS) -> removed
    expect(toast.items).toHaveLength(0);
  });

  it("caps the stack at 4, dropping the oldest toasts", () => {
    for (let i = 0; i < 6; i++) toast.show(`m${i}`);
    expect(toast.items).toHaveLength(4);
    expect(toast.items.map((t) => t.message)).toEqual(["m2", "m3", "m4", "m5"]);
  });

  it("dispose clears items and cancels pending timers", () => {
    toast.show("a");
    toast.show("b");
    expect(toast.items).toHaveLength(2);

    toast.dispose();
    expect(toast.items).toHaveLength(0);

    // Cleared timers must not resurrect anything.
    vi.advanceTimersByTime(5000);
    expect(toast.items).toHaveLength(0);
  });
});
