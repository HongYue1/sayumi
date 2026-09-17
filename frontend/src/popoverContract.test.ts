import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Seven popover surfaces are hand-written rather than built on one shared
// primitive, and that is the choice: they agree on almost nothing except the
// dismissal contract. The roles differ (menu, listbox, a role="group" slider
// box), the dismissal phase differs (BookCard has to preempt the open-book
// overlay's own click, so it captures; the rest listen on pointerdown), the
// row selectors differ, and opening costs different things -- a theme registry
// fetch here, the reader's chrome timer there, a type-ahead buffer elsewhere.
// One primitive spanning all of that would need an escape hatch per caller.
//
// What must NOT differ is the part that has bitten before: open state is
// mirrored in a plain variable, because a Solid signal read cannot see its own
// write in the same tick, and every toggle and every dismissal reads that
// mirror rather than the signal. This suite pins the contract across the
// copies, so neither a new popover nor an edit to an old one can quietly drop
// half of it.

type Popover = {
  path: string;
  // Plain non-reactive mirror of the open signal.
  mirror: string;
  // The single funnel every open/close write goes through.
  setter: string;
  // The signal setter that funnel wraps.
  signal: string;
  // The popup role the trigger promises, or null where no token fits.
  haspopup: string | null;
};

const POPOVERS: Popover[] = [
  {
    path: "components/library/ProfileMenu.tsx",
    mirror: "openNow",
    setter: "setOpenState",
    signal: "setOpen",
    haspopup: "menu",
  },
  {
    path: "components/library/ThemeDropdown.tsx",
    mirror: "openNow",
    setter: "setOpenState",
    signal: "setOpen",
    haspopup: "menu",
  },
  {
    path: "components/library/CardSizeControl.tsx",
    mirror: "openNow",
    setter: "setOpenState",
    signal: "setOpen",
    haspopup: null,
  },
  {
    path: "components/reader/DropSelect.tsx",
    mirror: "openNow",
    setter: "setOpenState",
    signal: "setOpen",
    haspopup: "listbox",
  },
  {
    path: "routes/Library.tsx",
    mirror: "sortOpenNow",
    setter: "setSortOpenState",
    signal: "setSortOpen",
    haspopup: "menu",
  },
  {
    path: "routes/Read.tsx",
    mirror: "moreOpenNow",
    setter: "setMoreOpenState",
    signal: "setMoreOpen",
    haspopup: "menu",
  },
];

// BookCard drives two menus from one openMenu() signal, so it has no single
// mirror to check; what it shares is the narrowing and the disclosure state.
const BOOK_CARD = "components/library/BookCard.tsx";

function source(path: string): string {
  return readFileSync(join(process.cwd(), "src", path), "utf8");
}

// The rationale comments quote the very patterns these assertions ban ("never
// `if (!open())`"), so every scan runs over the code with comment lines cut.
function code(path: string): string {
  return source(path)
    .split("\n")
    .filter((line) => !line.trimStart().startsWith("//"))
    .join("\n");
}

// Derive the reader from the setter instead of listing it: a renamed signal
// then cannot slip past the "never read the signal" check by going unlisted.
function reader(signal: string): string {
  const name = signal.slice("set".length);
  return name.charAt(0).toLowerCase() + name.slice(1);
}

describe("popover dismissal contract", () => {
  it("mirrors open state behind a single write funnel", () => {
    for (const { path, mirror, setter, signal } of POPOVERS) {
      const src = code(path);
      expect(src, path).toContain(`let ${mirror} = false;`);
      expect(src, path).toMatch(
        new RegExp(
          `function ${setter}\\(next: boolean\\): void \\{\\s*` +
            `${mirror} = next;\\s*${signal}\\(next\\);`,
          "u",
        ),
      );
      // Exactly one call, the funnel's own: a write that goes around it
      // leaves the mirror stale, which is the failure the mirror exists to
      // prevent in the first place.
      const writes = src.match(new RegExp(`\\b${signal}\\(`, "gu")) ?? [];
      expect(writes, path).toHaveLength(1);
    }
  });

  it("reads the mirror, never the signal, to open and close", () => {
    for (const { path, mirror, signal } of POPOVERS) {
      const src = code(path);
      expect(src, path).toContain(`if (!${mirror}) return;`);
      expect(src, path).not.toMatch(
        new RegExp(`!\\s*${reader(signal)}\\(\\)`, "u"),
      );
    }
  });

  it("narrows the dismissal target instead of casting it", () => {
    for (const path of [
      ...POPOVERS.map((popover) => popover.path),
      BOOK_CARD,
    ]) {
      const src = code(path);
      expect(src, path).toContain("instanceof Node");
      expect(src, path).not.toContain("target as");
    }
  });

  it("dismisses on pointerdown, except where a click is preempted", () => {
    for (const { path } of POPOVERS) {
      expect(code(path), path).toContain('addEventListener("pointerdown"');
    }
    const card = code(BOOK_CARD);
    expect(card).toContain(
      'window.addEventListener("click", onWindowClick, true);',
    );
    expect(card).not.toContain('addEventListener("pointerdown"');
    // The capture phase is the divergence, so keep its reason in the file.
    expect(source(BOOK_CARD)).toContain("equivalent of the openNow mirror");
  });

  it("promises only the popup role its popover actually has", () => {
    const triggers = [...POPOVERS, { path: BOOK_CARD, haspopup: "menu" }];
    for (const { path, haspopup } of triggers) {
      const src = code(path);
      expect(src, path).toContain("aria-expanded");
      if (haspopup === null) {
        // No token describes a slider box, so the omission is a decision and
        // has to stay documented rather than read as a missed attribute.
        expect(src, path).not.toContain("aria-haspopup");
        expect(source(path), path).toContain("No aria-haspopup");
      } else {
        expect(src, path).toContain(`aria-haspopup="${haspopup}"`);
      }
    }
  });
});
