// App shell: session boot, profile-owned store activation, global shortcuts,
// and top-level routing.
//
// Solid 2.0 notes:
//   - onMount -> onSettled. Its callback may return a cleanup function, which
//     is how the global keydown listener is torn down.
//   - createEffect takes a compute/apply pair (the single-argument form is
//     typed to return never). Only the compute phase tracks, so
//     session.profile is the sole dependency; everything the apply phase and
//     the async continuation read stays untracked.
import { createEffect, Match, onSettled, Show, Switch } from "solid-js";
import { session } from "~/lib/session";
import { router } from "~/lib/router";
import { ui } from "~/lib/ui";
import { keyboardEventIsOwnedByTarget } from "~/lib/keyboard";
import { settings } from "~/lib/settings";
import { applyTheme, getCachedThemeId, previewTheme } from "~/lib/theme";
import { getTheme } from "~/lib/themes";
import { themePreview } from "~/lib/themePreview";
import { customThemes } from "~/lib/customThemes";
import { library } from "~/lib/library";
import Login from "~/routes/Login";
import Library from "~/routes/Library";
import Read from "~/routes/Read";
import Toaster from "~/components/Toaster";
import OfflineBanner from "~/components/OfflineBanner";
import CommandPalette from "~/components/CommandPalette";
import ShortcutsHelp from "~/components/ShortcutsHelp";
import AboutDialog from "~/components/AboutDialog";

// Global shortcuts. Only active once signed in. Composition and controls
// that own the key stand down through the same contract as Read and frame.ts.
function onWindowKey(e: KeyboardEvent): void {
  if (
    !session.authenticated ||
    keyboardEventIsOwnedByTarget(e, document.activeElement)
  ) {
    return;
  }
  // AltGr arrives as ctrlKey+altKey on Windows and most Linux layouts, where
  // it is an ordinary character modifier: AltGr+K types a character on Polish,
  // Croatian and Vietnamese layouts, and claiming it here would open the
  // palette and swallow the keystroke. frame.ts:1302 already excludes it inside
  // the book iframe; this is the parent-document half of the same rule. The
  // "?" branch below deliberately does NOT exclude altKey -- AltGr is how "?"
  // is typed on several layouts, so excluding it there would break the
  // shortcut instead of protecting it.
  if (
    (e.ctrlKey || e.metaKey) &&
    !e.altKey &&
    (e.key === "k" || e.key === "K")
  ) {
    e.preventDefault();
    ui.togglePalette();
    return;
  }
  if (e.key === "?" && !e.ctrlKey && !e.metaKey) {
    e.preventDefault();
    ui.openShortcuts();
  }
}

export default function App() {
  let appliedThemeKey: string | null = null;

  onSettled(() => {
    // Re-apply the cached theme (already set pre-paint by the index.html
    // bootstrap) so SPA state and data-theme stay in sync; falls back to light
    // for a fresh visitor. The saved server theme is applied once settings load.
    applyTheme(getCachedThemeId());
    void session.init();
    window.addEventListener("keydown", onWindowKey);
    return () => window.removeEventListener("keydown", onWindowKey);
  });

  // Keep profile-owned singleton state aligned with the active session. A
  // profile change clears old library/custom-theme data immediately, and each
  // store generation-guards async work so a late response cannot publish into
  // the new profile. Closing global overlays on sign-out/session loss also
  // keeps stale commands and focus traps off the login screen.
  //
  // Library owns a second activation call (Library.tsx's onSettled) because
  // its child effects can run before this one after a full-page refresh.
  // activate() is idempotent per profile (library.ts activate()), so the
  // duplication is ordering insurance rather than a race.
  createEffect(
    () => session.profile,
    (profile) => {
      library.activate(profile);
      void settings.activate(profile);
      void customThemes.activate(profile);
      if (profile === null) ui.closeOverlays();
    },
  );

  // One tracked resolver owns app-chrome repainting. The settings gate protects
  // the pre-paint cache from compile-time defaults after a failed GET; getTheme
  // subscribes to custom-registry revisions, so loading or editing the active
  // custom theme repaints even though its id did not change. Passing the
  // definition into applyTheme keeps every reactive read in the compute phase.
  createEffect(
    () => {
      const profile = session.profile;
      if (
        profile === null ||
        !settings.loaded ||
        !settings.isReadyFor(profile)
      ) {
        return null;
      }
      // An open custom-theme dialog publishes its unsaved palette to
      // themePreview, so the chrome repaints on every color change and reverts
      // the instant the draft clears -- without that dialog painting anything
      // itself. This effect stays the only painter of app chrome.
      const draft = themePreview();
      if (draft !== null) {
        return { profile, id: draft.id, theme: draft, draft: true };
      }
      const id = settings.value.theme;
      return { profile, id, theme: getTheme(id), draft: false };
    },
    (active) => {
      if (active === null) {
        appliedThemeKey = null;
        return undefined;
      }
      const { profile, id, theme, draft } = active;
      const key = [
        // Part of the key so leaving preview repaints the saved theme even when
        // the draft happened to resolve to the same id and colors.
        draft ? "draft" : "saved",
        profile,
        id,
        theme.id,
        theme.group,
        theme.bg,
        theme.fg,
        theme.accent,
        theme.surface ?? "",
      ].join("\u0000");
      if (key !== appliedThemeKey) {
        appliedThemeKey = key;
        // previewTheme paints without writing the pre-paint cache, so a draft
        // cannot survive a reload.
        if (draft) previewTheme(theme);
        else applyTheme(id, theme);
      }
      return undefined;
    },
  );

  return (
    <>
      <OfflineBanner />

      <main>
        <Switch fallback={<Library />}>
          <Match when={session.status === "checking"}>
            <div class="boot" role="status" aria-busy="true">
              <span class="sr-only">Checking sign-in status…</span>
            </div>
          </Match>
          <Match when={session.status === "unavailable"}>
            <div class="boot boot-unavailable">
              <section
                class="boot-card paper"
                role="alert"
                aria-labelledby="boot-unavailable-title"
              >
                <p class="eyebrow">Connection</p>
                <h1 id="boot-unavailable-title" class="display">
                  Sayumi is unavailable
                </h1>
                <p>
                  Your sign-in status is unknown because the server could not be
                  reached.
                </p>
                <button
                  class="btn press"
                  type="button"
                  onClick={() => void session.init()}
                >
                  Try again
                </button>
              </section>
            </div>
          </Match>
          <Match when={session.status === "signed-out"}>
            <Login />
          </Match>
          <Match when={router.route.path === "/read/:id"}>
            {/* `keyed` is the point here, not the guard: matchRoute only
                produces this path with a non-empty id (router.ts:20-25), so
                the Show cannot fall through. Keyed remounts Read on a new book
                id instead of reusing the instance. */}
            <Show when={router.route.params.id} keyed>
              {(id) => <Read bookId={id} />}
            </Show>
          </Match>
        </Switch>
      </main>

      <CommandPalette />
      <ShortcutsHelp />
      <AboutDialog />
      <Toaster />
    </>
  );
}
