/**
 * Shared server-reachability signal.
 *
 * The app talks to a local sayumi server, so `navigator.onLine` is the wrong
 * signal for "is the backend reachable" — it only reflects the OS network
 * interface and stays `true` when the local server is quit or crashes. Instead
 * we treat the outcome of real API calls as the source of truth: a rejected
 * fetch flips us to unreachable, while any answered request (or a healthy
 * /health poll) flips us back. The OfflineBanner subscribes to this so it
 * surfaces the moment a request can't reach the server, in any view.
 *
 * Deliberately framework-agnostic (no Svelte runes) so the API client can
 * import it without pulling in component/runtime concerns or risking import
 * cycles.
 */

type Listener = (reachable: boolean) => void;

let reachable = true;
const listeners = new Set<Listener>();

export function isReachable(): boolean {
  return reachable;
}

function set(value: boolean): void {
  if (reachable === value) return;
  reachable = value;
  for (const listener of listeners) listener(value);
}

export function reportReachable(): void {
  set(true);
}

export function reportUnreachable(): void {
  set(false);
}

/** Subscribe to reachability transitions. Returns an unsubscribe function. */
export function subscribeReachability(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
