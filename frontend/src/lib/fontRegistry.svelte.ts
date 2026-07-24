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

/** A quoted CSS string for the family name encoded in a user-family id. */
export function userFamilyCSSName(id: string): string {
  let escaped = "";
  for (const char of userFamilyDir(id)) {
    const codePoint = char.codePointAt(0) ?? 0;
    if (char === "'" || char === "\\") {
      escaped += `\\${char}`;
    } else if (codePoint === 0) {
      escaped += "\uFFFD";
    } else if (codePoint <= 0x1f || codePoint === 0x7f) {
      escaped += `\\${codePoint.toString(16)} `;
    } else {
      escaped += char;
    }
  }
  return `'${escaped}'`;
}

/** CSS font-family value for a user family (quoted name + category fallback). */
export function userFamilyCSSValue(fam: UserFontFamily): string {
  const fallback = fam.category === "sans-serif" ? "sans-serif" : "serif";
  return `${userFamilyCSSName(fam.id)}, ${fallback}`;
}

class FontRegistry {
  families = $state<UserFontFamily[]>([]);

  /** True once a load/rescan has succeeded. Reactive (unlike a plain field) so
   *  consumers can tell "registry not loaded yet" apart from "loaded with no
   *  user fonts". A failed load leaves this false so the next attempt retries. */
  loaded = $state(false);

  /** Shared in-flight load so concurrent boot callers don't double-fetch. */
  private loadPromise: Promise<void> | null = null;

  /** Serializes reads and rescans so an older response cannot publish last. */
  private operationTail: Promise<void> = Promise.resolve();

  private enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.operationTail.then(operation, operation);
    this.operationTail = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  async load(): Promise<void> {
    if (this.loaded) return;
    // Without this, two near-simultaneous boot callers would each fire
    // GET /fonts because `loaded` only flips after the await. Share the first
    // in-flight request; clear it on settle so a failed load still retries.
    if (this.loadPromise) return this.loadPromise;
    const request = this.enqueue(async () => {
      // A rescan queued before this load may already have populated the store.
      if (this.loaded) return;
      try {
        this.families = await getFonts();
        this.loaded = true;
      } catch {
        // No user fonts is a normal, non-fatal state.
      }
    });
    this.loadPromise = request;
    try {
      await request;
    } finally {
      if (this.loadPromise === request) this.loadPromise = null;
    }
  }

  /** Returns true on success, false if the rescan failed (previous list kept). */
  async rescan(): Promise<boolean> {
    return this.enqueue(async () => {
      try {
        this.families = await rescanFonts();
        this.loaded = true;
        return true;
      } catch {
        // Keep the previous list on failure.
        return false;
      }
    });
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
