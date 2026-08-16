// Suite for the global overlay store: the two flags every focus-trapped
// overlay reads (CommandPalette, ShortcutsHelp) and the three mutators that
// App.tsx and Read.tsx drive from the keyboard and from reader chrome.
//
// Every test builds its own instance through createUIState(). The suites
// that touch this store indirectly reset it by calling
// ui.closeOverlays() (CommandPalette.test.ts, ShortcutsHelp.test.ts,
// Read.test.ts) -- a
// fixture derived from a function under test, which by construction cannot
// detect that function changing. App.test.ts isolates by re-importing under
// vi.resetModules instead. Fresh instances remove the ordering
// dependency instead of documenting it.
//
// Two batching facts are pinned here because behaviour elsewhere leans on
// them:
//   - A write is not visible to a read in the same tick; flush() publishes it,
//     which is why every mutator computes its next value once into a local.
//   - Two toggles in one tick therefore net OPEN, not closed. Read.tsx carries
//     an explicit single-ownership guard for Ctrl/Cmd+K because that masking is
//     silent, and this suite is what makes a change in it fail loudly.
import { describe, expect, it } from "vitest";
import { render } from "@solidjs/web";
import { createEffect, flush } from "solid-js";
import { createUIState, ui } from "~/lib/ui";

type Op = "togglePalette" | "openShortcuts" | "closeOverlays";

describe("ui overlay state", () => {
  it("starts with both overlays closed", () => {
    const u = createUIState();
    expect(u.palette).toBe(false);
    expect(u.shortcuts).toBe(false);
  });

  it("opens the palette from closed", () => {
    const u = createUIState();
    u.togglePalette();
    flush();
    expect(u.palette).toBe(true);
  });

  it("closes the palette from open", () => {
    const u = createUIState();
    u.togglePalette();
    flush();
    u.togglePalette();
    flush();
    expect(u.palette).toBe(false);
  });

  it("does not publish a write until the flush", () => {
    const u = createUIState();
    u.togglePalette();
    expect(u.palette).toBe(false);
    flush();
    expect(u.palette).toBe(true);
  });

  it("nets open when two handlers toggle on one tick", () => {
    const u = createUIState();
    u.togglePalette();
    u.togglePalette();
    flush();
    expect(u.palette).toBe(true);
  });

  it("dismisses the shortcuts sheet when the palette opens", () => {
    const u = createUIState();
    u.openShortcuts();
    flush();
    u.togglePalette();
    flush();
    expect(u.palette).toBe(true);
    expect(u.shortcuts).toBe(false);
  });

  it("opens the sheet and dismisses the palette", () => {
    const u = createUIState();
    u.togglePalette();
    flush();
    u.openShortcuts();
    flush();
    expect(u.shortcuts).toBe(true);
    expect(u.palette).toBe(false);
  });

  it("closes an open palette", () => {
    const u = createUIState();
    u.togglePalette();
    flush();
    u.closeOverlays();
    flush();
    expect(u.palette).toBe(false);
    expect(u.shortcuts).toBe(false);
  });

  it("closes an open sheet", () => {
    const u = createUIState();
    u.openShortcuts();
    flush();
    u.closeOverlays();
    flush();
    expect(u.shortcuts).toBe(false);
    expect(u.palette).toBe(false);
  });

  it("never leaves both overlays open, over every sequence up to four steps", () => {
    const ops: Op[] = ["togglePalette", "openShortcuts", "closeOverlays"];
    const bad: string[] = [];
    const walk = (path: Op[]): void => {
      if (path.length > 0) {
        const u = createUIState();
        for (const op of path) {
          u[op]();
          flush();
          if (u.palette && u.shortcuts) bad.push(path.join(" > "));
        }
      }
      if (path.length === 4) return;
      for (const op of ops) walk(path.concat(op));
    };
    walk([]);
    expect(bad).toEqual([]);
  });

  it("repeat calls do not re-notify subscribers", () => {
    const u = createUIState();
    const host = document.createElement("div");
    document.body.appendChild(host);
    let runs = 0;
    const dispose = render(() => {
      createEffect(
        () => u.shortcuts,
        () => {
          runs += 1;
        },
      );
      return null;
    }, host);
    flush();
    runs = 0;
    u.openShortcuts();
    flush();
    expect(runs).toBe(1);
    u.openShortcuts();
    u.openShortcuts();
    flush();
    expect(runs).toBe(1);
    dispose();
    host.remove();
  });

  it("hands out independent instances, and the module export is one of them", () => {
    const fresh = createUIState();
    try {
      ui.openShortcuts();
      flush();
      expect(ui.shortcuts).toBe(true);
      expect(fresh.shortcuts).toBe(false);
    } finally {
      ui.closeOverlays();
      flush();
    }
    expect(ui.shortcuts).toBe(false);
  });

  it("exposes the flags read-only", () => {
    const u = createUIState();
    // Getter-only, so assigning a flag (ui.shortcuts = false) is a TypeError
    // rather than a silent no-op. Asserted through behaviour: reading the
    // descriptor back would reference an unbound method.
    expect(() => Object.assign(u, { palette: true })).toThrow(TypeError);
    expect(u.palette).toBe(false);
  });

  it("anyOverlayOpen starts false and reflects either flag", () => {
    const u = createUIState();
    expect(u.anyOverlayOpen).toBe(false);
    u.togglePalette();
    flush();
    expect(u.anyOverlayOpen).toBe(true);
    u.closeOverlays();
    flush();
    expect(u.anyOverlayOpen).toBe(false);
    u.openShortcuts();
    flush();
    expect(u.anyOverlayOpen).toBe(true);
  });

  it("anyOverlayOpen closes with closeOverlays", () => {
    const u = createUIState();
    u.openShortcuts();
    flush();
    expect(u.anyOverlayOpen).toBe(true);
    u.closeOverlays();
    flush();
    expect(u.anyOverlayOpen).toBe(false);
  });

  it("anyOverlayOpen notifies once per real input change", () => {
    const u = createUIState();
    const host = document.createElement("div");
    document.body.appendChild(host);
    let runs = 0;
    const dispose = render(() => {
      createEffect(
        () => u.anyOverlayOpen,
        () => {
          runs += 1;
        },
      );
      return null;
    }, host);
    flush();
    runs = 0;
    u.togglePalette();
    flush();
    expect(runs).toBe(1);
    // The swap re-runs the apply: both underlying signals change value, so the
    // compute re-executes even though the conjunction's value stays true.
    // Measured, not assumed: Solid 2.0 keys the apply phase on compute
    // re-execution, not on the compute's output.
    u.openShortcuts();
    flush();
    expect(runs).toBe(2);
    // Same-value writes never notify, so nothing re-runs.
    u.openShortcuts();
    flush();
    expect(runs).toBe(2);
    u.closeOverlays();
    flush();
    expect(runs).toBe(3);
    dispose();
    host.remove();
  });
});
