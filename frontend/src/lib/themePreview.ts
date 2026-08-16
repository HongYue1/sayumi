// Ephemeral "theme being edited right now" draft: the palette CustomThemeDialog
// has in its color pickers, before anything is saved.
//
// It is a store rather than a pair of direct paint calls because both surfaces
// that render a theme already have exactly one painter each, and neither may
// gain a second one:
//   - App chrome: App.tsx's resolver effect (the only applyTheme caller).
//   - Reader frame: settings.iframe -> apply-settings (the only themeVars
//     source).
// The dialog publishes here; both painters read it and follow.
//
// Never persisted. The chrome paints a draft through previewTheme (lib/theme.ts),
// which deliberately skips the pre-paint localStorage cache, so a tab closed
// mid-edit reopens on the saved theme instead of a palette that never existed.
import { createSignal } from "solid-js";
import type { ThemeDef } from "~/lib/themes";

/**
 * Id the draft carries. Not a real theme id: it never reaches the server, and
 * it must not collide with a built-in (THEMES) or a stored custom id, because
 * the reader frame derives its html.theme-<id> class from it.
 */
export const PREVIEW_THEME_ID = "draft-preview";

const [draft, setDraft] = createSignal<ThemeDef | null>(null);

/** The palette being previewed, or null when nothing is being edited. */
export function themePreview(): ThemeDef | null {
  return draft();
}

/**
 * Publish a draft palette, or clear the preview with null. Callers must clear
 * it on close, save and unmount -- a stranded draft would outlive its dialog
 * and pin the whole app to a palette with no editor attached to it.
 */
export function setThemePreview(def: ThemeDef | null): void {
  setDraft(def);
}
