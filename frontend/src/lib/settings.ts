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
  // theme that has no static frame.css class; null for built-ins (their class
  // supplies the tokens) and for ids no theme registry knows. See frame.ts.
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
  // A store, not a signal: store drafts track at property level (docs29 04),
  // so a per-field mutation wakes only the consumers reading that property — a
  // font-size drag does not re-run isPaged, buildAllFontFaces, or the iframe
  // mapping. Replacing the whole object would invalidate every consumer.
  readonly #store = createStore<UserSettings>(freshDefaults());
  readonly #loadedSignal = createSignal(false);

  /** Memoised mapping to the reader-iframe settings shape. */
  readonly #iframe: () => IframeSettings;

  /**
   * Non-reactive mirror of `loaded`, used by load()'s guard.
   *
   * Solid 2.0 batches writes (docs29 01), so a signal read immediately after a
   * write still returns the last committed value until the flush. Control flow
   * reads this field; the signal exists only so gated consumers re-render.
   */
  #loaded = false;
  /**
   * True only after a load has SUCCEEDED. `loaded` gates consumers on server
   * truth; this gates the save pipeline: a failed load keeps the defaults but
   * stays retryable, and update() defers saves until server state is in hand —
   * otherwise the next debounced save would PUT compile-time defaults plus one
   * edit over the user's server row (the full object is PUT every time).
   */
  #loadOk = false;
  /** Edits made before a successful load, re-applied over #load's merge. */
  #dirtyDuringLoad: Partial<UserSettings> = {};
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
    // Detached on purpose: in Solid 2.0 ownership is the default, so document
    // lifetime is an explicit opt-in via runWithOwner(null, ...) (docs29 02).
    // Unowned memos autodispose once they lose their last subscriber (docs29
    // 01) — every settings.iframe subscriber is route-scoped in Read.tsx, so
    // the memo tears down with the reader route and recomputes on the next
    // mount. Safe here because the derivation is pure (no cleanup, no side
    // effects), and while unsubscribed it skips recompute on settings writes.
    this.#iframe = runWithOwner(null, () =>
      createMemo(() => toIframeSettings(this.value, customThemes.list)),
    );

    // Flush a pending debounced save when the page hides: the trailing-only
    // 500 ms timer would otherwise drop the last edit on tab close. Reading
    // progress has a keepalive beacon for the same hazard (beaconProgress);
    // settings flush through a keepalive PUT. App-lifetime singleton — no
    // removal, matching the discarded subscribeUnauthenticated disposer in
    // session.ts.
    window.addEventListener("pagehide", () => this.#flushPendingSave());
  }

  /** Current settings. Property reads track individually. */
  get value(): UserSettings {
    return this.#store[0];
  }

  get iframe(): IframeSettings {
    return this.#iframe();
  }

  /** True once the initial load has settled SUCCESSFULLY (reactive). A failed
   *  load keeps this false (and retryable): consumers - e.g. the reader's
   *  applyTheme effect - must never act on the compile-time defaults, which
   *  would flash and cache the default theme over the user's saved one. */
  get loaded(): boolean {
    return this.#loadedSignal[0]();
  }

  #setLoaded(value: boolean): void {
    this.#loaded = value;
    this.#loadedSignal[1](value);
  }

  /**
   * Loads server settings once; keeps defaults on failure (non-fatal). Never
   * rejects. A failure is NOT terminal: `loaded` flips only on success, so the
   * next call refetches (Read boot, Library mount, and update()'s deferred
   * save all call this), unlike a terminal failure that would strand the
   * session on compile-time defaults.
   */
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
      // Merge over defaults defensively, so a partial payload (older server,
      // hand-edited row) still yields a complete settings object and a known
      // fontFamily for the font <select>. The current handler always sends
      // every key (settingsJSON has no omitempty), so this is forward-compat
      // armor, not a live failure mode.
      //
      // The merged object is assembled and validated as a plain local before it
      // reaches the store. Validating after the write would read the pre-write
      // value under Solid's batching and check the *previous* profile's font id,
      // silently defeating the coercion below. fontRoles gets its own copy —
      // the shallow spread would otherwise share the response's nested map
      // between the store and #lastSaved (see freshDefaults).
      const next: UserSettings = { ...freshDefaults(), ...loaded };
      next.fontRoles = { ...next.fontRoles };
      // Coerce an unusable built-in font id (empty or unknown) to the default.
      // User fonts ("user:" prefix) can't be validated until the registry has
      // loaded, so SettingsPanel reconciles those reactively once families load.
      if (!isUserFamilyId(next.fontFamily) && !getFontById(next.fontFamily)) {
        next.fontFamily = DEFAULT_USER_SETTINGS.fontFamily;
      }
      // Edits made before this load landed survive the merge — a theme picked
      // while the GET was in flight is not clobbered by the server response.
      Object.assign(next, this.#dirtyDuringLoad);
      this.#dirtyDuringLoad = {};
      this.#store[1]((s) => {
        Object.assign(s, next);
      });
      this.#lastSaved = { ...next, fontRoles: { ...next.fontRoles } };
      this.#revision += 1;
      this.#setLoaded(true);
      this.#loadOk = true;
    } catch {
      // Keep defaults and stay retryable: #loadOk/#loaded are only set on
      // success, so the next load() refetches. A failed load must not mark the
      // defaults as server truth — applyTheme would paint and cache them over
      // the user's saved theme, and a save would PUT them over the server row.
    } finally {
      if (this.#loadController === controller) {
        this.#loadController = undefined;
        this.#loadPromise = undefined;
      }
      // An edit arrived while this load was in flight and its save was
      // deferred. Retry now: a success republishes with the edits re-applied
      // (generation/abort guards make a logout-race retry a no-op publish).
      if (
        this.#loaded &&
        !this.#loadOk &&
        Object.keys(this.#dirtyDuringLoad).length > 0
      ) {
        void this.load().then(() => this.#scheduleSave());
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
    if (!this.#loadOk) {
      // The server row's state is not in hand (load pending or failed): record
      // the edit so #load's merge cannot clobber it, and defer the save —
      // PUTing now would persist defaults+edit over the user's saved settings.
      Object.assign(this.#dirtyDuringLoad, partial);
      void this.load().then(() => this.#scheduleSave());
      return;
    }
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
    this.#dirtyDuringLoad = {};
    this.#loadOk = false;
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
   * fresh fontRoles object avoids sharing the module-level default map. If no
   * load has succeeded yet the PUT defers through update()'s loadOk gate.
   */
  resetToDefaults(): void {
    this.update(freshDefaults());
  }

  /** Trailing-edge debounce: each update() re-arms the 500 ms timer. */
  #scheduleSave(): void {
    if (this.#saveTimer) clearTimeout(this.#saveTimer);
    this.#saveTimer = setTimeout(() => {
      this.#saveTimer = undefined;
      this.#saveNow();
    }, 500);
  }

  /**
   * pagehide flush: fire a pending debounced save immediately, with keepalive
   * so the PUT outlives the page (the same contract beaconProgress uses for
   * reading progress). A save already in flight finishes on its own.
   */
  #flushPendingSave(): void {
    if (!this.#saveTimer) return;
    clearTimeout(this.#saveTimer);
    this.#saveTimer = undefined;
    this.#saveNow(true);
  }

  #saveNow(keepalive = false): void {
    // Supersede any in-flight save so a slow older request can't land after a
    // newer one and clobber the latest settings.
    this.#saveController?.abort();
    const controller = new AbortController();
    this.#saveController = controller;
    // snapshot() is the 2.0 replacement for unwrap(): a plain, non-tracking
    // value. Read the raw store node, never this.value — that getter exists to
    // create subscriptions, and reading it inside a tracked scope would
    // subscribe where snapshot() is meant not to. Spread so the payload can't
    // alias the live store; copy fontRoles so it shares no nested container
    // with store state or #lastSaved.
    const snap = snapshot(this.#store[0]);
    const payload: UserSettings = {
      ...snap,
      fontRoles: { ...(snap.fontRoles ?? {}) },
    };
    const revision = this.#revision;
    saveSettings(payload, controller.signal, keepalive)
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
        // The handler's only payload-driven rejection is the 400 from
        // validateSettings (internal/api/setting.go); the guard treats every
        // 4xx as a permanent rejection (a 401 has already torn the session
        // down via the epoch guard, so the rollback is moot there). A
        // permanently rejected payload would fail again on the next edit,
        // since the full object is PUT every time — roll back to the last
        // accepted settings so one bad field can't block every future save.
        if (
          error instanceof ApiError &&
          error.status != null &&
          error.status >= 400 &&
          error.status < 500
        ) {
          const lastSaved = this.#lastSaved;
          this.#store[1]((s) => {
            // Re-copy the nested map: the store must never alias the
            // fontRoles object #lastSaved holds.
            Object.assign(s, {
              ...lastSaved,
              fontRoles: { ...(lastSaved.fontRoles ?? {}) },
            });
          });
        }
        toast.show("Couldn't save settings");
      })
      .finally(() => {
        if (this.#saveController === controller)
          this.#saveController = undefined;
      });
  }
}

export const settings = new Settings();
