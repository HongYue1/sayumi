import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { flush } from "solid-js";
import { toast } from "~/lib/toast";

// MAX_TOASTS = 4, DEFAULT_DURATION_MS = 2000, EXIT_MS = 200 (private to the store).
//
// Solid 2.0 batches store writes and flushes them on a microtask, and
// vi.useFakeTimers() fakes queueMicrotask along with the timer functions - so
// that flush would never run here. Every read after a write needs an explicit
// flush(); without one, `toast.items` reports the pre-write array (length 0
// while already carrying the pending indices), which is a batching artifact
// rather than a store bug.
describe("toast store", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    toast.dispose();
    flush();
  });
  afterEach(() => {
    toast.dispose();
    flush();
    vi.useRealTimers();
  });

  it("shows a toast, marks it exiting after the duration, then removes it", () => {
    toast.show("hello", 1000);
    flush();
    expect(toast.items).toHaveLength(1);
    expect(toast.items[0]).toMatchObject({ message: "hello", exiting: false });

    vi.advanceTimersByTime(1000); // enter timer -> exiting
    flush();
    expect(toast.items[0].exiting).toBe(true);

    vi.advanceTimersByTime(200); // exit timer (EXIT_MS) -> removed
    flush();
    expect(toast.items).toHaveLength(0);
  });

  // Six show() calls in one tick: the pre-2.0 read-then-replace store would
  // drop toasts here, because each call read the same pre-write array.
  it("caps the stack at 4, dropping the oldest toasts", () => {
    for (let i = 0; i < 6; i++) toast.show(`m${i}`);
    flush();
    expect(toast.items).toHaveLength(4);
    expect(toast.items.map((t) => t.message)).toEqual(["m2", "m3", "m4", "m5"]);
  });

  // A dropped toast's timers must die with it. The assertion above cannot see
  // this: dropped items are already out of the array, so gutting clearTimers()
  // leaves the messages correct and only the pending timer count betrays the
  // leak -- 4 survivors x 1 enter timer, not 6.
  it("clears the timers of toasts dropped by the cap", () => {
    for (let i = 0; i < 6; i++) toast.show(`m${i}`);
    flush();

    expect(toast.items).toHaveLength(4);
    expect(vi.getTimerCount()).toBe(4);
  });

  it("dispose clears items and cancels pending timers", () => {
    toast.show("a");
    toast.show("b");
    flush();
    expect(toast.items).toHaveLength(2);

    toast.dispose();
    flush();
    expect(toast.items).toHaveLength(0);

    // Cleared timers must not resurrect anything.
    vi.advanceTimersByTime(5000);
    flush();
    expect(toast.items).toHaveLength(0);
  });
});
