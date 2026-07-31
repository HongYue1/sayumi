// Registry of user-supplied font families discovered by the backend under
// ./Fonts/. Embedded fonts remain a static constant (READER_FONTS); this store
// only tracks the dynamic, host-installed families and exposes helpers to
// resolve their CSS family name and id.

import { createSignal } from "solid-js";
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
  // Signal tuples are held directly rather than destructured so the class keeps
  // one field per piece of state and no guessed helper type names.
  readonly #familiesSignal = createSignal<UserFontFamily[]>([]);
  readonly #loadedSignal = createSignal(false);

  /**
   * Plain mirror of the loaded flag, used by synchronous control flow.
   *
   * Solid 2.0 batches writes: a signal read immediately after a write still
   * returns the pre-write value until the microtask flush. Guards that decide
   * whether to start work must therefore read this plain field, never the
   * accessor. The signals exist purely so the UI re-renders.
   */
  #loadedPlain = false;

  /** Shared in-flight load so concurrent boot callers don't double-fetch. */
  #loadPromise: Promise<void> | null = null;

  /** Serializes reads and rescans so an older response cannot publish last. */
  #operationTail: Promise<void> = Promise.resolve();

  /** True once a load/rescan has succeeded. Reactive so consumers can tell
   *  "registry not loaded yet" apart from "loaded with no user fonts". A failed
   *  load leaves this false so the next attempt retries. */
  get loaded(): boolean {
    return this.#loadedSignal[0]();
  }

  get families(): UserFontFamily[] {
    return this.#familiesSignal[0]();
  }

  #publish(families: UserFontFamily[], loaded: boolean): void {
    this.#loadedPlain = loaded;
    this.#familiesSignal[1](families);
    this.#loadedSignal[1](loaded);
  }

  #enqueue<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.#operationTail.then(operation, operation);
    this.#operationTail = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  async load(): Promise<void> {
    if (this.#loadedPlain) return;
    // Without this, two near-simultaneous boot callers would each fire
    // GET /fonts because the loaded flag only flips after the await. Share the
    // first in-flight request; clear it on settle so a failed load retries.
    if (this.#loadPromise) return this.#loadPromise;
    const request = this.#enqueue(async () => {
      // A rescan queued before this load may already have populated the store.
      if (this.#loadedPlain) return;
      try {
        this.#publish(await getFonts(), true);
      } catch {
        // No user fonts is a normal, non-fatal state.
      }
    });
    this.#loadPromise = request;
    try {
      await request;
    } finally {
      if (this.#loadPromise === request) this.#loadPromise = null;
    }
  }

  /** Returns true on success, false if the rescan failed (previous list kept). */
  async rescan(): Promise<boolean> {
    return this.#enqueue(async () => {
      try {
        this.#publish(await rescanFonts(), true);
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
