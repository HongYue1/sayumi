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
 * Deliberately framework-agnostic (no framework imports) so the API client
 * can import it without pulling in component/runtime concerns or risking
 * import cycles.
 */

type Listener = (reachable: boolean) => void;

let reachable = true;
const listeners = new Set<Listener>();

export function isReachable(): boolean {
  return reachable;
}

function set(value: boolean): void {
  if (reachable === value) return;
  // The write lands before notification, so a listener that re-reads
  // isReachable() sees the transition it is being told about.
  reachable = value;
  // Dispatch over a snapshot with every listener isolated - the same shape
  // sessionGate.reportUnauthenticated uses, for the same reason:
  // reportUnreachable() runs inside the API client's fetch catch, one line
  // before it throws the network_error ApiError the caller is awaiting. An
  // escaping listener error replaced that ApiError, so every `instanceof
  // ApiError` / `code === "network_error"` branch missed a downed server, and
  // it cut off the listeners behind the thrower too - the offline banner, the
  // session boot retry and the font registry all report through here, so the
  // old claim that propagation protected them had it backwards. The copy also
  // keeps a listener that subscribes mid-dispatch out of a transition that
  // predates it, while the membership check keeps one that unsubscribes
  // mid-dispatch silent. Pinned by reachability.test.ts.
  for (const listener of [...listeners]) {
    if (!listeners.has(listener)) continue;
    try {
      listener(value);
    } catch {
      // A listener's failure is its own. No logger here by design - this
      // module is imported by the API client.
    }
  }
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
