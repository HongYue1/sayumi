import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  requestWithRetry,
  ApiError,
  getAuthStatus,
  getFonts,
  rescanFonts,
  userFontUrl,
  checkHealth,
} from "~/api/client";
import {
  advanceSessionEpoch,
  currentSessionEpoch,
  subscribeUnauthenticated,
} from "~/lib/sessionGate";
import {
  isReachable,
  reportReachable,
  reportUnreachable,
} from "~/lib/reachability";

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// timeoutMs: 0 disables the per-attempt AbortSignal.timeout so the only timers
// in play are the retry backoff sleeps, which fake timers drive deterministically.
const noTimeout = (attempts: number) => ({ attempts, timeoutMs: 0 });

describe("requestWithRetry", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("retries an idempotent GET on a 500 and then succeeds", async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ error: "boom" }, 500))
      .mockResolvedValueOnce(jsonResponse({ ok: true }, 200));
    const p = requestWithRetry<{ ok: boolean }>(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    );
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("retries an idempotent PUT on a network error and then succeeds", async () => {
    fetchMock
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(jsonResponse({ ok: true }, 200));
    const p = requestWithRetry<{ ok: boolean }>(
      "PUT",
      "/x",
      { a: 1 },
      noTimeout(3),
    );
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does NOT retry a POST on a 500 (non-idempotent write)", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "boom" }, 500));
    const err = await requestWithRetry(
      "POST",
      "/x",
      { a: 1 },
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does NOT retry a PATCH on a 500 (non-idempotent write)", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "boom" }, 500));
    const err = await requestWithRetry(
      "PATCH",
      "/x",
      { a: 1 },
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does NOT retry a 4xx even for an idempotent GET", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "nope" }, 404));
    const err = await requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("gives up after the configured attempts on a persistent 500", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "down" }, 500));
    const settled = requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(2),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    const err = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("rethrows an AbortError without retrying", async () => {
    fetchMock.mockRejectedValue(new DOMException("aborted", "AbortError"));
    const err = await requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(DOMException);
    expect(err).toMatchObject({ name: "AbortError" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

// These cases drive the real per-attempt timer, which the block above disables
// with timeoutMs: 0.
describe("per-attempt timeout", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  // A fetch that settles only when its signal aborts, so the per-attempt timer
  // is the sole thing that can end the attempt.
  function neverSettles(init?: { signal?: AbortSignal }): Promise<Response> {
    const signal = init?.signal;
    return new Promise<Response>((_resolve, reject) => {
      if (!signal) return;
      signal.addEventListener("abort", () => {
        reject(signal.reason as Error);
      });
    });
  }

  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    // Reachability is shared module state: start from "reachable" so an earlier
    // network-error case cannot pre-satisfy or mask the assertions below.
    reportReachable();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    reportReachable();
  });

  it("does not report the server unreachable when an attempt times out", async () => {
    fetchMock.mockImplementation(
      (_input: unknown, init?: { signal?: AbortSignal }) => neverSettles(init),
    );
    const settled = requestWithRetry("GET", "/x", undefined, {
      attempts: 1,
      timeoutMs: 20_000,
    }).catch((e) => e);
    await vi.runAllTimersAsync();
    const err = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: "network_error" });
    // A slow answer is not evidence of a dead server; flipping this would paint
    // the offline banner over a working reader.
    expect(isReachable()).toBe(true);
  });

  it("keeps its full retry budget when an attempt times out", async () => {
    let calls = 0;
    fetchMock.mockImplementation(
      (_input: unknown, init?: { signal?: AbortSignal }) => {
        calls += 1;
        return calls < 3
          ? neverSettles(init)
          : Promise.resolve(jsonResponse({ ok: true }));
      },
    );
    const p = requestWithRetry<{ ok: boolean }>("GET", "/x", undefined, {
      attempts: 3,
      timeoutMs: 20_000,
    });
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    // This stopped at 2 before the fix: the timeout flipped reachability, and
    // the next attempt reads that snapshot as its go/no-go for retrying.
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("releases the timeout timer as soon as the request settles", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ ok: true }));
    await expect(
      requestWithRetry("GET", "/x", undefined, {
        attempts: 1,
        timeoutMs: 20_000,
      }),
    ).resolves.toEqual({ ok: true });
    // AbortSignal.timeout would leave this armed for the full 20s.
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("session authentication generation", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("reports a 401 against the generation that started the request", async () => {
    let resolveFetch!: (response: Response) => void;
    const response = new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() => response),
    );

    const startedAt = currentSessionEpoch();
    const reported: number[] = [];
    const unsubscribe = subscribeUnauthenticated((epoch) =>
      reported.push(epoch),
    );
    const pending = getAuthStatus();

    advanceSessionEpoch();
    resolveFetch(
      jsonResponse({ error: "not logged in", code: "unauthenticated" }, 401),
    );

    await expect(pending).rejects.toMatchObject({
      status: 401,
      code: "unauthenticated",
    });
    expect(reported).toEqual([startedAt]);
    unsubscribe();
  });
});

describe("user font access token", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("adds the latest authenticated font token to user font URLs", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse({ user: [], userToken: "first-token" }),
      )
      .mockResolvedValueOnce(
        jsonResponse({ user: [], userToken: "second-token" }),
      );

    await expect(getFonts()).resolves.toEqual([]);
    expect(userFontUrl("My Family", "Regular.woff2")).toBe(
      `${window.location.origin}/fonts/user/My%20Family/Regular.woff2?token=first-token`,
    );

    await expect(rescanFonts()).resolves.toEqual([]);
    expect(userFontUrl("My Family", "Regular.woff2")).toBe(
      `${window.location.origin}/fonts/user/My%20Family/Regular.woff2?token=second-token`,
    );
  });
});

describe("checkHealth", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    reportReachable();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    reportReachable();
  });

  it("reports reachable on a healthy answer", async () => {
    reportUnreachable();
    fetchMock.mockResolvedValueOnce(jsonResponse({ status: "ok" }));
    await expect(checkHealth()).resolves.toBe(true);
    expect(isReachable()).toBe(true);
  });

  it("reports unreachable when the server answers not-ok", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ error: "x" }, 503));
    await expect(checkHealth()).resolves.toBe(false);
    expect(isReachable()).toBe(false);
    reportReachable();
  });

  it("treats its own timeout as inconclusive while reachable", async () => {
    // A slow probe is not a dead server — same doctrine as request(). No
    // report fires, and the banner's poll path keeps its previous verdict.
    fetchMock.mockRejectedValueOnce(new DOMException("slow", "TimeoutError"));
    await expect(checkHealth()).resolves.toBe(true);
    expect(isReachable()).toBe(true);
  });

  it("keeps the unreachable verdict when a probe times out offline", async () => {
    reportUnreachable();
    fetchMock.mockRejectedValueOnce(new DOMException("slow", "TimeoutError"));
    await expect(checkHealth()).resolves.toBe(false);
    expect(isReachable()).toBe(false);
    reportReachable();
  });

  it("still reports unreachable on a genuine connection failure", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("fetch failed"));
    await expect(checkHealth()).resolves.toBe(false);
    expect(isReachable()).toBe(false);
    reportReachable();
  });
});
