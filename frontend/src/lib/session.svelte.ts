import {
  getAuthStatus,
  listProfiles,
  cloneProfile,
  deleteProfile,
  login as apiLogin,
  logout as apiLogout,
} from "~/api/client";
import { settings } from "~/lib/settings.svelte";

// Holds the currently authenticated profile. Replaces the legacy lib/profile.ts
// module-level state with a Svelte 5 rune. The real session lives server-side in
// the `sayumi_session` cookie; this is the client-side mirror.
class Session {
  /** Active profile name, or null when signed out. */
  profile = $state<string | null>(null);
  /** True once the initial server status check has completed. */
  ready = $state(false);

  get authenticated(): boolean {
    return this.profile !== null;
  }

  /** Checks the existing cookie session on app start. */
  async init(): Promise<void> {
    try {
      const status = await getAuthStatus();
      this.profile = status.authenticated ? status.profile : null;
    } catch {
      this.profile = null;
    } finally {
      this.ready = true;
    }
  }

  async login(name: string, pin: string, remember: boolean): Promise<void> {
    const res = await apiLogin(name, pin, remember);
    this.profile = res.profile;
  }

  async logout(): Promise<void> {
    try {
      await apiLogout();
    } finally {
      this.profile = null;
      // Drop the previous profile's settings so the next login refetches its
      // own from the server instead of inheriting this session's values.
      settings.reset();
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
    await deleteProfile(pin);
    this.profile = null;
    settings.reset();
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
