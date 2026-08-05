// Suite for the edit-book dialog. Only the two side-effecting stores are
// stubbed -- library (network) and toast (global UI). focusTrap is deliberately
// real: the focus invariant below is a statement about who wins between the
// trap's fallback and the dialog's own intent, so stubbing it would assert
// nothing.
//
// The invariants, each of which regressed silently before b30:
//   - Focus opens on the title field. A self-focusing ref could not do it,
//     because refs run while the node is still detached, so the trap's fallback
//     took the first focusable in the sheet -- the header close button, where
//     Enter dismisses the dialog the reader had just opened.
//   - The title error paragraph is mounted from first paint and only its text
//     and hidden state change. A <Show> whose condition is already true at
//     first paint never removes its child, so a book with no title (the
//     importer allows one; PATCH then refuses it) kept the error on screen for
//     the rest of the session, contradicting aria-invalid="false".
//   - Announcements come from one pre-mounted .sr-only region. A live region
//     inserted in the same tick as its text is not announced (b27), so the
//     visible paragraphs carry no role="alert" and the region exists before it
//     has anything to say.
//   - Save is aria-disabled, never disabled: the real attribute blurs the
//     button mid-request and strands a keyboard user outside the dialog, and
//     the same holds for the fields, which go readonly instead. The guard that
//     keeps one activation to one request therefore lives in submit(), pinned
//     here by a second activation while the first is still in flight.
//   - Escape that belongs to an IME composition is not a dismissal.
//   - A rejected cover pick reports itself without discarding an already
//     staged valid image.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type { BookMeta } from "~/api/client";

const stubs = vi.hoisted(() => ({
  editMetadata: vi.fn(),
  replaceCover: vi.fn(),
  toasts: [] as string[],
}));

vi.mock("~/lib/library", () => ({
  library: {
    editMetadata: stubs.editMetadata,
    replaceCover: stubs.replaceCover,
  },
}));

vi.mock("~/lib/toast", () => ({
  toast: {
    show: (message: string) => {
      stubs.toasts.push(message);
    },
  },
}));

import EditBookDialog from "~/components/library/EditBookDialog";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function book(over: Partial<BookMeta> = {}): BookMeta {
  return {
    id: "bk-1",
    title: "The Left Hand of Darkness",
    author: "Ursula K. Le Guin",
    language: "en",
    publisher: "Ace",
    description: "",
    pubDate: "1969",
    hasCover: true,
    direction: "ltr",
    chapterCount: 24,
    progress: 0.42,
    updatedAt: "2026-08-05 00:00:00",
    ...over,
  };
}

describe("EditBookDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let closes: number;
  let created: string[];
  let revoked: string[];

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    closes = 0;
    created = [];
    revoked = [];
    stubs.editMetadata.mockReset();
    stubs.replaceCover.mockReset();
    stubs.editMetadata.mockResolvedValue(undefined);
    stubs.replaceCover.mockResolvedValue(undefined);
    stubs.toasts.length = 0;
    // Spies rather than assignment: saving URL.createObjectURL by reference is
    // an unbound method read, and restoreAllMocks puts both statics back.
    vi.spyOn(URL, "createObjectURL").mockImplementation((object) => {
      const url = `blob:${object instanceof File ? object.name : "object"}`;
      created.push(url);
      return url;
    });
    vi.spyOn(URL, "revokeObjectURL").mockImplementation((url) => {
      revoked.push(url);
    });
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    vi.restoreAllMocks();
  });

  async function mount(over: Partial<BookMeta> = {}): Promise<void> {
    const meta = book(over);
    dispose = render(
      () =>
        EditBookDialog({
          book: meta,
          onclose: () => {
            closes += 1;
          },
        }),
      container,
    );
    await settle();
  }

  const textInputs = (): HTMLInputElement[] =>
    Array.from(
      container.querySelectorAll<HTMLInputElement>('input[type="text"]'),
    );
  const titleInput = (): HTMLInputElement => textInputs()[0]!;
  const authorInput = (): HTMLInputElement => textInputs()[1]!;
  const closeButton = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".eb-close")!;
  const saveButton = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".eb-save")!;
  const titleNote = (): HTMLElement | null =>
    container.querySelector<HTMLElement>("#book-title-error");
  const authorNote = (): HTMLElement | null =>
    container.querySelector<HTMLElement>("#book-author-error");
  const liveRegion = (): HTMLElement | null =>
    container.querySelector<HTMLElement>('p.sr-only[role="alert"]');
  const fileInput = (): HTMLInputElement =>
    container.querySelector<HTMLInputElement>(".eb-file-input")!;
  const coverName = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".eb-cover-name");
  const coverError = (): HTMLElement | null =>
    container.querySelector<HTMLElement>("#cover-pick-error");

  function type(input: HTMLInputElement, value: string): void {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function pick(files: File[]): void {
    const input = fileInput();
    Object.defineProperty(input, "files", { value: files, configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  }

  it("opens with focus in the title field, not on the close button", async () => {
    await mount();

    expect(closeButton()).not.toBeNull();
    expect(document.activeElement).toBe(titleInput());
    expect(document.activeElement).not.toBe(closeButton());
  });

  it("keeps the title error mounted and empties it once the title is valid", async () => {
    await mount({ title: "" });

    const note = titleNote();
    expect(note).not.toBeNull();
    expect(note!.hasAttribute("hidden")).toBe(false);
    expect(note!.textContent).toContain("Title");
    expect(titleInput().getAttribute("aria-invalid")).toBe("true");
    expect(titleInput().getAttribute("aria-describedby")).toBe(
      "book-title-error",
    );

    type(titleInput(), "A Wizard of Earthsea");
    await settle();

    // Same node throughout: the paragraph is never conditionally mounted.
    expect(titleNote()).toBe(note);
    expect(note!.hasAttribute("hidden")).toBe(true);
    expect(note!.textContent).toBe("");
    expect(titleInput().getAttribute("aria-invalid")).toBe("false");
    expect(titleInput().getAttribute("aria-describedby")).toBeNull();
  });

  it("announces from a region that exists before it has text", async () => {
    await mount();

    const region = liveRegion();
    expect(region).not.toBeNull();
    expect(region!.textContent).toBe("");
    expect(titleNote()?.getAttribute("role")).toBeNull();
    expect(authorNote()?.getAttribute("role")).toBeNull();

    type(titleInput(), "   ");
    await settle();

    expect(liveRegion()).toBe(region);
    expect(region!.textContent).toContain("Title");
  });

  it("marks save aria-disabled rather than disabled, and admits one submission", async () => {
    await mount();

    expect(saveButton().hasAttribute("disabled")).toBe(false);
    expect(saveButton().getAttribute("aria-disabled")).toBe("true");

    type(titleInput(), "Tehanu");
    await settle();
    expect(saveButton().getAttribute("aria-disabled")).toBe("false");

    let release: (() => void) | undefined;
    stubs.editMetadata.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          release = () => resolve();
        }),
    );

    saveButton().click();
    await settle();

    expect(stubs.editMetadata).toHaveBeenCalledTimes(1);
    expect(saveButton().hasAttribute("disabled")).toBe(false);
    expect(saveButton().getAttribute("aria-disabled")).toBe("true");
    expect(saveButton().textContent).toContain("Saving");
    expect(titleInput().hasAttribute("disabled")).toBe(false);
    expect(titleInput().hasAttribute("readonly")).toBe(true);
    expect(titleInput().getAttribute("aria-disabled")).toBe("true");
    expect(authorInput().hasAttribute("disabled")).toBe(false);
    expect(authorInput().hasAttribute("readonly")).toBe(true);

    // Second activation while the first request is still open.
    saveButton().click();
    await settle();
    expect(stubs.editMetadata).toHaveBeenCalledTimes(1);

    release?.();
    await settle();

    expect(stubs.toasts).toEqual(["Saved changes"]);
    expect(closes).toBe(1);
  });

  it("ignores Escape that belongs to an IME composition", async () => {
    await mount();

    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    // Guards the environment as much as the component: if the flag does not
    // survive construction here, this test proves nothing about the guard.
    expect(composing.isComposing).toBe(true);

    window.dispatchEvent(composing);
    await settle();
    expect(closes).toBe(0);

    window.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }),
    );
    await settle();
    expect(closes).toBe(1);
  });

  it("keeps a staged cover when a later pick is rejected", async () => {
    await mount();

    pick([new File(["png"], "cover.png", { type: "image/png" })]);
    await settle();
    expect(coverName()?.textContent).toContain("cover.png");
    expect(created).toEqual(["blob:cover.png"]);

    pick([new File(["gif"], "nope.gif", { type: "image/gif" })]);
    await settle();

    expect(coverError()?.textContent).toContain("JPEG");
    expect(coverName()?.textContent).toContain("cover.png");
    expect(revoked).toEqual([]);
  });

  it("shows the author limit note only while the author is too long", async () => {
    await mount();

    const note = authorNote();
    expect(note).not.toBeNull();
    expect(note!.hasAttribute("hidden")).toBe(true);
    expect(authorInput().getAttribute("aria-invalid")).toBe("false");

    type(authorInput(), "x".repeat(513));
    await settle();

    expect(authorNote()).toBe(note);
    expect(note!.hasAttribute("hidden")).toBe(false);
    expect(note!.textContent).toContain("512-byte");
    expect(authorInput().getAttribute("aria-invalid")).toBe("true");
    expect(authorInput().getAttribute("aria-describedby")).toBe(
      "book-author-error",
    );
    expect(saveButton().getAttribute("aria-disabled")).toBe("true");
    expect(liveRegion()!.textContent).toContain("512-byte");
  });
});
