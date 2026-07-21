// Store for user-created custom themes. Loaded from the backend once per
// session and mirrored into the themes.ts registry (setCustomThemes) so a
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

class CustomThemes {
  /** Custom themes as ThemeDefs, in server (creation) order. */
  list = $state<ThemeDef[]>([]);

  /** True once a load has succeeded. Reactive so the UI can distinguish "not
   *  loaded yet" from "loaded, none saved"; a failed load leaves it false so a
   *  later attempt retries. */
  loaded = $state(false);

  /** Shared in-flight load so concurrent boot callers don't double-fetch. */
  #loadPromise: Promise<void> | null = null;

  /** Loads custom themes once; non-fatal on failure (built-ins still work). */
  async load(): Promise<void> {
    if (this.loaded) return;
    if (this.#loadPromise) return this.#loadPromise;
    this.#loadPromise = (async () => {
      try {
        this.#apply((await getCustomThemes()).map(toThemeDef));
        this.loaded = true;
      } catch {
        // Built-ins still work; loaded stays false so a later call retries.
      } finally {
        this.#loadPromise = null;
      }
    })();
    return this.#loadPromise;
  }

  /** Replaces the list and keeps the themes.ts registry in sync. */
  #apply(next: ThemeDef[]): void {
    this.list = next;
    setCustomThemes(next);
  }

  /** Creates a theme; returns the stored ThemeDef, or null on failure. */
  async create(input: CustomThemeInput): Promise<ThemeDef | null> {
    try {
      const def = toThemeDef(await createCustomTheme(input));
      this.#apply([...this.list, def]);
      return def;
    } catch {
      toast.show("Couldn't save theme");
      return null;
    }
  }

  /** Updates a theme; returns the stored ThemeDef, or null on failure. */
  async update(id: string, input: CustomThemeInput): Promise<ThemeDef | null> {
    try {
      const def = toThemeDef(await updateCustomTheme(id, input));
      this.#apply(this.list.map((t) => (t.id === id ? def : t)));
      return def;
    } catch {
      toast.show("Couldn't update theme");
      return null;
    }
  }

  /** Deletes a theme; returns true on success. */
  async remove(id: string): Promise<boolean> {
    try {
      await deleteCustomTheme(id);
      this.#apply(this.list.filter((t) => t.id !== id));
      return true;
    } catch {
      toast.show("Couldn't delete theme");
      return false;
    }
  }

  /** Custom theme by id (does not consult built-ins). */
  get(id: string): ThemeDef | undefined {
    return this.list.find((t) => t.id === id);
  }
}

export const customThemes = new CustomThemes();
