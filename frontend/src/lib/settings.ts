import {
  createMemo,
  createSignal,
  createStore,
  runWithOwner,
  snapshot,
} from "solid-js";
import {
  ApiError,
  getSettings,
  saveSettings,
  type UserSettings,
} from "~/api/client";
import { getFontById, getFontFamily } from "~/lib/fonts";
import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry";
import { toast } from "~/lib/toast";
import { customThemes } from "~/lib/customThemes";
import { readerThemeVars, type ThemeDef } from "~/lib/themes";

// Shape the reader iframe expects (see iframe/frame.ts apply-settings handler).
export interface IframeSettings {
  mode: "scroll" | "paged" | "paged-two";
  fontSize: number;
  fontFamily: string;
  preserveBookStyles: boolean;
  preserveBookFonts: boolean;
  lineHeight: number | null;
  paragraphSpacing: number | null;
  textIndent: number | null;
  letterSpacing: number | null;
  contentWidth: number | null;
  margins: { top: number | null; bottom: number | null; side: number | null };
  justify: boolean;
  hyphenation: boolean;
  theme: string;
  // Resolved reader palette (CSS custom-property declarations) for a custom
  // theme that has no static frame.css class; null for built-ins. See frame.ts.
  themeVars: string | null;
  chapterTitleAlign: "left" | "center" | "right" | null;
  chapterTitleSize: number | null;
  chapterTitleSpacing: number | null;
  chapterTitleFontFamily: string | null;
  headingLetterSpacing: number | null;
  headerSizesEnabled: boolean;
  h1Size: number | null;
  h2Size: number | null;
  h3Size: number | null;
  h4Size: number | null;
  h5Size: number | null;
  h6Size: number | null;
  headerWeight: number | null;
  textWeight: number | null;
}

export const DEFAULT_USER_SETTINGS: UserSettings = {
  fontSize: 30,
  fontFamily: "literata",
  lineHeight: null,
  paragraphSpacing: null,
  textIndent: 0,
  letterSpacing: null,
  contentWidth: null,
  displayMode: "scroll",
  marginTop: 48,
  marginBottom: 48,
  marginSide: 48,
  preserveStyles: true,
  preserveFonts: false,
  justify: true,
  hyphenation: true,
  theme: "catppuccin",
  chapterTitleAlign: "center",
  chapterTitleSize: 48,
  chapterTitleSpacing: 1,
  chapterTitleFontFamily: null,
  headingLetterSpacing: null,
  headerSizesEnabled: false,
  h1Size: null,
  h2Size: null,
  h3Size: null,
  h4Size: null,
  h5Size: null,
  h6Size: null,
  headerWeight: null,
  textWeight: null,
  fontRoles: {},
};

/**
 * A defaults object with its own nested containers.
 *
 * Spreading DEFAULT_USER_SETTINGS copies `fontRoles` by reference, so anything
 * that later mutates it in place would corrupt the module-level default for the
 * rest of the session. Stores mutate through drafts, so every path that seeds
 * state from the defaults goes through this.
 */
function freshDefaults(): UserSettings {
  return { ...DEFAULT_USER_SETTINGS, fontRoles: {} };
}

/** Resolves a font family id to a CSS font-family value, handling user fonts. */
function resolveFontFamily(id: string): string {
  if (isUserFamilyId(id)) {
    return fontRegistry.cssValue(id) ?? getFontFamily(id);
  }
  return getFontFamily(id);
}

export function toIframeSettings(
  s: UserSettings,
  customThemesList: ThemeDef[] = [],
): IframeSettings {
  return {
    mode: s.displayMode,
    fontSize: s.fontSize,
    fontFamily: resolveFontFamily(s.fontFamily),
    preserveBookStyles: s.preserveStyles,
    preserveBookFonts: s.preserveFonts,
    lineHeight: s.lineHeight,
    paragraphSpacing: s.paragraphSpacing,
    textIndent: s.textIndent,
    letterSpacing: s.letterSpacing,
    contentWidth: s.contentWidth,
    margins: { top: s.marginTop, bottom: s.marginBottom, side: s.marginSide },
    justify: s.justify,
    hyphenation: s.hyphenation,
    theme: s.theme,
    themeVars: readerThemeVars(s.theme, customThemesList),
    chapterTitleAlign: s.chapterTitleAlign,
    chapterTitleSize: s.chapterTitleSize,
    chapterTitleSpacing: s.chapterTitleSpacing,
    chapterTitleFontFamily: s.chapterTitleFontFamily
      ? resolveFontFamily(s.chapterTitleFontFamily)
      : null,
    headingLetterSpacing: s.headingLetterSpacing,
    headerSizesEnabled: s.headerSizesEnabled,
    h1Size: s.h1Size,
    h2Size: s.h2Size,
    h3Size: s.h3Size,
    h4Size: s.h4Size,
    h5Size: s.h5Size,
    h6Size: s.h6Size,
    headerWeight: s.headerWeight,
    textWeight: s.textWeight,
  };
}

class Settings {
  // A store, not a signal: the Svelte revision deliberately mutated fields in
  // place on the deep-reactive proxy rather than replacing the object, so that a
  // font-size drag only wakes the consumers reading fontSize instead of every
  // settings consumer (isPaged, buildAllFontFaces, the iframe mapping). Store
  // drafts give exactly that property-level invalidation.
  readonly #store = createStore<UserSettings>(freshDefaults());
  readonly #loadedSignal = createSignal(false);

  /** Memoised mapping to the reader-iframe settings shape. */
  readonly #iframe: () => IframeSettings;

  /**
   * Non-reactive mirror of `loaded`, used by load()'s guard.
   *
   * This split is inherited from the Svelte revision, and Solid needs it for a
   * second reason: writes are batched, so a signal read immediately after a
   * write still returns the pre-write value. Control flow reads this field; the
   * signal exists only so gated consumers re-render.
   */
  #loaded = false;
  #loadGeneration = 0;
  #loadController: AbortController | undefined;
  #loadPromise: Promise<void> | undefined;
  #revision = 0;
  #saveTimer: ReturnType<typeof setTimeout> | undefined;
  #saveController: AbortController | undefined;
  // Last server-accepted settings. A failed PUT sends the whole object, so a
  // single invalid field would otherwise wedge every later save; on a 4xx we
  // roll back to this snapshot.
  #lastSaved: UserSettings = freshDefaults();

  constructor() {
    // Detached on purpose. In Solid 2.0 a root is owned by its parent by
    // default, and a memo created with no owner warns (NO_OWNER_EFFECT), so
    // "global lifetime" has to be an explicit opt-in. This singleton lives for
    // the life of the document and is never disposed.
    this.#iframe = runWithOwner(null, () =>
      createMemo(() => toIframeSettings(this.value, customThemes.list)),
    );
  }

  /** Current settings. Property reads track individually. */
  get value(): UserSettings {
    return this.#store[0];
  }

  get iframe(): IframeSettings {
    return this.#iframe();
  }

  /** True once server settings have been merged in (reactive). Consumers that
   *  must not act on the compile-time defaults - e.g. the reader's applyTheme
   *  effect, which would flash and cache the default theme - gate on this. */
  get loaded(): boolean {
    return this.#loadedSignal[0]();
  }

  #setLoaded(value: boolean): void {
    this.#loaded = value;
    this.#loadedSignal[1](value);
  }

  /** Loads server settings once; keeps defaults on failure (non-fatal). */
  load(): Promise<void> {
    if (this.#loaded) return Promise.resolve();
    if (this.#loadPromise) return this.#loadPromise;

    const generation = this.#loadGeneration;
    const controller = new AbortController();
    this.#loadController = controller;
    const promise = this.#load(generation, controller);
    this.#loadPromise = promise;
    return promise;
  }

  async #load(generation: number, controller: AbortController): Promise<void> {
    try {
      const loaded = await getSettings(controller.signal);
      // reset() advances the generation before a different profile can load.
      // Never publish a response that belongs to the previous profile.
      if (controller.signal.aborted || generation !== this.#loadGeneration) {
        return;
      }
      // Merge over defaults so a sparse/new-profile response (missing or empty
      // fields) still yields a complete, valid settings object. Without this a
      // new profile can come back without a fontFamily, leaving the font
      // <select> with no matching option (blank instead of the default font).
      //
      // The merged object is assembled and validated as a plain local before it
      // reaches the store. Validating after the write would read the pre-write
      // value under Solid's batching and check the *previous* profile's font id,
      // silently defeating the coercion below.
      const next: UserSettings = { ...freshDefaults(), ...loaded };
      // Coerce an unusable built-in font id (empty or unknown) to the default.
      // User fonts ("user:" prefix) can't be validated until the registry has
      // loaded, so SettingsPanel reconciles those reactively once families load.
      if (!isUserFamilyId(next.fontFamily) && !getFontById(next.fontFamily)) {
        next.fontFamily = DEFAULT_USER_SETTINGS.fontFamily;
      }
      this.#store[1]((s) => {
        Object.assign(s, next);
      });
      this.#lastSaved = { ...next };
      this.#revision += 1;
      this.#loaded = true;
    } catch {
      // Keep defaults if settings cannot be loaded.
    } finally {
      if (this.#loadController === controller) {
        // Settled for this profile (success, or failure keeping defaults):
        // `value` is now the best truth this session will get, so gated
        // consumers may act on it. Not set on abort/supersede (logout race).
        this.#setLoaded(true);
        this.#loadController = undefined;
        this.#loadPromise = undefined;
      }
    }
  }

  update(partial: Partial<UserSettings>): void {
    // Per-field draft mutation, not a wholesale replace: a replace invalidates
    // *every* settings consumer on every change, so a slider drag would rebuild
    // @font-face CSS and re-derive unrelated values each frame.
    this.#store[1]((s) => {
      Object.assign(s, partial);
    });
    this.#revision += 1;
    this.#scheduleSave();
  }

  /** Call on logout so the next login gets fresh settings from the server. */
  reset(): void {
    this.#setLoaded(false);
    this.#loadGeneration += 1;
    this.#loadController?.abort();
    this.#loadController = undefined;
    this.#loadPromise = undefined;
    this.#revision += 1;
    if (this.#saveTimer) {
      clearTimeout(this.#saveTimer);
      this.#saveTimer = undefined;
    }
    this.#saveController?.abort();
    this.#saveController = undefined;
    const defaults = freshDefaults();
    this.#store[1]((s) => {
      Object.assign(s, defaults);
    });
    this.#lastSaved = freshDefaults();
  }

  /**
   * Restore every setting to its default and persist. Unlike reset() (which is
   * for logout and drops the loaded flag), this keeps the session live and
   * schedules a save so the server row is overwritten with the defaults. A
   * fresh fontRoles object avoids sharing the module-level default map.
   */
  resetToDefaults(): void {
    this.update(freshDefaults());
  }

  #scheduleSave(): void {
    if (this.#saveTimer) clearTimeout(this.#saveTimer);
    this.#saveTimer = setTimeout(() => {
      this.#saveTimer = undefined;
      // Supersede any in-flight save so a slow older request can't land after
      // a newer one and clobber the latest settings.
      this.#saveController?.abort();
      const controller = new AbortController();
      this.#saveController = controller;
      // snapshot() is the 2.0 replacement for unwrap(): a plain, non-tracking
      // value. Spread so the payload can't alias the live store.
      const payload: UserSettings = { ...snapshot(this.value) };
      const revision = this.#revision;
      saveSettings(payload, controller.signal)
        .then(() => {
          // A reset() (logout) may have aborted this request between the
          // server accepting it and this callback running; #lastSaved was
          // already re-pointed at the defaults for the next profile and must
          // not be clobbered with the previous profile's payload.
          if (this.#saveController !== controller) return;
          // Remember the last payload the server accepted.
          this.#lastSaved = payload;
        })
        .catch((error: unknown) => {
          if (error instanceof DOMException && error.name === "AbortError")
            return;
          // Only the latest save drives UI feedback; superseded saves abort.
          if (this.#saveController !== controller) return;
          // A newer local edit is already queued behind this request. Its value
          // must survive regardless of how this older snapshot was rejected;
          // the queued save will surface any error that still applies.
          if (this.#revision !== revision) return;
          // A 4xx means this payload is invalid and resending it on the next
          // edit would fail again (the full object is PUT every time). Roll back
          // to the last accepted settings so one bad field can't block every
          // future save.
          if (
            error instanceof ApiError &&
            error.status != null &&
            error.status >= 400 &&
            error.status < 500
          ) {
            const lastSaved = this.#lastSaved;
            this.#store[1]((s) => {
              Object.assign(s, lastSaved);
            });
          }
          toast.show("Couldn't save settings");
        })
        .finally(() => {
          if (this.#saveController === controller)
            this.#saveController = undefined;
        });
    }, 500);
  }
}

export const settings = new Settings();
