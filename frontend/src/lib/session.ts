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
import { settings } from "~/lib/settings";
import {
  advanceSessionEpoch,
  currentSessionEpoch,
  subscribeUnauthenticated,
} from "~/lib/sessionGate";

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
   * returns the pre-write value. clearLocalSession() guards on "already signed
   * out" and then writes; if that guard read the accessor, two 401s arriving in
   * the same tick would both pass it and advance the session epoch and reset
   * settings twice. The signal exists only so the UI re-renders.
   */
  #profilePlain: string | null = null;

  constructor() {
    // When the API layer detects the server-side session is gone (e.g. a
    // restart dropped a non-remembered session, or it expired), fall back to
    // the login screen. No-op when already signed out.
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

  /** Checks the existing cookie session on app start. */
  async init(): Promise<void> {
    try {
      const status = await getAuthStatus();
      if (status.authenticated) {
        advanceSessionEpoch();
        this.#setProfile(status.profile);
      } else {
        this.#setProfile(null);
      }
    } catch {
      this.#setProfile(null);
    } finally {
      this.#readySignal[1](true);
    }
  }

  async login(name: string, pin: string, remember: boolean): Promise<void> {
    const res = await apiLogin(name, pin, remember);
    advanceSessionEpoch();
    this.#setProfile(res.profile);
  }

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
   */
  async deleteCurrent(pin: string): Promise<void> {
    try {
      await deleteProfile(pin);
    } catch (error) {
      // Wrong-PIN failures leave the server session intact. Other failures can
      // happen after the backend has already revoked every profile session, so
      // reconcile before preserving the local mirror and rethrow the original
      // operation error either way.
      if (!(
        error instanceof ApiError && error.code === "invalid_credentials"
      )) {
        try {
          const status = await getAuthStatus();
          if (!status.authenticated) this.#clearLocalSession();
        } catch {
          // The status probe is best-effort; never mask the deletion failure.
        }
      }
      throw error;
    }
    this.#clearLocalSession();
  }

  /**
   * Whether the current profile is PIN-protected. Used by the delete dialog to
   * decide if a PIN must be collected. /auth/status doesn't carry hasPin, so we
   * read it from the profiles list. Returns false if the profile isn't found.
   */
  async currentHasPin(): Promise<boolean> {
    const name = this.profile;
    if (name === null) return false;
    const profiles = await listProfiles();
    return profiles.find((p) => p.name === name)?.hasPin ?? false;
  }
}

export const session = new Session();
