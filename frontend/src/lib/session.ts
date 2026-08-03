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
import { settings } from "~/lib/settings";
import {
  advanceSessionEpoch,
  currentSessionEpoch,
  subscribeUnauthenticated,
} from "~/lib/sessionGate";

/**
 * True when a failed request proves nothing about the session: the server was
 * unreachable, or it answered with an error that is not about authentication.
 * /auth/status answers 200 whether or not a session exists, so anything else
 * leaves the boot state indeterminate and must not be shown as "signed out".
 */
function isTransportFailure(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  return error.code === "network_error" || (error.status ?? 0) >= 500;
}

// Holds the currently authenticated profile. Replaces the legacy lib/profile.ts
// module-level state. The real session lives server-side in the `sayumi_session`
// cookie; this is the client-side mirror.
class Session {
  readonly #profileSignal = createSignal<string | null>(null);
  readonly #readySignal = createSignal(false);

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

  /** Guards re-entrant init(): boot plus any later reachability re-probe. */
  #initInFlight = false;

  /** Unsubscribes the armed reachability re-probe, or null when none is. */
  #bootRetry: (() => void) | null = null;

  constructor() {
    // When the API layer detects the server-side session is gone (e.g. a
    // restart dropped a non-remembered session, or it expired), fall back to
    // the login screen. No-op when already signed out.
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

  /** True once the initial server status check has completed. */
  get ready(): boolean {
    return this.#readySignal[0]();
  }

  get authenticated(): boolean {
    return this.profile !== null;
  }

  #setProfile(value: string | null): void {
    this.#profilePlain = value;
    this.#profileSignal[1](value);
  }

  /** Clears the current profile and invalidates requests from its generation. */
  #clearLocalSession(): void {
    if (this.#profilePlain === null) return;
    advanceSessionEpoch();
    this.#setProfile(null);
    settings.reset();
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
    try {
      const status = await getAuthStatus();
      this.#cancelBootRetry();
      if (status.authenticated) {
        advanceSessionEpoch();
        this.#setProfile(status.profile);
      } else {
        // Route through the shared teardown rather than blanking the name: a
        // re-probe that follows a real session must also advance the epoch and
        // drop that profile's settings. No-ops on first boot.
        this.#clearLocalSession();
      }
    } catch (error) {
      if (isTransportFailure(error)) {
        // Indeterminate, not signed out. Leave local state alone and re-probe
        // when the server answers again (OfflineBanner polls /health from
        // outside the boot gate), so a server that starts late signs the user
        // in instead of stranding them on a login screen with no retry.
        this.#armBootRetry();
      } else {
        this.#clearLocalSession();
      }
    } finally {
      this.#initInFlight = false;
      this.#readySignal[1](true);
    }
  }

  #armBootRetry(): void {
    if (this.#bootRetry !== null) return;
    this.#bootRetry = subscribeReachability((reachable) => {
      // Only while still signed out - a login in the meantime owns the session.
      if (!reachable || this.#profilePlain !== null) return;
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
    advanceSessionEpoch();
    this.#setProfile(res.profile);
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
      // Drop the previous profile's settings so the next login refetches its
      // own from the server instead of inheriting this session's values.
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
      throw new ApiError("Not signed in.", undefined, "unauthenticated");
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
