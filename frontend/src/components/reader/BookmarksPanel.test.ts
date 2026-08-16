// Suite for the reader bookmarks panel. Nothing is stubbed: the panel takes
// plain props and renders real rows, so every test is a statement about the
// shipped component. The invariants, two of which regressed silently before
// this suite existed:
//   - Focus follows the edit toggle both ways: entering edit mode puts focus
//     on the label field, and leaving it -- save or cancel -- returns focus to
//     that row's edit button. The old self-focusing refs ran while the nodes
//     were still detached, so both moves were silent no-ops and
//     focus dropped to body.
//   - Rows sort by chapter, then position within the chapter.
//   - bookmarkName prefers the user's label, then the chapter's TOC heading,
//     then a Chapter N fallback.
//   - Label and note are capped at 2000 UTF-8 BYTES (the server enforces the
//     same byte cap in internal/api/bookmarks.go); an over-cap save shows a
//     role=alert error that clears on the next keystroke, and never calls
//     onupdate.
//   - An Escape that belongs to an IME composition is not a dismissal, and a
//     real Escape cancels without bubbling to the reader's window handler.
//   - Enter saves from the label input but not from the note textarea.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type { Bookmark } from "~/api/client";
import BookmarksPanel from "~/components/reader/BookmarksPanel";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

const CREATED = "2026-08-05T00:00:00Z";

function bm(
  id: string,
  chapter: number,
  percent: number,
  label = "",
  comment = "",
): Bookmark {
  return { id, chapter, percent, label, comment, createdAt: CREATED };
}

// Deliberately unsorted; each name exercises one bookmarkName branch.
function fixture(): Bookmark[] {
  return [
    bm("d", 2, 0.5, "Gamma"),
    bm("a", 0, 0.5, "", "nice spot"),
    bm("b", 1, 0.1),
    bm("c", 2, 0.25),
  ];
}

describe("BookmarksPanel", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let onnavigate: ReturnType<typeof vi.fn>;
  let ondelete: ReturnType<typeof vi.fn>;
  let onupdate: ReturnType<typeof vi.fn>;
  let onclose: ReturnType<typeof vi.fn>;
  let list: Bookmark[];

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    onnavigate = vi.fn();
    ondelete = vi.fn();
    onupdate = vi.fn();
    onclose = vi.fn();
    list = fixture();
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    vi.restoreAllMocks();
  });

  async function mount(): Promise<void> {
    dispose = render(
      () =>
        BookmarksPanel({
          bookmarks: list,
          chapterTitle: (chapter: number) => (chapter === 0 ? "Opening" : null),
          onnavigate: onnavigate as (b: Bookmark) => void,
          ondelete: ondelete as (id: string) => void,
          onupdate: onupdate as (
            id: string,
            label: string,
            comment: string,
          ) => void,
          onclose: onclose as () => void,
        }),
      container,
    );
    await settle();
  }

  const rows = (): HTMLButtonElement[] =>
    Array.from(container.querySelectorAll<HTMLButtonElement>(".bmp-open"));
  const rowNames = (): string[] =>
    Array.from(container.querySelectorAll<HTMLElement>(".bmp-label")).map(
      (el) => el.textContent ?? "",
    );
  const editBtn = (name: string): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(
      `button[aria-label="Edit bookmark: ${name}"]`,
    )!;
  const labelField = (): HTMLInputElement | null =>
    container.querySelector<HTMLInputElement>(".bmp-edit input.field");
  const noteField = (): HTMLTextAreaElement | null =>
    container.querySelector<HTMLTextAreaElement>(".bmp-edit textarea.field");
  const errEl = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".bmp-edit-error");
  const saveBtn = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".bmp-edit-actions .btn")!;

  function key(target: EventTarget, init: KeyboardEventInit): void {
    target.dispatchEvent(
      new KeyboardEvent("keydown", {
        bubbles: true,
        cancelable: true,
        ...init,
      }),
    );
  }

  function typeInto(
    field: HTMLInputElement | HTMLTextAreaElement,
    value: string,
  ): void {
    field.value = value;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  it("renders rows sorted by chapter, then position", async () => {
    await mount();
    expect(rowNames()).toEqual(["Opening", "Chapter 2", "Chapter 3", "Gamma"]);
  });

  it("resolves names: label, then TOC heading, then Chapter N", async () => {
    await mount();
    expect(editBtn("Gamma")).not.toBeNull();
    expect(editBtn("Opening")).not.toBeNull();
    expect(editBtn("Chapter 2")).not.toBeNull();
    expect(rows()[0]!.getAttribute("aria-label")).toContain("chapter 1, 50%");
  });

  it("navigates on row activation and deletes on demand", async () => {
    await mount();
    rows()[0]!.click();
    expect(onnavigate).toHaveBeenCalledWith(list[1]);
    container
      .querySelector<HTMLButtonElement>(
        'button[aria-label="Delete bookmark: Gamma"]',
      )!
      .click();
    expect(ondelete).toHaveBeenCalledWith("d");
  });

  it("closes from the header button", async () => {
    await mount();
    container.querySelector<HTMLButtonElement>(".bmp-close")!.click();
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("shows the empty state when the book has no bookmarks", async () => {
    list = [];
    await mount();
    expect(container.querySelector(".bmp-items")).toBeNull();
    expect(container.querySelector(".bmp-empty")?.textContent).toContain(
      "No bookmarks yet",
    );
  });

  it("moves focus to the label field when a row enters edit mode", async () => {
    await mount();
    editBtn("Gamma").click();
    await settle();
    const field = labelField();
    expect(field).not.toBeNull();
    expect(field!.value).toBe("Gamma");
    expect(document.activeElement).toBe(field);
  });

  it("returns focus to the row's edit button after a save", async () => {
    await mount();
    const btn = editBtn("Gamma");
    btn.click();
    await settle();
    saveBtn().click();
    await settle();
    expect(onupdate).toHaveBeenCalledWith("d", "Gamma", "");
    expect(labelField()).toBeNull();
    const after = editBtn("Gamma");
    expect(document.activeElement).toBe(after);
    expect(after).not.toBe(btn);
  });

  it("returns focus to the row's edit button after a cancel", async () => {
    await mount();
    editBtn("Chapter 3").click();
    await settle();
    key(labelField()!, { key: "Escape" });
    await settle();
    expect(labelField()).toBeNull();
    expect(onupdate).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(editBtn("Chapter 3"));
  });

  it("saves on Enter in the label field but not in the note", async () => {
    await mount();
    editBtn("Chapter 3").click();
    await settle();
    typeInto(noteField()!, "line one");
    key(noteField()!, { key: "Enter" });
    await settle();
    expect(onupdate).not.toHaveBeenCalled();
    expect(labelField()).not.toBeNull();
    typeInto(labelField()!, "Marked");
    key(labelField()!, { key: "Enter" });
    await settle();
    expect(onupdate).toHaveBeenCalledWith("c", "Marked", "line one");
  });

  it("rejects an over-cap label with a byte-counted alert, cleared on input", async () => {
    await mount();
    editBtn("Chapter 3").click();
    await settle();
    // 1001 CJK chars = 3003 UTF-8 bytes, over the 2000-byte cap the server
    // enforces in internal/api/bookmarks.go. maxlength counts UTF-16 units,
    // so this value types fine and only the byte check can stop it.
    typeInto(labelField()!, "\u5b57".repeat(1001));
    saveBtn().click();
    await settle();
    expect(onupdate).not.toHaveBeenCalled();
    const err = errEl();
    expect(err?.getAttribute("role")).toBe("alert");
    expect(err?.textContent).toContain("3003");
    expect(labelField()?.getAttribute("aria-describedby")).toBe(
      err?.id ?? null,
    );
    typeInto(labelField()!, "short");
    await settle();
    expect(errEl()).toBeNull();
  });

  it("rejects an over-cap note with its own wording", async () => {
    await mount();
    editBtn("Chapter 3").click();
    await settle();
    typeInto(noteField()!, "\u5b57".repeat(1001));
    saveBtn().click();
    await settle();
    expect(onupdate).not.toHaveBeenCalled();
    expect(errEl()?.textContent).toContain("Note must be");
  });

  it("does not treat an IME composition Escape as a cancel", async () => {
    await mount();
    editBtn("Gamma").click();
    await settle();
    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    // Guards the environment as much as the component: if the flag does not
    // survive construction, this test proves nothing about the guard.
    expect(composing.isComposing).toBe(true);
    labelField()!.dispatchEvent(composing);
    await settle();
    expect(labelField()).not.toBeNull();
    expect(onupdate).not.toHaveBeenCalled();
  });

  it("keeps a real Escape from bubbling to the reader's window handler", async () => {
    await mount();
    editBtn("Gamma").click();
    await settle();
    const onWindow = vi.fn();
    window.addEventListener("keydown", onWindow);
    key(labelField()!, { key: "Escape" });
    await settle();
    window.removeEventListener("keydown", onWindow);
    expect(onWindow).not.toHaveBeenCalled();
    expect(labelField()).toBeNull();
  });
});
