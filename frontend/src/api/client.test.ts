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
import { getErrorMessage } from "~/lib/errors";

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
    // Reachability is shared module state and the network-error cases below
    // flip it, so reset it rather than leaking a verdict into the next test.
    reportReachable();
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

  it("keeps its full retry budget when the connection keeps failing", async () => {
    // The first failure reports the server unreachable. Read per attempt,
    // that verdict became attempt 2's go/no-go snapshot and classified its
    // own network error as non-retryable, cutting the budget from 3 to 2.
    reportReachable();
    fetchMock
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(jsonResponse({ ok: true }, 200));
    const p = requestWithRetry<{ ok: boolean }>(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    );
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("does not retry a request that starts while the server is down", async () => {
    // The short-circuit the snapshot exists for: a server already known to be
    // gone must not cost 3 attempts x backoff x timeout on every navigation.
    reportUnreachable();
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const settled = requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    const err = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: "network_error" });
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

  it("retries a timeout that fires while the body streams", async () => {
    let calls = 0;
    fetchMock.mockImplementation(
      (_input: unknown, init?: { signal?: AbortSignal }) => {
        calls += 1;
        if (calls > 1) return Promise.resolve(jsonResponse({ ok: true }));
        const signal = init?.signal;
        // Headers answered, body stalls: res.json() waits on a stream that
        // only ends when the attempt's timeout aborts it. The rejection is
        // the signal's TimeoutError reason - as ApiError(200,
        // invalid_response) it would be classified non-retryable and
        // misreport a stalled-but-answered body as malformed, so request()
        // gives it the retryable network_error shape instead.
        return Promise.resolve(
          new Response(
            new ReadableStream({
              start(controller) {
                signal?.addEventListener("abort", () => {
                  controller.error(signal.reason as Error);
                });
              },
            }),
            { headers: { "Content-Type": "application/json" } },
          ),
        );
      },
    );
    const p = requestWithRetry<{ ok: boolean }>("GET", "/x", undefined, {
      attempts: 2,
      timeoutMs: 20_000,
    });
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("reports a body-phase timeout like a headers-phase one", async () => {
    fetchMock.mockImplementation(
      (_input: unknown, init?: { signal?: AbortSignal }) => {
        const signal = init?.signal;
        return Promise.resolve(
          new Response(
            new ReadableStream({
              start(controller) {
                signal?.addEventListener("abort", () => {
                  controller.error(signal.reason as Error);
                });
              },
            }),
            { headers: { "Content-Type": "application/json" } },
          ),
        );
      },
    );
    const settled = requestWithRetry("GET", "/x", undefined, {
      attempts: 1,
      timeoutMs: 20_000,
    }).catch((e) => e);
    await vi.runAllTimersAsync();
    const err = await settled;
    // Raw, this reached call sites as a TimeoutError DOMException, which
    // getErrorMessage's ApiError narrowing drops -- so the same timeout that
    // reads "Request timed out" from the headers phase showed up as each
    // caller's generic copy once the headers had already arrived.
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      code: "network_error",
      message: "Request timed out",
    });
    expect(getErrorMessage(err, "Couldn't load")).toBe("Request timed out");
  });

  it("bounds a single-shot request that never gets an answer", async () => {
    // getAuthStatus goes straight through request(), which passed no timeout
    // at all: the promise never settled, so a server that stopped answering
    // left the login screen spinning forever with nothing to report.
    fetchMock.mockImplementation(
      (_input: unknown, init?: { signal?: AbortSignal }) => neverSettles(init),
    );
    const settled = getAuthStatus().catch((e) => e);
    await vi.advanceTimersByTimeAsync(20_000);
    const err = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      code: "network_error",
      message: "Request timed out",
    });
    // Same doctrine as the retry path: slow is not gone.
    expect(isReachable()).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
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

  it("does not report a credential 401 as a lost session", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          jsonResponse(
            { error: "incorrect PIN", code: "invalid_credentials" },
            401,
          ),
        ),
      ),
    );
    const reported: number[] = [];
    const unsubscribe = subscribeUnauthenticated((epoch) =>
      reported.push(epoch),
    );

    await expect(getAuthStatus()).rejects.toMatchObject({
      status: 401,
      code: "invalid_credentials",
    });
    expect(reported).toEqual([]);
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

  it("ends a stalled probe on its own bound", async () => {
    // Pins the timer, not only the bound: AbortSignal.timeout's timer is
    // native and cannot be cleared, so it answers to neither fake timers here
    // nor a dispose() in the app, and every poll left one armed for 5s. A
    // fetch that settles only when its signal aborts leaves that bound as the
    // single way out of the probe.
    vi.useFakeTimers();
    try {
      fetchMock.mockImplementation(
        (_input: unknown, init?: { signal?: AbortSignal }) =>
          new Promise<Response>((_resolve, reject) => {
            const signal = init?.signal;
            if (!signal) return;
            signal.addEventListener("abort", () => {
              reject(signal.reason as Error);
            });
          }),
      );

      let done = false;
      const settled = checkHealth().then((reachable) => {
        done = true;
        return reachable;
      });
      await vi.advanceTimersByTimeAsync(4_999);
      expect(done).toBe(false);

      await vi.advanceTimersByTimeAsync(1);
      // Its own timeout stays inconclusive, so the previous verdict holds.
      await expect(settled).resolves.toBe(true);
      expect(isReachable()).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});
