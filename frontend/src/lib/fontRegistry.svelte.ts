// Registry of user-supplied font families discovered by the backend under
// ./Fonts/. Embedded fonts remain a static constant (READER_FONTS); this store
// only tracks the dynamic, host-installed families and exposes helpers to
// resolve their CSS family name and id.

import { getFonts, rescanFonts, type UserFontFamily } from "~/api/client";

/** The "user:" prefix marks a family that lives in ./Fonts/ rather than embedded. */
export const USER_FONT_PREFIX = "user:";

export function isUserFamilyId(id: string): boolean {
  return id.startsWith(USER_FONT_PREFIX);
}

/** Directory segment for a user family id ("user:MinionPro" -> "MinionPro"). */
export function userFamilyDir(id: string): string {
  return id.slice(USER_FONT_PREFIX.length);
}

/** CSS font-family value for a user family (quoted name + category fallback). */
export function userFamilyCSSValue(fam: UserFontFamily): string {
  const fallback = fam.category === "sans-serif" ? "sans-serif" : "serif";
  return `'${userFamilyDir(fam.id)}', ${fallback}`;
}

class FontRegistry {
  families = $state<UserFontFamily[]>([]);

  /** True once a load/rescan has succeeded. Reactive (unlike a plain field) so
   *  consumers can tell "registry not loaded yet" apart from "loaded with no
   *  user fonts". A failed load leaves this false so the next attempt retries. */
  loaded = $state(false);

  async load(): Promise<void> {
    if (this.loaded) return;
    try {
      this.families = await getFonts();
      this.loaded = true;
    } catch {
      // No user fonts is a normal, non-fatal state.
    }
  }

  /** Returns true on success, false if the rescan failed (previous list kept). */
  async rescan(): Promise<boolean> {
    try {
      this.families = await rescanFonts();
      this.loaded = true;
      return true;
    } catch {
      // Keep the previous list on failure.
      return false;
    }
  }

  get(id: string): UserFontFamily | undefined {
    return this.families.find((f) => f.id === id);
  }

  /** Resolves a user family id to its CSS font-family value, or null if unknown. */
  cssValue(id: string): string | null {
    const fam = this.get(id);
    return fam ? userFamilyCSSValue(fam) : null;
  }
}

export const fontRegistry = new FontRegistry();
