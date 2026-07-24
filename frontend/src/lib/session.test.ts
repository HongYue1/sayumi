import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  getAuthStatus: vi.fn(),
  listProfiles: vi.fn(),
  cloneProfile: vi.fn(),
  deleteProfile: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
}));
const resetSettings = vi.hoisted(() => vi.fn());

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

  return { ApiError, ...api };
});

vi.mock("~/lib/settings.svelte", () => ({
  settings: { reset: resetSettings },
}));

beforeEach(() => {
  vi.resetModules();
  vi.clearAllMocks();
  api.login.mockResolvedValue({ profile: "Alice" });
  api.logout.mockResolvedValue(undefined);
  api.getAuthStatus.mockResolvedValue({
    authenticated: true,
    profile: "Alice",
  });
});

describe("session authentication generation", () => {
  it("ignores a late 401 from the profile before the latest login", async () => {
    const { session } = await import("~/lib/session.svelte");
    const gate = await import("~/lib/sessionGate");
    const staleEpoch = gate.currentSessionEpoch();

    await session.login("Alice", "", false);
    gate.reportUnauthenticated(staleEpoch);

    expect(session.profile).toBe("Alice");
    expect(resetSettings).not.toHaveBeenCalled();

    gate.reportUnauthenticated(gate.currentSessionEpoch());
    expect(session.profile).toBeNull();
    expect(resetSettings).toHaveBeenCalledOnce();
  });
});

describe("profile deletion reconciliation", () => {
  it("clears local state when deletion fails after server revocation", async () => {
    const { session } = await import("~/lib/session.svelte");
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
    const { session } = await import("~/lib/session.svelte");
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
