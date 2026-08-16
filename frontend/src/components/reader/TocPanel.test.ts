// Suite for the reader table-of-contents panel -- the last component in
// components/reader/ that had none. Nothing is stubbed: the panel takes
// plain props and renders real rows, so every test is a statement about the
// shipped component. The invariants, four of which regressed silently:
//   - The virtualized list keeps exactly ONE tab stop and keeps it inside the
//     rendered window. focusedIndex does not move when the user scrolls, so a
//     tabindex keyed on it alone left every mounted row at -1 once the current
//     chapter scrolled out of the window: the contents dropped out of the tab
//     order with no way back in. focusTrap's `button:not([disabled])` still
//     matched those rows, so nothing trap-driven could see it.
//   - Escape branches on the RAW query. A whitespace-only filter still renders
//     the clear button, so Escape has to clear it rather than close the panel.
//   - The window carries role="list": .tocp-window is list-style: none, which
//     drops list semantics in Safari/VoiceOver exactly where aria-setsize and
//     aria-posinset do the work.
//   - Opening the panel logs no STRICT_READ_UNTRACKED. Both effects read
//     signals from their apply phases, which are untracked scopes, so the
//     deliberate reads are wrapped in untrack() the way Read.tsx does.
//   - Opening centers on the current chapter and puts focus in the filter.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type { TocEntry } from "~/api/client";
import TocPanel from "~/components/reader/TocPanel";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

const ROW_H = 34;

function entry(n: number): TocEntry {
  return { title: `Chapter ${n}`, href: `c${n}.html`, depth: 0 };
}

function book(n: number): TocEntry[] {
  const out: TocEntry[] = [];
  for (let i = 0; i < n; i += 1) out.push(entry(i));
  return out;
}

describe("TocPanel", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let onnavigate: ReturnType<typeof vi.fn>;
  let onclose: ReturnType<typeof vi.fn>;
  let logged: string[];
  let realError: typeof console.error;
  let realWarn: typeof console.warn;
  let realLog: typeof console.log;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    onnavigate = vi.fn();
    onclose = vi.fn();
    // Solid's strict-mode diagnostics are console output, not throws, so they
    // are invisible to ordinary assertions unless captured (BookCard.test.ts
    // uses the same shape).
    logged = [];
    realError = console.error;
    realWarn = console.warn;
    realLog = console.log;
    const capture =
      (next: (...args: unknown[]) => void) =>
      (...args: unknown[]): void => {
        logged.push(args.map((a) => String(a)).join(" "));
        next(...args);
      };
    console.error = capture(realError as (...args: unknown[]) => void);
    console.warn = capture(realWarn as (...args: unknown[]) => void);
    console.log = capture(realLog as (...args: unknown[]) => void);
  });

  afterEach(() => {
    console.error = realError;
    console.warn = realWarn;
    console.log = realLog;
    dispose?.();
    dispose = undefined;
    container.remove();
    vi.restoreAllMocks();
  });

  async function mount(
    toc: TocEntry[],
    active: TocEntry | null,
  ): Promise<void> {
    dispose = render(
      () =>
        TocPanel({
          toc,
          activeEntry: active,
          onnavigate: onnavigate as (href: string) => void,
          onclose: onclose as () => void,
        }),
      container,
    );
    await settle();
  }

  const filterEl = (): HTMLInputElement =>
    container.querySelector<HTMLInputElement>(".tocp-filter input")!;
  const scroller = (): HTMLElement =>
    container.querySelector<HTMLElement>("nav.tocp-scroll")!;
  const windowEl = (): HTMLUListElement =>
    container.querySelector<HTMLUListElement>("ul.tocp-window")!;
  const rows = (): HTMLButtonElement[] =>
    Array.from(container.querySelectorAll<HTMLButtonElement>(".tocp-entry"));
  const tabStops = (): HTMLButtonElement[] =>
    rows().filter((row) => row.getAttribute("tabindex") === "0");

  function typeFilter(value: string): void {
    const el = filterEl();
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  function pressEscape(el: HTMLElement): void {
    el.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }),
    );
    flush();
  }

  it("keeps one tab stop inside the window after scrolling away", async () => {
    const toc = book(200);
    await mount(toc, toc[150]!);

    // Opens centered on chapter 150, so the mounted window is nowhere near the
    // top of the list: rows 142..157 with one tab stop on the current chapter.
    expect(rows()[0]!.id).toBe("toc-entry-142");
    expect(tabStops()).toHaveLength(1);
    expect(tabStops()[0]!.id).toBe("toc-entry-150");

    scroller().scrollTop = 0;
    scroller().dispatchEvent(new Event("scroll"));
    flush();
    await settle();

    // The window is now rows 0..15 and focusedIndex (150) sits outside it.
    // Keyed on focusedIndex alone every row here renders tabindex -1 and the
    // contents leave the tab order entirely; the tab stop has to clamp into
    // the window, landing on the edge nearest the focused row.
    expect(rows()[0]!.id).toBe("toc-entry-0");
    expect(tabStops()).toHaveLength(1);
    expect(tabStops()[0]!.id).toBe("toc-entry-15");
  });

  it("clears a whitespace-only filter on Escape instead of closing", async () => {
    const toc = book(3);
    await mount(toc, toc[0]!);

    typeFilter("   ");
    await settle();
    // The clear button renders on the raw query, so a whitespace-only filter
    // puts a visible clear affordance on screen. Escape has to mean "clear
    // that", not "close the panel out from under it".
    expect(container.querySelector(".tocp-clear")).not.toBeNull();

    pressEscape(filterEl());
    await settle();

    expect(onclose).not.toHaveBeenCalled();
    expect(filterEl().value).toBe("");
    expect(container.querySelector(".tocp-clear")).toBeNull();
  });

  it("closes on Escape once the filter is empty", async () => {
    const toc = book(3);
    await mount(toc, toc[0]!);

    pressEscape(filterEl());
    await settle();

    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("keeps distinct dialog and navigation naming hooks", async () => {
    const toc = book(3);
    await mount(toc, toc[0]!);

    const title = container.querySelector("#toc-panel-title");
    expect(title?.textContent).toBe("Contents");
    expect(scroller().getAttribute("aria-label")).toBe("Table of contents");
  });

  it("marks the virtualized window as a list", async () => {
    const toc = book(3);
    await mount(toc, toc[0]!);

    // .tocp-window is list-style: none, which drops the implicit list role in
    // Safari/VoiceOver -- exactly where aria-setsize and aria-posinset are
    // doing the work of describing a virtualized list.
    expect(windowEl().localName).toBe("ul");
    expect(windowEl().getAttribute("role")).toBe("list");

    const first = container.querySelector("ul.tocp-window > li")!;
    expect(first.getAttribute("aria-setsize")).toBe("3");
    expect(first.getAttribute("aria-posinset")).toBe("1");
  });

  it("swaps the list for a status when nothing matches", async () => {
    const toc = book(3);
    await mount(toc, toc[0]!);
    expect(rows()).toHaveLength(3);

    typeFilter("zzzz");
    await settle();

    expect(container.querySelector("nav.tocp-scroll")).toBeNull();
    const empty = container.querySelector("p.tocp-empty")!;
    expect(empty.getAttribute("role")).toBe("status");
    expect(empty.textContent).toContain("No chapters match");
    expect(empty.textContent).toContain("zzzz");
  });

  it("opens centered on the current chapter with focus in the filter", async () => {
    const toc = book(200);
    await mount(toc, toc[150]!);

    // The panel is keyboard-first: it opens with the filter focused so typing
    // narrows the contents immediately, and scrolled to the chapter the reader
    // is actually in rather than to the top of a 200-entry book.
    expect(document.activeElement).toBe(filterEl());
    expect(scroller().scrollTop).toBeGreaterThan(140 * ROW_H);

    const current = container.querySelector("[aria-current]")!;
    expect(current.id).toBe("toc-entry-150");
    expect(current.getAttribute("aria-current")).toBe("location");
  });

  it("keeps an astral TOC match on code-point boundaries", async () => {
    const toc = [{ title: "Lead 🙂🙂 tail", href: "emoji.xhtml", depth: 0 }];
    await mount(toc, toc[0]!);

    typeFilter("🙂🙂");
    await settle();

    const mark = container.querySelector(".tocp-entry mark");
    expect(mark?.textContent).toBe("🙂🙂");
    expect(Array.from(mark?.textContent ?? "")).toHaveLength(2);
    expect(mark?.previousSibling?.textContent).toBe("Lead ");
    expect(mark?.nextSibling?.textContent).toBe(" tail");
  });

  it("opens and filters without logging an untracked-read diagnostic", async () => {
    const toc = book(200);
    await mount(toc, toc[150]!);

    typeFilter("chapter 1");
    await settle();
    typeFilter("");
    await settle();

    // Both effects read signals from their apply phase, which is an untracked
    // scope: without untrack() this logs four times on open and twice more per
    // filter change, and the noise is what hides a real dependency mistake.
    expect(logged.filter((m) => m.includes("STRICT_READ_UNTRACKED"))).toEqual(
      [],
    );
  });
});
