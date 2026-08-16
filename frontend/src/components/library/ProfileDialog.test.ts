// Suite for the clone/delete profile dialog. Only the side-effecting
// boundaries are stubbed -- the api client (listProfiles alone, spread over
// importOriginal so ApiError stays the real class), the session store, and
// toast. focusTrap is deliberately real: the focus invariant below is a
// statement about who wins between the trap's fallback and the dialog's own
// intent, so stubbing it would assert nothing.
//
// The invariants, each of which regressed silently before this suite existed:
//   - Focus opens on the field the mode exists for (the name field for clone,
//     the confirm field for delete) -- never on the header close button,
//     where Enter dismisses the dialog the reader had just opened. A
//     self-focusing ref could not do it, because refs run while the node is
//     still detached, so the trap's fallback took the first focusable.
//   - Announcements come from one pre-mounted .sr-only region. A live region
//     inserted in the same tick as its text is not announced, so the
//     visible paragraphs carry no role="alert" and the region exists before
//     it has anything to say.
//   - A Windows device name is rejected client-side with its own message;
//     the server's blanket regex message names rules the name satisfies.
//   - A successful delete owns its teardown -- toast, unblock, close --
//     instead of delegating it to an unmount deleteCurrent does not promise.
//   - The mount fetch is aborted on dispose, and a PIN-probe failure fails
//     closed: no PIN field, no enabled delete, a visible retry.
//   - Capture-phase Escape leaves an active IME composition untouched.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { ApiError } from "~/api/client";

const stubs = vi.hoisted(() => ({
  listProfiles: vi.fn(),
  clone: vi.fn(),
  deleteCurrent: vi.fn(),
  currentHasPin: vi.fn(),
  toasts: [] as string[],
}));

vi.mock("~/api/client", async (importOriginal) => {
  // importOriginal + spread, per Login.test.ts: a bare factory would replace
  // ApiError with a twin and turn every instanceof branch into dead code.
  const actual = await importOriginal<Record<string, unknown>>();
  return { ...actual, listProfiles: stubs.listProfiles };
});

vi.mock("~/lib/session", () => ({
  session: {
    clone: stubs.clone,
    deleteCurrent: stubs.deleteCurrent,
    currentHasPin: stubs.currentHasPin,
  },
}));

vi.mock("~/lib/toast", () => ({
  toast: {
    show: (message: string) => {
      stubs.toasts.push(message);
    },
  },
}));

import ProfileDialog from "~/components/library/ProfileDialog";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}
describe("ProfileDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let closes: number;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    closes = 0;
    stubs.listProfiles.mockReset();
    stubs.clone.mockReset();
    stubs.deleteCurrent.mockReset();
    stubs.currentHasPin.mockReset();
    stubs.listProfiles.mockResolvedValue([
      { name: "Alice", hasPin: true },
      { name: "Bob", hasPin: false },
    ]);
    stubs.clone.mockResolvedValue(undefined);
    stubs.deleteCurrent.mockResolvedValue(undefined);
    stubs.currentHasPin.mockResolvedValue(false);
    stubs.toasts.length = 0;
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
  });

  async function mount(mode: "clone" | "delete"): Promise<void> {
    dispose = render(
      () =>
        ProfileDialog({
          mode,
          profileName: "Alice",
          onclose: () => {
            closes += 1;
          },
        }),
      container,
    );
    await settle();
  }

  const sheet = (): HTMLElement =>
    container.querySelector<HTMLElement>(".pd-sheet")!;
  const closeButton = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".pd-close")!;
  const nameInput = (): HTMLInputElement =>
    container.querySelector<HTMLInputElement>("input[maxlength='32']")!;
  const confirmInput = (): HTMLInputElement =>
    container.querySelector<HTMLInputElement>("input[autocomplete='off']")!;
  const submitButton = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>("button[type='submit']")!;
  const liveRegion = (): HTMLElement | null =>
    container.querySelector<HTMLElement>('p.sr-only[role="alert"]');
  const nameNote = (): HTMLElement | null =>
    container.querySelector<HTMLElement>("#profile-name-error");
  const retryButton = (): HTMLButtonElement | null =>
    container.querySelector<HTMLButtonElement>(".pd-retry");

  function type(input: HTMLInputElement, value: string): void {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  it("opens with focus in the name field, not on the close button", async () => {
    await mount("clone");

    expect(sheet().contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(nameInput());
    expect(document.activeElement).not.toBe(closeButton());
  });

  it("opens with focus in the confirm field in delete mode", async () => {
    await mount("delete");

    expect(sheet().contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(confirmInput());
    expect(document.activeElement).not.toBe(closeButton());
  });

  it("announces from a region that exists before it has text", async () => {
    await mount("clone");

    const region = liveRegion();
    expect(region).not.toBeNull();
    expect(region!.textContent).toBe("");
    expect(nameNote()).toBeNull();

    type(nameInput(), "-bad");
    await settle();

    // Same node throughout: the region was mounted from first paint, and
    // the visible note it mirrors carries no live role of its own.
    expect(liveRegion()).toBe(region);
    expect(region!.textContent).toContain("letters, digits");
    expect(nameNote()!.getAttribute("role")).toBeNull();
  });

  it("rejects a Windows device name the regex alone would pass", async () => {
    await mount("clone");

    type(nameInput(), "com1");
    await settle();

    expect(nameNote()!.textContent).toContain("Windows reserves");
    expect(nameInput().getAttribute("aria-invalid")).toBe("true");
    expect(submitButton().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().hasAttribute("disabled")).toBe(false);

    type(nameInput(), "copy of Alice");
    await settle();
    expect(nameNote()).toBeNull();
    expect(submitButton().getAttribute("aria-disabled")).toBe("false");
  });

  it("rejects a case-only duplicate of an existing profile", async () => {
    await mount("clone");

    type(nameInput(), "ALICE");
    await settle();

    expect(nameNote()!.textContent).toContain("already taken");
    expect(submitButton().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().hasAttribute("disabled")).toBe(false);
  });

  it("closes and unblocks itself after a successful delete", async () => {
    await mount("delete");

    type(confirmInput(), "Alice");
    await settle();
    expect(submitButton().getAttribute("aria-disabled")).toBe("false");

    submitButton().click();
    await settle();

    expect(stubs.deleteCurrent).toHaveBeenCalledTimes(1);
    expect(stubs.toasts).toEqual(["Deleted profile “Alice”"]);
    // The dialog owns its teardown: deleteCurrent resolving is not a promise
    // that the session cleared or that anything unmounts this dialog.
    expect(closes).toBe(1);
    expect(submitButton().getAttribute("aria-disabled")).toBe("false");
    expect(closeButton().getAttribute("aria-disabled")).toBe("false");
  });

  // A failed clone has to show what the server said, not a house fallback that
  // hides it. Nothing pinned this: the getErrorMessage call could collapse to
  // the bare fallback string and every other assertion in this file still
  // passed.
  it("shows the server's own message when the clone fails", async () => {
    await mount("clone");
    type(nameInput(), "Zephyrine");
    await settle();

    stubs.clone.mockRejectedValue(
      new ApiError("Disk is full", 507, "insufficient_storage"),
    );

    submitButton().click();
    await settle();

    expect(container.textContent).toContain("Disk is full");
    expect(stubs.toasts).toEqual([]);
    expect(closes).toBe(0);
  });

  it("closes and consumes an ordinary Escape", async () => {
    await mount("clone");
    let bubbled = false;
    const bubbleSpy = (): void => {
      bubbled = true;
    };
    window.addEventListener("keydown", bubbleSpy);
    const escape = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });

    nameInput().dispatchEvent(escape);
    await settle();
    window.removeEventListener("keydown", bubbleSpy);

    expect(closes).toBe(1);
    expect(escape.defaultPrevented).toBe(true);
    expect(bubbled).toBe(false);
  });

  it("leaves a composing Escape with the profile field", async () => {
    await mount("clone");
    let bubbled = false;
    const bubbleSpy = (): void => {
      bubbled = true;
    };
    window.addEventListener("keydown", bubbleSpy);
    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    expect(composing.isComposing).toBe(true);

    nameInput().dispatchEvent(composing);
    await settle();
    window.removeEventListener("keydown", bubbleSpy);

    expect(closes).toBe(0);
    expect(composing.defaultPrevented).toBe(false);
    expect(bubbled).toBe(true);
  });

  it("aborts the names fetch when the dialog unmounts mid-flight", async () => {
    stubs.listProfiles.mockImplementation(
      (_signal?: AbortSignal) => new Promise(() => {}),
    );
    await mount("clone");

    const signal = stubs.listProfiles.mock.calls[0]?.[0] as
      | AbortSignal
      | undefined;
    expect(signal).toBeInstanceOf(AbortSignal);
    expect(signal!.aborted).toBe(false);

    dispose?.();
    dispose = undefined;

    expect(signal!.aborted).toBe(true);
  });

  it("fails closed when the PIN probe fails, and recovers on Retry", async () => {
    stubs.currentHasPin.mockRejectedValue(
      new ApiError("boom", 500, "server_error"),
    );
    await mount("delete");

    // No PIN field, no enabled delete, and the failure is visible twice:
    // the pre-mounted region and the retry affordance.
    expect(container.querySelectorAll('input[type="password"]').length).toBe(0);
    expect(liveRegion()!.textContent).toBe("boom");
    expect(retryButton()).not.toBeNull();

    type(confirmInput(), "Alice");
    await settle();
    expect(submitButton().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().hasAttribute("disabled")).toBe(false);

    stubs.currentHasPin.mockResolvedValue(true);
    retryButton()!.click();
    await settle();

    expect(container.querySelectorAll('input[type="password"]').length).toBe(1);
    expect(liveRegion()!.textContent).toBe("");
  });

  it("keeps busy controls focusable and inert while a clone is in flight", async () => {
    let releaseClone!: () => void;
    stubs.clone.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          releaseClone = () => resolve();
        }),
    );
    await mount("clone");
    type(nameInput(), "Alina");
    await settle();
    expect(submitButton().getAttribute("aria-disabled")).toBe("false");

    submitButton().click();
    await settle();
    expect(stubs.clone).toHaveBeenCalledTimes(1);

    // aria-disabled + readonly, never disabled: every control keeps its
    // tab-order place for the whole request, and the guards do the refusing.
    expect(nameInput().readOnly).toBe(true);
    expect(nameInput().disabled).toBe(false);
    expect(nameInput().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().hasAttribute("disabled")).toBe(false);
    expect(closeButton().getAttribute("aria-disabled")).toBe("true");
    const cancel = container.querySelector<HTMLButtonElement>(
      ".pd-actions .btn-ghost",
    )!;
    expect(cancel.getAttribute("aria-disabled")).toBe("true");
    cancel.click();
    closeButton().click();
    await settle();
    expect(closes).toBe(0);

    releaseClone();
    await settle();
    expect(closes).toBe(1);
  });

  it("marks the confirm field readonly, never disabled, while a delete runs", async () => {
    let releaseDelete!: () => void;
    stubs.deleteCurrent.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          releaseDelete = () => resolve();
        }),
    );
    await mount("delete");
    type(confirmInput(), "Alice");
    await settle();

    submitButton().click();
    await settle();
    expect(stubs.deleteCurrent).toHaveBeenCalledTimes(1);
    expect(confirmInput().readOnly).toBe(true);
    expect(confirmInput().disabled).toBe(false);
    expect(confirmInput().getAttribute("aria-disabled")).toBe("true");
    expect(submitButton().getAttribute("aria-disabled")).toBe("true");

    releaseDelete();
    await settle();
    expect(closes).toBe(1);
  });
});
