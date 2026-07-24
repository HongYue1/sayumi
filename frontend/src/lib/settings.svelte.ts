import {
  ApiError,
  getSettings,
  saveSettings,
  type UserSettings,
} from "~/api/client";
import { getFontById, getFontFamily } from "~/lib/fonts";
import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry.svelte";
import { toast } from "~/lib/toast.svelte";
import { customThemes } from "~/lib/customThemes.svelte";
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
  value = $state<UserSettings>({ ...DEFAULT_USER_SETTINGS });
  /** Memoised mapping to the reader-iframe settings shape. */
  iframe = $derived.by<IframeSettings>(() =>
    toIframeSettings(this.value, customThemes.list),
  );

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
  #lastSaved: UserSettings = { ...DEFAULT_USER_SETTINGS };

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
      this.value = { ...DEFAULT_USER_SETTINGS, ...loaded };
      // Coerce an unusable built-in font id (empty or unknown) to the default.
      // User fonts ("user:" prefix) can't be validated until the registry has
      // loaded, so SettingsPanel reconciles those reactively once families load.
      const fam = this.value.fontFamily;
      if (!isUserFamilyId(fam) && !getFontById(fam)) {
        this.value.fontFamily = DEFAULT_USER_SETTINGS.fontFamily;
      }
      this.#lastSaved = { ...this.value };
      this.#revision += 1;
      this.#loaded = true;
    } catch {
      // Keep defaults if settings cannot be loaded.
    } finally {
      if (this.#loadController === controller) {
        this.#loadController = undefined;
        this.#loadPromise = undefined;
      }
    }
  }

  update(partial: Partial<UserSettings>): void {
    // Mutate fields in place on the deep-reactive $state proxy instead of
    // replacing the whole object. A wholesale reassign invalidates *every*
    // settings.value consumer (isPaged, fontFaceCSS / buildAllFontFaces, the
    // iframe derived -> apply-settings postMessage) on every change; per-field
    // assignment only wakes the consumers that read the changed fields, so a
    // font-size / line-height slider drag no longer rebuilds @font-face CSS or
    // re-derives unrelated values each frame.
    Object.assign(this.value, partial);
    this.#revision += 1;
    this.#scheduleSave();
  }

  /** Call on logout so the next login gets fresh settings from the server. */
  reset(): void {
    this.#loaded = false;
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
    this.value = { ...DEFAULT_USER_SETTINGS };
    this.#lastSaved = { ...DEFAULT_USER_SETTINGS };
  }

  /**
   * Restore every setting to its default and persist. Unlike reset() (which is
   * for logout and drops the loaded flag), this keeps the session live and
   * schedules a save so the server row is overwritten with the defaults. A
   * fresh fontRoles object avoids sharing the module-level default map.
   */
  resetToDefaults(): void {
    this.update({ ...DEFAULT_USER_SETTINGS, fontRoles: {} });
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
      const snapshot = { ...this.value };
      const revision = this.#revision;
      saveSettings(snapshot, controller.signal)
        .then(() => {
          // Remember the last payload the server accepted.
          this.#lastSaved = snapshot;
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
            Object.assign(this.value, this.#lastSaved);
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
