import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, tick, unmount } from "svelte";
import Login from "~/routes/Login.svelte";

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

vi.mock("~/lib/session.svelte", () => ({
  session: { login: api.login },
}));

describe("Login profile creation", () => {
  let target: HTMLDivElement;
  let component: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    api.listProfiles.mockReset().mockResolvedValue([]);
    api.createProfile.mockReset().mockResolvedValue({ name: "Reader" });
    api.login
      .mockReset()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined);
    target = document.createElement("div");
    document.body.append(target);
    component = mount(Login, { target });
  });

  afterEach(async () => {
    if (component) await unmount(component);
    target.remove();
  });

  it("retries a created profile without submitting creation again", async () => {
    await vi.waitFor(() => {
      expect(
        target.querySelector<HTMLInputElement>(
          'input[aria-label="Profile name"]',
        ),
      ).not.toBeNull();
    });

    const name = target.querySelector<HTMLInputElement>(
      'input[aria-label="Profile name"]',
    );
    expect(name).not.toBeNull();
    name!.value = "Reader";
    name!.dispatchEvent(new Event("input", { bubbles: true }));
    await tick();

    target
      .querySelector("form")!
      .dispatchEvent(
        new SubmitEvent("submit", { bubbles: true, cancelable: true }),
      );

    await vi.waitFor(() => {
      expect(target.textContent).toContain(
        "Profile created, but sign-in failed",
      );
    });
    expect(api.createProfile).toHaveBeenCalledOnce();
    expect(api.login).toHaveBeenNthCalledWith(1, "Reader", "", false);

    const profile = Array.from(
      target.querySelectorAll<HTMLButtonElement>("button.profile"),
    ).find((button) => button.textContent?.includes("Reader"));
    expect(profile).toBeDefined();
    profile!.click();

    await vi.waitFor(() => expect(api.login).toHaveBeenCalledTimes(2));
    expect(api.login).toHaveBeenNthCalledWith(2, "Reader", "", false);
    expect(api.createProfile).toHaveBeenCalledOnce();
  });
});
