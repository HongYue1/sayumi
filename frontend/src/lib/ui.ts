// Tiny shared UI state for global overlays (command palette, shortcuts help)
// so any component can open them without prop-drilling or event plumbing.
//
// Solid 2.0 note: writes are batched, so a setter does NOT change what the
// matching accessor returns until the microtask flush. Every mutator below
// therefore computes the next value once and reuses that local, rather than
// writing and then re-reading (which would observe the pre-write value).
// Measured, not assumed: a write followed by an immediate read in
// the same tick returns the pre-write value, and only flush() publishes it.
//
// One consequence is load-bearing elsewhere. Two handlers toggling the
// palette on a single keydown both read the pre-write value, so both
// compute the same next state and the second toggle is MASKED -- the
// palette ends up open rather than flickering shut. Read.tsx keeps an
// explicit single-ownership guard for Ctrl/Cmd+K anyway, because that
// masking is silent; ui.test.ts pins it so a change in batching semantics
// fails there first.
import { createSignal } from "solid-js";

export interface UIState {
  readonly palette: boolean;
  readonly shortcuts: boolean;
  /** True while either global overlay is open. The single place the conjunct
   *  lives: a future overlay kind joins the store here, and readers (the
   *  reader's keyboard stand-down) never re-derive the list by hand. */
  readonly anyOverlayOpen: boolean;
  togglePalette(): void;
  openShortcuts(): void;
  closeOverlays(): void;
}

/**
 * Builds an independent overlay state. Exported for tests only: app code
 * must use the `ui` singleton below, so every consumer shares one set of
 * flags. A suite that reset the singleton by calling closeOverlays() would
 * be deriving its fixture from a function under test -- which is exactly
 * what the suites that reset via closeOverlays() (CommandPalette,
 * ShortcutsHelp, Read) have to do. App.test.ts isolates by re-importing
 * under vi.resetModules instead.
 */
export function createUIState(): UIState {
  const [palette, setPalette] = createSignal(false);
  const [shortcuts, setShortcuts] = createSignal(false);

  return {
    get palette(): boolean {
      return palette();
    },
    get shortcuts(): boolean {
      return shortcuts();
    },
    get anyOverlayOpen(): boolean {
      return palette() || shortcuts();
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

/** The one instance every component reads and mutates. */
export const ui = createUIState();
