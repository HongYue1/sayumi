import { createSignal } from "solid-js";
import {
  ApiError,
  getAuthStatus,
  listProfiles,
  cloneProfile,
  deleteProfile,
  login as apiLogin,
  logout as apiLogout,
} from "~/api/client";
import { subscribeReachability } from "~/lib/reachability";
import {
  advanceSessionEpoch,
  currentSessionEpoch,
  subscribeUnauthenticated,
  UNAUTHENTICATED_CODE,
} from "~/lib/sessionGate";

// Holds the currently authenticated profile. The real session lives server-side
// in the `sayumi_session` cookie; this is the client-side mirror.
export type SessionStatus =
  | "checking"
  | "authenticated"
  | "signed-out"
  | "unavailable";

class Session {
  readonly #profileSignal = createSignal<string | null>(null);
  readonly #statusSignal = createSignal<SessionStatus>("checking");

  /**
   * Plain mirror of `profile`, read by synchronous control flow.
   *
   * Solid batches writes, so a signal read immediately after a write still
   * returns the last committed value until the flush. Repeat 401s in one tick
   * are NOT what this guards: advanceSessionEpoch() mutates a plain number
   * synchronously, so #handleSessionLost() already rejects every later report
   * carrying a spent generation. What the mirror guards is teardown
   * re-entrancy - a gate report crossing logout()/deleteCurrent()'s own
   * teardown, where the clear has run but the signal has not flushed, so an
   * accessor read would still see the old profile and advance the epoch and
   * reset settings a second time. It is also the only guard during boot, while
   * the epoch still sits at its initial generation. The signal exists only so
   * the UI re-renders.
   */
  #profilePlain: string | null = null;

  /** Plain status mirror for same-tick retry and authentication guards. */
  #statusPlain: SessionStatus = "checking";

  /** Guards re-entrant init(): boot plus any later reachability re-probe. */
  #initInFlight = false;

  /** Unsubscribes the armed reachability re-probe, or null when none is. */
  #bootRetry: (() => void) | null = null;

  constructor() {
    // When the API layer detects the server-side session is gone (e.g. a
    // restart dropped a non-remembered session, or it expired), publish a
    // determinate signed-out state. A status-probe failure takes a separate
    // unavailable path and never reaches this callback.
    //
    // The unsubscribe is deliberately discarded: `session` is an app-lifetime
    // singleton created at module evaluation and never torn down, the same
    // contract settings.ts and library.ts document for their global state.
    subscribeUnauthenticated((epoch) => this.#handleSessionLost(epoch));
  }

  /** Active profile name, or null when signed out. */
  get profile(): string | null {
    return this.#profileSignal[0]();
  }

  /** What the latest authoritative status probe established. */
  get status(): SessionStatus {
    return this.#statusSignal[0]();
  }

  get authenticated(): boolean {
    return this.status === "authenticated";
  }

  #setProfile(value: string | null): void {
    this.#profilePlain = value;
    this.#profileSignal[1](value);
  }

  #setStatus(value: SessionStatus): void {
    this.#statusPlain = value;
    this.#statusSignal[1](value);
  }

  /** Clears the current profile and publishes a determinate signed-out state. */
  #clearLocalSession(): void {
    this.#cancelBootRetry();
    if (this.#profilePlain !== null) {
      advanceSessionEpoch();
      this.#setProfile(null);
    }
    this.#setStatus("signed-out");
  }

  /** Clears local state only when the 401 belongs to the current login. */
  #handleSessionLost(epoch: number): void {
    if (epoch !== currentSessionEpoch()) return;
    this.#clearLocalSession();
  }

  /**
   * Checks the existing cookie session on app start, and again on a later
   * reachability recovery when boot could not reach the server. Re-entrant
   * calls are dropped, so it is safe to call repeatedly.
   */
  async init(): Promise<void> {
    if (this.#initInFlight) return;
    this.#initInFlight = true;
    if (this.#statusPlain !== "authenticated") this.#setStatus("checking");
    try {
      const status = await getAuthStatus();
      this.#cancelBootRetry();
      if (status.authenticated) {
        advanceSessionEpoch();
        this.#setProfile(status.profile);
        this.#setStatus("authenticated");
      } else {
        // Route through the shared teardown rather than blanking the name: a
        // re-probe that follows a real session must also advance the epoch and
        // drop that profile's settings. No-ops on first boot.
        this.#clearLocalSession();
      }
    } catch {
      // /auth/status answers 200 for both authenticated and signed-out users.
      // Every error is therefore indeterminate, including an unexpected 4xx:
      // never turn a server/protocol failure into a claim that the user signed
      // out. Keep any known profile and retry automatically on recovery.
      this.#setStatus("unavailable");
      this.#armBootRetry();
    } finally {
      this.#initInFlight = false;
    }
  }

  #armBootRetry(): void {
    if (this.#bootRetry !== null) return;
    this.#bootRetry = subscribeReachability((reachable) => {
      // Only while the status is still unknown - a login or explicit logout
      // in the meantime owns the session and cancels this subscription.
      if (!reachable || this.#statusPlain !== "unavailable") return;
      void this.init();
    });
  }

  #cancelBootRetry(): void {
    const unsubscribe = this.#bootRetry;
    if (unsubscribe === null) return;
    this.#bootRetry = null;
    unsubscribe();
  }

  async login(name: string, pin: string, remember: boolean): Promise<void> {
    const res = await apiLogin(name, pin, remember);
    this.#cancelBootRetry();
    advanceSessionEpoch();
    this.#setProfile(res.profile);
    this.#setStatus("authenticated");
  }

  /**
   * Signs out. Local teardown runs even when the request fails, so the UI never
   * strands the user on a session it has already forgotten - but the rejection
   * is rethrown, because a transport failure means the server-side session may
   * still exist. Teardown has already completed by the time a caller sees it.
   */
  async logout(): Promise<void> {
    try {
      await apiLogout();
    } finally {
      // App observes the profile clear and deactivates every profile-owned
      // store through its own lifecycle boundary.
      this.#clearLocalSession();
    }
  }

  /**
   * Clones the current profile into `newName`, optionally setting `pin` on the
   * copy. The server only duplicates data - it does NOT switch the session, so
   * the user stays signed in as the current profile and local state is left
   * untouched.
   */
  async clone(newName: string, pin: string): Promise<void> {
    await cloneProfile(newName, pin);
  }

  /**
   * Deletes the current profile after the server verifies `pin` against it. On
   * success the server clears the session cookie, so mirror logout's local
   * teardown. Teardown only runs on success: a failed verify (e.g. wrong PIN)
   * throws and leaves the session intact for the caller to surface.
   *
   * Every teardown here is generation-guarded. The server revokes all sessions
   * for the profile BEFORE it takes the delete lock, and that lock waits up to
   * 30s, so a concurrent request can 401 and tear this session down while the
   * delete is still outstanding. If the user signs in again in that window,
   * this call must not clear the session they came back to.
   */
  async deleteCurrent(pin: string): Promise<void> {
    const epoch = currentSessionEpoch();
    try {
      await deleteProfile(pin);
    } catch (error) {
      // Wrong-PIN failures leave the server session intact. Other failures can
      // happen after the backend has already revoked every profile session, so
      // reconcile before preserving the local mirror and rethrow the original
      // operation error either way. A 401 needs no probe: the gate has already
      // torn the session down, which is exactly what the mirror reads as null.
      if (
        !(error instanceof ApiError && error.code === "invalid_credentials") &&
        this.#profilePlain !== null
      ) {
        try {
          const status = await getAuthStatus();
          if (!status.authenticated && epoch === currentSessionEpoch()) {
            this.#clearLocalSession();
          }
        } catch {
          // The status probe is best-effort; never mask the deletion failure.
        }
      }
      throw error;
    }
    if (epoch !== currentSessionEpoch()) return;
    this.#clearLocalSession();
  }

  /**
   * Whether the current profile is PIN-protected. Used by the delete dialog to
   * decide if a PIN must be collected. /auth/status doesn't carry hasPin, so we
   * read it from the profiles list.
   *
   * Fails closed, per ProfileDialog: a profile missing from the list - deleted
   * from another tab, or a session the server no longer knows - throws instead
   * of answering "no PIN". Answering false there hides the PIN field, enables
   * submit with an empty PIN, and leaves a protected profile undeletable behind
   * a retry that re-runs the same coercion.
   */
  async currentHasPin(): Promise<boolean> {
    // The plain mirror, not the accessor: this decides control flow and can run
    // in the same tick as a teardown, before the signal write has flushed.
    const name = this.#profilePlain;
    if (name === null) {
      throw new ApiError("Not signed in.", undefined, UNAUTHENTICATED_CODE);
    }
    const profiles = await listProfiles();
    const entry = profiles.find((p) => p.name === name);
    if (entry === undefined) {
      throw new ApiError(
        `Profile “${name}” is no longer available.`,
        undefined,
        "not_found",
      );
    }
    return entry.hasPin;
  }
}

export const session = new Session();
