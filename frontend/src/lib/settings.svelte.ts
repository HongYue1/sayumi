import {
  ApiError,
  getSettings,
  saveSettings,
  type UserSettings,
} from "~/api/client";
import { getFontById, getFontFamily } from "~/lib/fonts";
import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry.svelte";
import { toast } from "~/lib/toast.svelte";

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
  contentWidth: number | null;
  margins: { top: number | null; bottom: number | null; side: number | null };
  justify: boolean;
  hyphenation: boolean;
  theme: string;
  chapterTitleAlign: "left" | "center" | "right" | null;
  chapterTitleSize: number | null;
  chapterTitleSpacing: number | null;
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
  fontSize: 26,
  fontFamily: "literata",
  lineHeight: null,
  paragraphSpacing: null,
  textIndent: null,
  contentWidth: null,
  displayMode: "scroll",
  marginTop: 48,
  marginBottom: 48,
  marginSide: 48,
  preserveStyles: true,
  preserveFonts: false,
  justify: true,
  hyphenation: false,
  theme: "rose-pine",
  chapterTitleAlign: null,
  chapterTitleSize: null,
  chapterTitleSpacing: null,
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

export function toIframeSettings(s: UserSettings): IframeSettings {
  return {
    mode: s.displayMode,
    fontSize: s.fontSize,
    fontFamily: resolveFontFamily(s.fontFamily),
    preserveBookStyles: s.preserveStyles,
    preserveBookFonts: s.preserveFonts,
    lineHeight: s.lineHeight,
    paragraphSpacing: s.paragraphSpacing,
    textIndent: s.textIndent,
    contentWidth: s.contentWidth,
    margins: { top: s.marginTop, bottom: s.marginBottom, side: s.marginSide },
    justify: s.justify,
    hyphenation: s.hyphenation,
    theme: s.theme,
    chapterTitleAlign: s.chapterTitleAlign,
    chapterTitleSize: s.chapterTitleSize,
    chapterTitleSpacing: s.chapterTitleSpacing,
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
  iframe = $derived.by<IframeSettings>(() => toIframeSettings(this.value));

  #loaded = false;
  #saveTimer: ReturnType<typeof setTimeout> | undefined;
  #saveController: AbortController | undefined;
  // Last server-accepted settings. A failed PUT sends the whole object, so a
  // single invalid field would otherwise wedge every later save; on a 4xx we
  // roll back to this snapshot.
  #lastSaved: UserSettings = { ...DEFAULT_USER_SETTINGS };

  /** Loads server settings once; keeps defaults on failure (non-fatal). */
  async load(): Promise<void> {
    if (this.#loaded) return;
    try {
      const loaded = await getSettings();
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
      this.#loaded = true;
    } catch {
      // Keep defaults if settings cannot be loaded.
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
    this.#scheduleSave();
  }

  /** Call on logout so the next login gets fresh settings from the server. */
  reset(): void {
    this.#loaded = false;
    if (this.#saveTimer) {
      clearTimeout(this.#saveTimer);
      this.#saveTimer = undefined;
    }
    this.#saveController?.abort();
    this.#saveController = undefined;
    this.value = { ...DEFAULT_USER_SETTINGS };
    this.#lastSaved = { ...DEFAULT_USER_SETTINGS };
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
