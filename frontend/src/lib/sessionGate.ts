// Bridges low-level API 401s to the session store without a circular import:
// api/client.ts must not import lib/session.ts (which imports the
// client). The client calls reportUnauthenticated() when an authenticated
// request comes back 401 "unauthenticated" — e.g. the server restarted and a
// non-remembered session wasn't restored, or the session expired — and the
// session store subscribes to drop the app back to the login screen.
//
// The generation is reported here, not enforced: the client stamps a request
// with the epoch that was current when it started, and the session store owns
// the decision of whether that generation still holds the session. Every
// listener therefore sees the same epoch regardless of dispatch order, even
// when an earlier one advances it while tearing the session down.
type Listener = (epoch: number) => void;

/** The one client-side owner of the server's lost-session wire code. */
export const UNAUTHENTICATED_CODE = "unauthenticated";

/** Distinguishes a lost session from credential failures that also use 401. */
export function isSessionAccessError(
  status: number,
  code: string | undefined,
): boolean {
  return status === 401 && code === UNAUTHENTICATED_CODE;
}

const listeners = new Set<Listener>();

// Generation 0 is "before any login": nothing has been authenticated yet, so a
// 401 stamped with it can only come from the boot status probe, and the session
// store's "already signed out" mirror is what makes that report a no-op.
let sessionEpoch = 0;

/** Identifies the local authentication generation a request belongs to. */
export function currentSessionEpoch(): number {
  return sessionEpoch;
}

/** Advances whenever the client accepts or clears an authenticated profile. */
export function advanceSessionEpoch(): void {
  sessionEpoch += 1;
}

/**
 * Reports that the request stamped with `epoch` came back 401.
 *
 * Dispatch is snapshotted and isolated. This runs inside the API client's error
 * path, immediately before it throws the ApiError the caller is awaiting: a
 * throwing listener must not replace that error, and must not cut off the
 * listeners behind it. Iterating a copy also makes unsubscribing mid-dispatch
 * safe.
 */
export function reportUnauthenticated(epoch: number): void {
  for (const listener of [...listeners]) {
    try {
      listener(epoch);
    } catch {
      // A listener's failure is its own: swallowing keeps the caller's ApiError
      // intact and the remaining listeners running. No logger here by design -
      // this module is imported by the API client.
    }
  }
}

export function subscribeUnauthenticated(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
