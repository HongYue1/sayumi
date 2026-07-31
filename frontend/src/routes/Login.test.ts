// Ported from the Svelte Login.test.ts (which mounted Login.svelte). Drives
// the Solid component through @solidjs/testing-library; `flush` forces Solid
// 2.0's batched writes so assertions see committed state -- this file does
// not call vi.resetModules(), so a statically imported flush drives the same
// scheduler instance as the component under test.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render } from "@solidjs/testing-library";
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
  let container: HTMLElement;
  let unmount: (() => void) | undefined;

  beforeEach(() => {
    api.listProfiles.mockReset().mockResolvedValue([]);
    api.createProfile.mockReset().mockResolvedValue({ name: "Reader" });
    api.login
      .mockReset()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined);
    const result = render(Login);
    container = result.container;
    unmount = result.unmount;
  });

  afterEach(() => {
    unmount?.();
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
    fireEvent.input(name, { target: { value: "Reader" } });
    flush();

    fireEvent.submit(container.querySelector("form")!);

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
