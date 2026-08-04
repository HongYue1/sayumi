// Ported from the Svelte Login.test.ts. Renders the component with
// @solidjs/web's render directly -- NOT @solidjs/testing-library, whose dist
// imports the removed "solid-js/web" specifier and dies at suite collection
// under Solid 2.0. Events are dispatched by hand (as the Svelte version did);
// `flush` forces Solid 2.0's batched writes so assertions see committed
// state. This file never calls vi.resetModules(), so a statically imported
// flush drives the same scheduler instance as the component under test.
//
// The client mock spreads importOriginal instead of hand-rolling an ApiError
// twin. A local twin is never constructed by the route, so every
// `e instanceof ApiError` branch becomes dead code and the tests only ever
// pin the fallback copy -- and a bare factory silently drops every other
// export of the module. Only the two functions this route calls are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { ApiError, type ProfileInfo } from "~/api/client";
import Login from "~/routes/Login";

const api = vi.hoisted(() => ({
  listProfiles: vi.fn(),
  createProfile: vi.fn(),
  login: vi.fn(),
}));

// The route bails out of its post-await continuation when the session has
// authenticated underneath it, so the mock needs a live view of that flag.
const sessionState = vi.hoisted(() => ({ authenticated: false }));

vi.mock("~/api/client", async (importOriginal) => {
  // A Record rather than `typeof import(...)`: oxlint forbids import() type
  // annotations, and nothing here needs the precise module type.
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    listProfiles: api.listProfiles,
    createProfile: api.createProfile,
  };
});

vi.mock("~/lib/session", () => ({
  session: {
    login: api.login,
    get authenticated() {
      return sessionState.authenticated;
    },
  },
}));

describe("Login route", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;

  function mount(): void {
    container = document.createElement("div");
    document.body.append(container);
    dispose = render(Login, container);
  }

  function q(selector: string): HTMLElement | null {
    return container.querySelector<HTMLElement>(selector);
  }

  function qi(selector: string): HTMLInputElement | null {
    return container.querySelector<HTMLInputElement>(selector);
  }

  function qb(selector: string): HTMLButtonElement | null {
    return container.querySelector<HTMLButtonElement>(selector);
  }

  async function waitForEl<T extends Element>(selector: string): Promise<T> {
    await vi.waitFor(() => {
      expect(container.querySelector(selector)).not.toBeNull();
    });
    flush();
    return container.querySelector<T>(selector)!;
  }

  function typeInto(input: HTMLInputElement, value: string): void {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  function tickCheckbox(): void {
    const box = qi('input[type="checkbox"]')!;
    box.checked = true;
    box.dispatchEvent(new Event("change", { bubbles: true }));
    flush();
  }

  function submitForm(): SubmitEvent {
    const event = new SubmitEvent("submit", {
      bubbles: true,
      cancelable: true,
    });
    container.querySelector<HTMLFormElement>("form")!.dispatchEvent(event);
    return event;
  }

  function profileButtons(): HTMLButtonElement[] {
    return Array.from(
      container.querySelectorAll<HTMLButtonElement>("button.login-profile"),
    );
  }

  beforeEach(() => {
    // A fake clock leaked by another suite in this worker (fake timers also
    // fake queueMicrotask) would starve Solid's batch flush and freeze the
    // component mid-render. Never trust ambient timer state.
    vi.useRealTimers();
    sessionState.authenticated = false;
    api.listProfiles.mockReset();
    api.createProfile.mockReset();
    api.login.mockReset();
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
  });

  it("Login route: live regions stay mounted (H1)", async () => {
    let settle: (value: ProfileInfo[]) => void = () => {};
    api.listProfiles.mockImplementation(
      () =>
        new Promise<ProfileInfo[]>((resolve) => {
          settle = resolve;
        }),
    );
    mount();

    const status = await waitForEl<HTMLOutputElement>("output.login-live");
    const alert = await waitForEl<HTMLParagraphElement>(
      'p.login-error[role="alert"]',
    );
    expect(status.textContent).toBe("Loading profiles…");

    settle([]);
    await vi.waitFor(() => expect(status.textContent).toBe(""));

    // Same nodes as before: text swaps inside a mounted region, which is what
    // gets announced. A region that appears together with its content does not.
    expect(q("output.login-live")).toBe(status);
    expect(q('p.login-error[role="alert"]')).toBe(alert);
    expect(alert.textContent).toBe("");
  });

  it("Login route: failed list offers a retry, not an empty picker (H4, T5)", async () => {
    api.listProfiles
      .mockRejectedValueOnce(
        new ApiError("failed to list profiles", 500, "db_error"),
      )
      .mockResolvedValueOnce([{ name: "Ann", hasPin: false }]);
    mount();

    const retry = await waitForEl<HTMLButtonElement>("button.login-primary");
    expect(retry.textContent).toContain("Try again");
    // The real ApiError reaches the region; a hand-rolled twin never did.
    expect(q("p.login-error")!.textContent).toContain(
      "failed to list profiles",
    );
    expect(container.textContent).not.toContain("Welcome");

    retry.click();
    flush();
    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    expect(api.listProfiles).toHaveBeenCalledTimes(2);
  });

  it("Login route: PIN-less profile signs in on click (T3)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Ann", hasPin: false }]);
    api.login.mockResolvedValue(undefined);
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    profileButtons()[0].click();
    flush();

    await vi.waitFor(() => expect(api.login).toHaveBeenCalledTimes(1));
    expect(api.login).toHaveBeenCalledWith("Ann", "", false);
  });

  it("Login route: locked profile opens the PIN form (T3, M5)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Bea", hasPin: true }]);
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    profileButtons()[0].click();
    flush();

    const pin = await waitForEl<HTMLInputElement>('input[aria-label="PIN"]');
    expect(api.login).not.toHaveBeenCalled();
    expect(pin.getAttribute("autocomplete")).toBe("current-password");
    // Identity field, or the browser offers profile A's saved PIN to B.
    expect(qi('input[autocomplete="username"]')!.value).toBe("Bea");
  });

  it("Login route: created profile is appended, not clobbered (T4, T7, T15, H5)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Ann", hasPin: false }]);
    api.createProfile.mockResolvedValue({ name: "Reader" });
    api.login
      .mockRejectedValueOnce(
        new ApiError("offline", undefined, "network_error"),
      )
      .mockResolvedValueOnce(undefined);
    mount();

    // Reach the create form from a NON-empty picker: with an empty list the
    // append-vs-clobber assertion is vacuous.
    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    qb("button.login-new")!.click();
    flush();

    const name = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    typeInto(name, "  Reader  ");
    // preventDefault is the only thing standing between submit and a full
    // page navigation that would drop the whole SPA.
    expect(submitForm().defaultPrevented).toBe(true);

    await vi.waitFor(() => {
      expect(container.textContent).toContain(
        "Profile created, but sign-in failed",
      );
    });
    expect(api.createProfile).toHaveBeenCalledOnce();
    expect(api.createProfile.mock.calls[0][0]).toBe("Reader");
    expect(api.createProfile.mock.calls[0][1]).toBe("");
    expect(api.login).toHaveBeenNthCalledWith(1, "Reader", "", false);
    expect(profileButtons()).toHaveLength(2);

    // Distinguishable second call: the shared flag has to reach a PIN-less
    // retry, which is impossible while the checkbox lives in the PIN form.
    tickCheckbox();
    const retry = profileButtons().find((b) =>
      b.textContent?.includes("Reader"),
    )!;
    expect(retry).toBeDefined();
    retry.click();
    flush();

    await vi.waitFor(() => expect(api.login).toHaveBeenCalledTimes(2));
    expect(api.login).toHaveBeenNthCalledWith(2, "Reader", "", true);
    expect(api.createProfile).toHaveBeenCalledOnce();
  });

  it("Login route: rejected PIN is reported, cleared, and refocused (T2, M7, L3, H2)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Bea", hasPin: true }]);
    api.login.mockRejectedValue(
      new ApiError("invalid name or PIN", 401, "invalid_credentials"),
    );
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    profileButtons()[0].click();
    flush();

    const pin = await waitForEl<HTMLInputElement>('input[aria-label="PIN"]');
    typeInto(pin, "1234");
    const submit = qb("button.login-primary")!;
    submit.focus();
    expect(submitForm().defaultPrevented).toBe(true);

    await vi.waitFor(() => {
      expect(q("p.login-error")!.textContent).toContain(
        "That PIN did not match",
      );
    });
    expect(qi('input[aria-label="PIN"]')!.value).toBe("");
    expect(submit.disabled).toBe(false);
    await vi.waitFor(() => expect(document.activeElement).toBe(submit));
  });

  it("Login route: blocked submit is never silent (L9, M8, T9)", async () => {
    api.listProfiles.mockResolvedValue([]);
    mount();

    const name = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    const submit = qb("button.login-primary")!;
    // aria-disabled, not disabled: the button keeps its place in the tab order
    // and Enter says why nothing happened.
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-disabled")).toBe("true");
    submitForm();
    await vi.waitFor(() => {
      expect(q("p.login-error")!.textContent).toContain("Enter a profile name");
    });
    expect(api.createProfile).not.toHaveBeenCalled();

    // Names the Go validator rejects are caught before the round trip.
    typeInto(name, "_bob");
    expect(container.textContent).toContain("starting and ending");
    submitForm();
    expect(api.createProfile).not.toHaveBeenCalled();

    typeInto(name, "nul");
    expect(container.textContent).toContain("reserves for a device");
    submitForm();
    expect(api.createProfile).not.toHaveBeenCalled();

    typeInto(name, "Reader");
    expect(submit.getAttribute("aria-disabled")).toBe("false");
  });

  it("Login route: returning user lands on the picker (T10, L8)", async () => {
    api.listProfiles.mockResolvedValue([
      { name: "Ann", hasPin: false },
      { name: "Bea", hasPin: true },
    ]);
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(2));
    expect(container.textContent).toContain("Choose a profile to continue");
    expect(q('input[aria-label="Profile name"]')).toBeNull();
    expect(q("ul.login-profiles")!.getAttribute("aria-label")).toBe("Profiles");
    expect(q('svg[aria-label="PIN protected"]')).not.toBeNull();
  });

  it("Login route: Back returns to the picker and drops the flag (T11, H5, M10)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Bea", hasPin: true }]);
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    profileButtons()[0].click();
    flush();

    const pin = await waitForEl<HTMLInputElement>('input[aria-label="PIN"]');
    typeInto(pin, "1234");
    tickCheckbox();

    qb("button.login-back")!.click();
    flush();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    expect(q('input[aria-label="PIN"]')).toBeNull();
    expect(qi('input[type="checkbox"]')!.checked).toBe(false);
    // Focus follows the backward transition instead of dropping to <body>.
    await vi.waitFor(() => {
      expect(document.activeElement).toBe(q("p.login-muted"));
    });
  });

  it("Login route: forward transitions focus their first field (T13)", async () => {
    api.listProfiles.mockResolvedValue([]);
    mount();

    const name = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    await vi.waitFor(() => expect(document.activeElement).toBe(name));
  });

  it("Login route: an in-flight sign-in freezes the picker (H3)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Ann", hasPin: false }]);
    api.login.mockImplementation(() => new Promise(() => {}));
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    profileButtons()[0].click();
    flush();

    await vi.waitFor(() => {
      expect(qb("button.login-new")!.disabled).toBe(true);
    });
    expect(profileButtons()[0].disabled).toBe(true);
    expect(q("output.login-live")!.textContent).toBe("Signing in…");

    // The plain in-flight flag also rejects a second submit in the same tick,
    // which busy() alone cannot (both reads see the pre-write value).
    profileButtons()[0].click();
    profileButtons()[0].click();
    flush();
    expect(api.login).toHaveBeenCalledTimes(1);
  });

  it("Login route: create fields are cleared on every exit (T14, L4)", async () => {
    api.listProfiles.mockResolvedValue([{ name: "Ann", hasPin: false }]);
    mount();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    qb("button.login-new")!.click();
    flush();

    const name = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    typeInto(name, "Draft");
    typeInto(qi('input[aria-label="PIN (optional)"]')!, "4321");

    qb("button.login-back")!.click();
    flush();
    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));

    qb("button.login-new")!.click();
    flush();
    const again = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    expect(again.value).toBe("");
    expect(qi('input[aria-label="PIN (optional)"]')!.value).toBe("");
  });

  it("Login route: a name conflict resyncs the picker (M6)", async () => {
    api.listProfiles
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ name: "Reader", hasPin: false }]);
    api.createProfile.mockRejectedValue(
      new ApiError("profile already exists", 409, "name_taken"),
    );
    mount();

    const name = await waitForEl<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    typeInto(name, "Reader");
    submitForm();

    await vi.waitFor(() => expect(profileButtons()).toHaveLength(1));
    expect(api.listProfiles).toHaveBeenCalledTimes(2);
    expect(q("p.login-error")!.textContent).toContain("profile already exists");
  });

  it("Login route: an empty PIN states the consequence (M4, M5)", async () => {
    api.listProfiles.mockResolvedValue([]);
    mount();

    const pin = await waitForEl<HTMLInputElement>(
      'input[aria-label="PIN (optional)"]',
    );
    expect(container.textContent).toContain("opens for anyone");
    expect(pin.getAttribute("autocomplete")).toBe("new-password");
    expect(
      qi('input[aria-label="Profile name"]')!.getAttribute("autocomplete"),
    ).toBe("username");

    typeInto(pin, "1234");
    // isConnected tells a stale node (Solid re-created the subtree) apart from
    // a write that simply has not been committed yet.
    expect(pin.isConnected).toBe(true);
    expect(pin.value).toBe("1234");
    await vi.waitFor(() => {
      expect(container.textContent).not.toContain("opens for anyone");
    });

    // Clearing the field brings the line back exactly once. An earlier revision
    // kept the first copy in the DOM and appended a second one here.
    typeInto(pin, "");
    await vi.waitFor(() => {
      expect(container.textContent).toContain("opens for anyone");
    });
    expect(container.textContent!.match(/opens for anyone/g)).toHaveLength(1);
  });

  it("Login route: disposal aborts the boot fetch (M2, M9)", async () => {
    let bootSignal: AbortSignal | undefined;
    api.listProfiles.mockImplementation((signal?: AbortSignal) => {
      bootSignal = signal;
      return new Promise(() => {});
    });
    mount();

    await vi.waitFor(() => expect(bootSignal).toBeDefined());
    expect(bootSignal!.aborted).toBe(false);
    dispose?.();
    dispose = undefined;
    expect(bootSignal!.aborted).toBe(true);
  });
});
