// Ported from the Svelte Login.test.ts. Renders the component with
// @solidjs/web's render directly -- NOT @solidjs/testing-library, whose dist
// imports the removed "solid-js/web" specifier and dies at suite collection
// under Solid 2.0. Events are dispatched by hand (as the Svelte version did);
// `flush` forces Solid 2.0's batched writes so assertions see committed
// state. This file never calls vi.resetModules(), so a statically imported
// flush drives the same scheduler instance as the component under test.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import Login from "~/routes/Login";

const api = vi.hoisted(() => ({
  listProfiles: vi.fn(),
  createProfile: vi.fn(),
  login: vi.fn(),
}));

vi.mock("~/api/client", () => {
  class ApiError extends Error {
    readonly status?: number;
    readonly code?: string;

    constructor(message: string, status?: number, code?: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.code = code;
    }
  }

  return {
    ApiError,
    listProfiles: api.listProfiles,
    createProfile: api.createProfile,
  };
});

vi.mock("~/lib/session", () => ({
  session: { login: api.login },
}));

describe("Login profile creation", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;

  beforeEach(() => {
    // A fake clock leaked by another suite in this worker (fake timers also
    // fake queueMicrotask) would starve Solid's batch flush and freeze the
    // component mid-render. Never trust ambient timer state.
    vi.useRealTimers();
    api.listProfiles.mockReset().mockResolvedValue([]);
    api.createProfile.mockReset().mockResolvedValue({ name: "Reader" });
    api.login
      .mockReset()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined);
    container = document.createElement("div");
    document.body.append(container);
    dispose = render(Login, container);
  });

  afterEach(() => {
    dispose?.();
    container.remove();
  });

  it("retries a created profile without submitting creation again", async () => {
    await vi.waitFor(() => {
      expect(
        container.querySelector<HTMLInputElement>(
          'input[aria-label="Profile name"]',
        ),
      ).not.toBeNull();
    });

    const name = container.querySelector<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    )!;
    name.value = "Reader";
    name.dispatchEvent(new Event("input", { bubbles: true }));
    flush();

    container
      .querySelector("form")!
      .dispatchEvent(
        new SubmitEvent("submit", { bubbles: true, cancelable: true }),
      );

    await vi.waitFor(() => {
      expect(container.textContent).toContain(
        "Profile created, but sign-in failed",
      );
    });
    expect(api.createProfile).toHaveBeenCalledOnce();
    expect(api.login).toHaveBeenNthCalledWith(1, "Reader", "", false);

    const profile = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button.login-profile"),
    ).find((button) => button.textContent?.includes("Reader"));
    expect(profile).toBeDefined();
    profile!.click();
    flush();

    await vi.waitFor(() => expect(api.login).toHaveBeenCalledTimes(2));
    expect(api.login).toHaveBeenNthCalledWith(2, "Reader", "", false);
    expect(api.createProfile).toHaveBeenCalledOnce();
  });
});
