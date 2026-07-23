// Store for user-created custom themes. Loaded from the backend for the active
// profile and mirrored into the themes.ts registry (setCustomThemes) so a
// custom id resolves through getTheme everywhere the built-ins do — the app
// chrome and, via settings.iframe -> apply-settings, the reader frame.

import {
  createCustomTheme,
  deleteCustomTheme,
  getCustomThemes,
  updateCustomTheme,
  type CustomTheme,
  type CustomThemeInput,
} from "~/api/client";
import { autoAccent, setCustomThemes, type ThemeDef } from "~/lib/themes";
import { toast } from "~/lib/toast.svelte";

/**
 * Maps a stored custom theme to the shared ThemeDef shape used for rendering.
 * The accent is resolved here (empty -> auto) so every consumer — the shell's
 * applyTheme (onAccentColor needs a real hex) and the reader's derived vars —
 * always sees a concrete color rather than the "" sentinel that means "auto".
 */
function toThemeDef(ct: CustomTheme): ThemeDef {
  return {
    id: ct.id,
    label: ct.name,
    group: ct.group,
    bg: ct.bg,
    fg: ct.fg,
    accent: ct.accent || autoAccent(ct.bg, ct.fg),
  };
}

export class CustomThemes {
  /** Custom themes as ThemeDefs, in server (creation) order. */
  list = $state<ThemeDef[]>([]);

  /** True once a load has succeeded. Reactive so the UI can distinguish "not
   *  loaded yet" from "loaded, none saved"; a failed load leaves it false so a
   *  later attempt retries. */
  loaded = $state(false);

  /** Shared in-flight load so concurrent boot callers don't double-fetch. */
  #loadPromise: Promise<void> | null = null;

  /** Profile whose custom themes this instance currently represents. */
  #profile: string | null = null;

  /** Invalidates async work started under a previous profile. */
  #generation = 0;

  /**
   * Switches the store to a profile, clearing profile-owned state before any
   * asynchronous load. Re-activating the same profile retries a failed load.
   */
  activate(profile: string | null): Promise<void> {
    if (this.#profile === profile) {
      return profile === null ? Promise.resolve() : this.load();
    }

    this.#profile = profile;
    this.#generation++;
    this.#loadPromise = null;
    this.loaded = false;
    this.#apply([]);

    return profile === null ? Promise.resolve() : this.load();
  }

  /** Loads custom themes once; non-fatal on failure (built-ins still work). */
  async load(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    if (this.loaded) return;
    if (this.#loadPromise) return this.#loadPromise;

    const generation = this.#generation;
    const promise = (async () => {
      try {
        const themes = (await getCustomThemes()).map(toThemeDef);
        if (!this.#isCurrent(profile, generation)) return;
        this.#apply(themes);
        this.loaded = true;
      } catch {
        // Built-ins still work; loaded stays false so a later call retries.
      } finally {
        // A profile change increments the generation before starting its own
        // load, so an older request must not clear the newer in-flight promise.
        if (this.#isCurrent(profile, generation)) this.#loadPromise = null;
      }
    })();
    this.#loadPromise = promise;
    return promise;
  }

  #isCurrent(profile: string, generation: number): boolean {
    return this.#profile === profile && this.#generation === generation;
  }

  /** Replaces the list and keeps the themes.ts registry in sync. */
  #apply(next: ThemeDef[]): void {
    this.list = next;
    setCustomThemes(next);
  }

  /** Creates a theme; returns the stored ThemeDef, or null on failure. */
  async create(input: CustomThemeInput): Promise<ThemeDef | null> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return null;
    try {
      const def = toThemeDef(await createCustomTheme(input));
      if (!this.#isCurrent(profile, generation)) return null;
      this.#apply([...this.list, def]);
      return def;
    } catch {
      if (this.#isCurrent(profile, generation)) {
        toast.show("Couldn't save theme");
      }
      return null;
    }
  }

  /** Updates a theme; returns the stored ThemeDef, or null on failure. */
  async update(id: string, input: CustomThemeInput): Promise<ThemeDef | null> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return null;
    try {
      const def = toThemeDef(await updateCustomTheme(id, input));
      if (!this.#isCurrent(profile, generation)) return null;
      this.#apply(this.list.map((t) => (t.id === id ? def : t)));
      return def;
    } catch {
      if (this.#isCurrent(profile, generation)) {
        toast.show("Couldn't update theme");
      }
      return null;
    }
  }

  /** Deletes a theme; returns true on success. */
  async remove(id: string): Promise<boolean> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return false;
    try {
      await deleteCustomTheme(id);
      if (!this.#isCurrent(profile, generation)) return false;
      this.#apply(this.list.filter((t) => t.id !== id));
      return true;
    } catch {
      if (this.#isCurrent(profile, generation)) {
        toast.show("Couldn't delete theme");
      }
      return false;
    }
  }

  /** Custom theme by id (does not consult built-ins). */
  get(id: string): ThemeDef | undefined {
    return this.list.find((t) => t.id === id);
  }
}

export const customThemes = new CustomThemes();
