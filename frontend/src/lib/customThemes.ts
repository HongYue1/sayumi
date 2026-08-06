// Store for user-created custom themes. Loaded from the backend for the active
// profile and mirrored into the themes.ts registry (setCustomThemes) so a
// custom id resolves through getTheme everywhere the built-ins do -- the app
// chrome and, via settings.iframe -> apply-settings, the reader frame.

import { createSignal } from "solid-js";
import {
  createCustomTheme,
  deleteCustomTheme,
  getCustomThemes,
  updateCustomTheme,
  type CustomTheme,
  type CustomThemeInput,
} from "~/api/client";
import { autoAccent, setCustomThemes, type ThemeDef } from "~/lib/themes";
import { toast } from "~/lib/toast";

/**
 * Maps a stored custom theme to the shared ThemeDef shape used for rendering.
 * The accent is resolved here (empty -> auto) so every consumer -- the shell's
 * applyTheme (onAccentColor needs a real hex) and the reader's derived vars --
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
  readonly #listSignal = createSignal<ThemeDef[]>([]);
  readonly #loadedSignal = createSignal(false);

  /**
   * Plain mirrors of the reactive state, read by synchronous control flow.
   *
   * This is load-bearing, not defensive. Solid 2.0 batches writes, so a signal
   * read immediately after a write still returns the pre-write value.
   * activate() clears the loaded flag and then calls load() in the same tick;
   * if load()'s early-return guard read the accessor it would still see `true`
   * and skip the fetch, so switching profiles would silently never reload the
   * custom themes. Guards read these fields; the signals only drive rendering.
   */
  #loadedPlain = false;
  #listPlain: ThemeDef[] = [];

  /** Shared in-flight load so concurrent boot callers don't double-fetch. */
  #loadPromise: Promise<void> | null = null;

  /** Profile whose custom themes this instance currently represents. */
  #profile: string | null = null;

  /** Invalidates async work started under a previous profile. */
  #generation = 0;

  /**
   * Counts local writes that have been published. A load compares this value
   * across its await: a GET issued before a create/update/delete cannot know
   * about that write, so its response is stale even though the profile never
   * changed. #generation cannot cover it -- that counter only moves on a
   * profile switch, and the load and the write run under the same generation.
   */
  #mutations = 0;

  /** Custom themes as ThemeDefs, in server (creation) order. */
  get list(): ThemeDef[] {
    return this.#listSignal[0]();
  }

  /** True once a load has succeeded. Reactive so the UI can distinguish "not
   *  loaded yet" from "loaded, none saved"; a failed load leaves it false so a
   *  later attempt retries. */
  get loaded(): boolean {
    return this.#loadedSignal[0]();
  }

  #setLoaded(value: boolean): void {
    this.#loadedPlain = value;
    this.#loadedSignal[1](value);
  }

  /** Replaces the list and keeps the themes.ts registry in sync. */
  #apply(next: ThemeDef[]): void {
    this.#listPlain = next;
    this.#listSignal[1](next);
    setCustomThemes(next);
  }

  /**
   * Publishes a local write. Bumping the counter before the list lands is what
   * lets an in-flight load recognise that its response predates this change.
   */
  #applyWrite(next: ThemeDef[]): void {
    this.#mutations++;
    this.#apply(next);
  }

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
    this.#setLoaded(false);
    this.#apply([]);

    return profile === null ? Promise.resolve() : this.load();
  }

  /** Loads custom themes once; non-fatal on failure (built-ins still work). */
  async load(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    if (this.#loadedPlain) return;
    if (this.#loadPromise) return this.#loadPromise;

    const generation = this.#generation;
    const mutations = this.#mutations;
    const promise = (async () => {
      try {
        const themes = (await getCustomThemes()).map(toThemeDef);
        if (!this.#isCurrent(profile, generation)) return;
        // A create/update/delete landed while this GET was in flight. The
        // request was issued before that write, so the response cannot contain
        // it, and publishing it would erase the local change -- the theme stays
        // on the server but disappears from the UI, and loaded would flip true
        // so no retry surface ever corrects it. Drop the stale list instead and
        // leave loaded false, so the next retry refetches and converges.
        if (this.#mutations !== mutations) return;
        this.#apply(themes);
        this.#setLoaded(true);
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

  /** Creates a theme; returns the stored ThemeDef, or null on failure. */
  async create(
    input: CustomThemeInput,
    signal?: AbortSignal,
  ): Promise<ThemeDef | null> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return null;
    try {
      const def = toThemeDef(await createCustomTheme(input, signal));
      if (!this.#isCurrent(profile, generation)) return null;
      // Built from the plain mirror: reading this.list here would return the
      // pre-write array inside the same batch and drop a concurrent create.
      this.#applyWrite([...this.#listPlain, def]);
      return def;
    } catch (error) {
      if (
        !(error instanceof DOMException && error.name === "AbortError") &&
        this.#isCurrent(profile, generation)
      ) {
        toast.show("Couldn't save theme");
      }
      return null;
    }
  }

  /** Updates a theme; returns the stored ThemeDef, or null on failure. */
  async update(
    id: string,
    input: CustomThemeInput,
    signal?: AbortSignal,
  ): Promise<ThemeDef | null> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return null;
    try {
      const def = toThemeDef(await updateCustomTheme(id, input, signal));
      if (!this.#isCurrent(profile, generation)) return null;
      this.#applyWrite(this.#listPlain.map((t) => (t.id === id ? def : t)));
      return def;
    } catch (error) {
      if (
        !(error instanceof DOMException && error.name === "AbortError") &&
        this.#isCurrent(profile, generation)
      ) {
        toast.show("Couldn't update theme");
      }
      return null;
    }
  }

  /** Deletes a theme; returns true on success. */
  async remove(id: string, signal?: AbortSignal): Promise<boolean> {
    const profile = this.#profile;
    const generation = this.#generation;
    if (profile === null) return false;
    try {
      await deleteCustomTheme(id, signal);
      if (!this.#isCurrent(profile, generation)) return false;
      this.#applyWrite(this.#listPlain.filter((t) => t.id !== id));
      return true;
    } catch (error) {
      if (
        !(error instanceof DOMException && error.name === "AbortError") &&
        this.#isCurrent(profile, generation)
      ) {
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
