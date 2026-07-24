import {
  ApiError,
  getAuthStatus,
  listProfiles,
  cloneProfile,
  deleteProfile,
  login as apiLogin,
  logout as apiLogout,
} from "~/api/client";
import { settings } from "~/lib/settings.svelte";
import {
  advanceSessionEpoch,
  currentSessionEpoch,
  subscribeUnauthenticated,
} from "~/lib/sessionGate";

// Holds the currently authenticated profile. Replaces the legacy lib/profile.ts
// module-level state with a Svelte 5 rune. The real session lives server-side in
// the `sayumi_session` cookie; this is the client-side mirror.
class Session {
  /** Active profile name, or null when signed out. */
  profile = $state<string | null>(null);
  /** True once the initial server status check has completed. */
  ready = $state(false);

  constructor() {
    // When the API layer detects the server-side session is gone (e.g. a
    // restart dropped a non-remembered session, or it expired), fall back to
    // the login screen. No-op when already signed out.
    subscribeUnauthenticated((epoch) => this.handleSessionLost(epoch));
  }

  get authenticated(): boolean {
    return this.profile !== null;
  }

  /** Clears the current profile and invalidates requests from its generation. */
  private clearLocalSession(): void {
    if (this.profile === null) return;
    advanceSessionEpoch();
    this.profile = null;
    settings.reset();
  }

  /** Clears local state only when the 401 belongs to the current login. */
  private handleSessionLost(epoch: number): void {
    if (epoch !== currentSessionEpoch()) return;
    this.clearLocalSession();
  }

  /** Checks the existing cookie session on app start. */
  async init(): Promise<void> {
    try {
      const status = await getAuthStatus();
      if (status.authenticated) {
        advanceSessionEpoch();
        this.profile = status.profile;
      } else {
        this.profile = null;
      }
    } catch {
      this.profile = null;
    } finally {
      this.ready = true;
    }
  }

  async login(name: string, pin: string, remember: boolean): Promise<void> {
    const res = await apiLogin(name, pin, remember);
    advanceSessionEpoch();
    this.profile = res.profile;
  }

  async logout(): Promise<void> {
    try {
      await apiLogout();
    } finally {
      // Drop the previous profile's settings so the next login refetches its
      // own from the server instead of inheriting this session's values.
      this.clearLocalSession();
    }
  }

  /**
   * Clones the current profile into `newName`, optionally setting `pin` on the
   * copy. The server only duplicates data — it does NOT switch the session, so
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
          if (!status.authenticated) this.clearLocalSession();
        } catch {
          // The status probe is best-effort; never mask the deletion failure.
        }
      }
      throw error;
    }
    this.clearLocalSession();
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
