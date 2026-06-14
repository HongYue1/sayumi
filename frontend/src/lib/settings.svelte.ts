import { getSettings, saveSettings, type UserSettings } from "~/api/client";
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
  };
}

class Settings {
  value = $state<UserSettings>({ ...DEFAULT_USER_SETTINGS });
  /** Memoised mapping to the reader-iframe settings shape. */
  iframe = $derived.by<IframeSettings>(() => toIframeSettings(this.value));

  #loaded = false;
  #saveTimer: ReturnType<typeof setTimeout> | undefined;
  #saveController: AbortController | undefined;

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
      this.#loaded = true;
    } catch {
      // Keep defaults if settings cannot be loaded.
    }
  }

  update(partial: Partial<UserSettings>): void {
    this.value = { ...this.value, ...partial };
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
        .catch((error: unknown) => {
          if (error instanceof DOMException && error.name === "AbortError")
            return;
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
