import {
  getAuthStatus,
  login as apiLogin,
  logout as apiLogout,
} from "~/api/client";

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
    }
  }
}

export const session = new Session();
