// Bridges low-level API 401s to the session store without a circular import:
// api/client.ts must not import lib/session.svelte.ts (which imports the
// client). The client calls reportUnauthenticated() when an authenticated
// request comes back 401 "unauthenticated" — e.g. the server restarted and a
// non-remembered session wasn't restored, or the session expired — and the
// session store subscribes to drop the app back to the login screen.
type Listener = (epoch: number) => void;

const listeners = new Set<Listener>();
let sessionEpoch = 0;

/** Identifies the local authentication generation a request belongs to. */
export function currentSessionEpoch(): number {
  return sessionEpoch;
}

/** Advances whenever the client accepts or clears an authenticated profile. */
export function advanceSessionEpoch(): number {
  sessionEpoch += 1;
  return sessionEpoch;
}

export function reportUnauthenticated(epoch: number): void {
  for (const listener of listeners) listener(epoch);
}

export function subscribeUnauthenticated(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
