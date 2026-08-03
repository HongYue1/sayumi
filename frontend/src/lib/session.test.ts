import { beforeEach, describe, expect, it, vi } from "vitest";
import type * as ApiClient from "~/api/client";

const api = vi.hoisted(() => ({
  getAuthStatus: vi.fn(),
  listProfiles: vi.fn(),
  cloneProfile: vi.fn(),
  deleteProfile: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
}));
const resetSettings = vi.hoisted(() => vi.fn());

// Spread the real module so ApiError stays the exact class the store's
// instanceof checks and the client's own throws use. A hand-rolled twin drifts:
// the real constructor already takes a fourth `cause` argument.
vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});

vi.mock("~/lib/settings", () => ({
  settings: { reset: resetSettings },
}));

beforeEach(() => {
  vi.resetModules();
  // resetAllMocks, not clearAllMocks: clear keeps implementations, so a
  // rejection seeded by one test bleeds into the next and the suite goes
  // order-dependent. Every mock the store can reach is seeded below.
  vi.resetAllMocks();
  vi.useRealTimers();
  api.login.mockResolvedValue({ profile: "Alice" });
  api.logout.mockResolvedValue(undefined);
  api.getAuthStatus.mockResolvedValue({
    authenticated: true,
    profile: "Alice",
  });
  api.listProfiles.mockResolvedValue([{ name: "Alice", hasPin: true }]);
  api.cloneProfile.mockResolvedValue(undefined);
  api.deleteProfile.mockResolvedValue(undefined);
});

describe("session authentication generation", () => {
  it("ignores a late 401 from the profile before the latest login", async () => {
    const { session } = await import("~/lib/session");
    const gate = await import("~/lib/sessionGate");
    // vi.resetModules() forks the module registry, so the solid-js instance
    // driving the freshly imported session store must be imported here too —
    // a statically imported flush would drive the pre-reset scheduler.
    const { flush } = await import("solid-js");
    const staleEpoch = gate.currentSessionEpoch();

    await session.login("Alice", "", false);
    gate.reportUnauthenticated(staleEpoch);

    expect(session.profile).toBe("Alice");
    expect(resetSettings).not.toHaveBeenCalled();

    gate.reportUnauthenticated(gate.currentSessionEpoch());
    // The epoch-matched report clears the profile through a batched signal
    // write; flush before the synchronous read (Solid 2.0 batches writes).
    flush();
    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });
});

describe("profile deletion reconciliation", () => {
  it("clears local state when deletion fails after server revocation", async () => {
    const { session } = await import("~/lib/session");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    api.deleteProfile.mockRejectedValue(new Error("delete failed"));
    api.getAuthStatus.mockResolvedValue({ authenticated: false, profile: "" });

    await expect(session.deleteCurrent("1234")).rejects.toThrow(
      "delete failed",
    );

    expect(api.getAuthStatus).toHaveBeenCalledOnce();
    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });

  it("keeps the session on an invalid-credentials deletion failure", async () => {
    const { ApiError } = await import("~/api/client");
    const { session } = await import("~/lib/session");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    api.deleteProfile.mockRejectedValue(
      new ApiError("incorrect PIN", 401, "invalid_credentials"),
    );

    await expect(session.deleteCurrent("0000")).rejects.toMatchObject({
      code: "invalid_credentials",
    });

    expect(api.getAuthStatus).not.toHaveBeenCalled();
    expect(session.profile).toBe("Alice");
    expect(resetSettings).not.toHaveBeenCalled();
  });
});

describe("boot status probe", () => {
  it("signs in from an existing cookie session", async () => {
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");

    await session.init();
    flush();

    expect(session.ready).toBe(true);
    expect(session.profile).toBe("Alice");
  });

  it("does not report signed out when the server is unreachable", async () => {
    const { ApiError } = await import("~/api/client");
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    api.getAuthStatus.mockRejectedValue(
      new ApiError("Could not reach the server.", undefined, "network_error"),
    );

    await session.init();
    flush();

    // Ready so the UI stops spinning, but nothing was torn down and nothing
    // claimed the user is signed out.
    expect(session.ready).toBe(true);
    expect(session.profile).toBeNull();
    expect(resetSettings).not.toHaveBeenCalled();
  });

  it("re-probes when the server becomes reachable again", async () => {
    const { ApiError } = await import("~/api/client");
    const reachability = await import("~/lib/reachability");
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    api.getAuthStatus.mockRejectedValueOnce(
      new ApiError("Could not reach the server.", undefined, "network_error"),
    );

    await session.init();
    expect(session.profile).toBeNull();

    reachability.reportUnreachable();
    reachability.reportReachable();

    await vi.waitFor(() => {
      flush();
      expect(session.profile).toBe("Alice");
    });
  });

  it("tears down through the shared path when the server says signed out", async () => {
    const { session } = await import("~/lib/session");
    const gate = await import("~/lib/sessionGate");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    const epoch = gate.currentSessionEpoch();
    api.getAuthStatus.mockResolvedValue({ authenticated: false, profile: "" });

    await session.init();
    flush();

    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
    expect(gate.currentSessionEpoch()).not.toBe(epoch);
  });
});

describe("sign-out", () => {
  it("clears local state even when the request fails, and still rejects", async () => {
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    api.logout.mockRejectedValue(new Error("network down"));

    await expect(session.logout()).rejects.toThrow("network down");
    flush();

    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });
});

describe("profile deletion", () => {
  it("clears local state after a successful deletion", async () => {
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();

    await session.deleteCurrent("1234");
    flush();

    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });

  it("leaves a session that was created while the request was in flight", async () => {
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    // The server revokes every session for the profile before it takes the
    // delete lock, so the session can die and be replaced mid-request.
    api.deleteProfile.mockImplementation(async () => {
      await session.logout();
      api.login.mockResolvedValue({ profile: "Bob" });
      await session.login("Bob", "", false);
    });

    await session.deleteCurrent("1234");
    flush();

    expect(session.profile).toBe("Bob");
    expect(resetSettings).toHaveBeenCalledOnce();
  });

  it("skips the reconciliation probe when the gate already signed us out", async () => {
    const { ApiError } = await import("~/api/client");
    const { session } = await import("~/lib/session");
    const gate = await import("~/lib/sessionGate");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();
    api.getAuthStatus.mockClear();
    api.deleteProfile.mockImplementation(() => {
      gate.reportUnauthenticated(gate.currentSessionEpoch());
      return Promise.reject(
        new ApiError("not logged in", 401, "unauthenticated"),
      );
    });

    await expect(session.deleteCurrent("1234")).rejects.toMatchObject({
      code: "unauthenticated",
    });
    flush();

    expect(api.getAuthStatus).not.toHaveBeenCalled();
    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });
});

describe("PIN prerequisite", () => {
  it("reports the current profile's PIN protection", async () => {
    const { session } = await import("~/lib/session");
    await session.login("Alice", "", false);

    await expect(session.currentHasPin()).resolves.toBe(true);
  });

  it("fails closed when the profile is gone from the list", async () => {
    const { session } = await import("~/lib/session");
    await session.login("Alice", "", false);
    api.listProfiles.mockResolvedValue([{ name: "Bob", hasPin: false }]);

    await expect(session.currentHasPin()).rejects.toMatchObject({
      code: "not_found",
    });
  });

  it("fails closed when signed out", async () => {
    const { session } = await import("~/lib/session");

    await expect(session.currentHasPin()).rejects.toMatchObject({
      code: "unauthenticated",
    });
    expect(api.listProfiles).not.toHaveBeenCalled();
  });
});

describe("profile cloning", () => {
  it("leaves the current session untouched", async () => {
    const { session } = await import("~/lib/session");
    const { flush } = await import("solid-js");
    await session.login("Alice", "", false);
    resetSettings.mockClear();

    await session.clone("Alice copy", "");
    flush();

    expect(api.cloneProfile).toHaveBeenCalledWith("Alice copy", "");
    expect(session.profile).toBe("Alice");
    expect(resetSettings).not.toHaveBeenCalled();
  });
});
