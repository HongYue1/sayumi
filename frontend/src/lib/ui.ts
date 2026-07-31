// Tiny shared UI state for global overlays (command palette, shortcuts help)
// so any component can open them without prop-drilling or event plumbing.
//
// Solid 2.0 note: writes are batched, so a setter does NOT change what the
// matching accessor returns until the microtask flush. Every mutator below
// therefore computes the next value once and reuses that local, rather than
// writing and then re-reading (which would observe the pre-write value).
import { createSignal } from "solid-js";

function createUIState() {
  const [palette, setPalette] = createSignal(false);
  const [shortcuts, setShortcuts] = createSignal(false);

  return {
    get palette(): boolean {
      return palette();
    },
    get shortcuts(): boolean {
      return shortcuts();
    },

    togglePalette(): void {
      const next = !palette();
      setPalette(next);
      // Opening the palette dismisses the shortcuts sheet so the two
      // focus-trapped overlays can't stack (mirrors openShortcuts() closing the
      // palette). Closing the palette leaves shortcuts untouched.
      //
      // `next` is used deliberately instead of re-reading palette(): under
      // Solid 2.0 batching the read would still return the old value here.
      if (next) setShortcuts(false);
    },

    openShortcuts(): void {
      setPalette(false);
      setShortcuts(true);
    },

    closeOverlays(): void {
      setPalette(false);
      setShortcuts(false);
    },
  };
}

export const ui = createUIState();
