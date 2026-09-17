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

  /** True while a status probe runs; concurrent init() calls coalesce. */
  #initInFlight = false;

  /** Set by a trigger that landed mid-probe; queues one follow-up probe. */
  #reprobeRequested = false;

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
   * reachability recovery when boot could not reach the server. Safe to call
   * repeatedly: a call made while a probe runs coalesces onto it.
   *
   * A coalesced trigger is queued rather than dropped, because it may have
   * seen a recovery the running probe was issued too early to see. It buys
   * exactly one follow-up probe, and only when the running one ends
   * indeterminate - a determinate answer is already the news the trigger was
   * asking for, and each further pass needs a fresh trigger, so this cannot
   * spin. The boot card's Try again cannot land mid-probe (the card unmounts
   * as soon as the status turns checking), so the edge armed by
   * #armBootRetry is the trigger that matters: reachability only notifies on
   * a transition, and a probe that fails on anything other than a network
   * error never reports the server unreachable, so a dropped edge left
   * nothing behind to retry on.
   */
  async init(): Promise<void> {
    this.#reprobeRequested = true;
    if (this.#initInFlight) return;
    this.#initInFlight = true;
    try {
      while (this.#reprobeRequested) {
        this.#reprobeRequested = false;
        await this.#probe();
        if (this.#statusPlain !== "unavailable") break;
      }
    } finally {
      this.#initInFlight = false;
    }
  }

  /** One authoritative status probe. Resolves however it ends. */
  async #probe(): Promise<void> {
    if (this.#statusPlain !== "authenticated") this.#setStatus("checking");
    try {
      const status = await getAuthStatus();
      this.#cancelBootRetry();
      if (status.authenticated) {
        // Mint a generation only when the identity actually changes. The
        // epoch binds a 401 to the login that issued the request, so bumping
        // it for a re-probe that confirms the same profile would make
        // #handleSessionLost discard a genuine 401 already in flight against
        // that very session, leaving the app signed in to a session the
        // server has forgotten. The same name here is the same cookie
        // session: only login() and teardown start a new one.
        if (status.profile !== this.#profilePlain) {
          advanceSessionEpoch();
          this.#setProfile(status.profile);
        }
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
    }
  }

  #armBootRetry(): void {
    if (this.#bootRetry !== null) return;
    this.#bootRetry = subscribeReachability((reachable) => {
      // Only while the status is still unknown - a login or explicit logout
      // in the meantime owns the session and cancels this subscription. That
      // leaves "unavailable" (no probe running) and "checking" (one is), and
      // the second is forwarded too: init() folds it into a single follow-up
      // probe, where dropping it lost the recovery entirely.
      if (!reachable) return;
      if (this.#statusPlain !== "unavailable" && !this.#initInFlight) return;
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
