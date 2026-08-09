// App shell: session boot, profile-owned store activation, global shortcuts,
// and top-level routing. Ported from App.svelte.
//
// Solid 2.0 notes:
//   - onMount -> onSettled. Its callback may return a cleanup function, which
//     is how the removed <svelte:window onkeydown> binding is replaced.
//   - createEffect takes a compute/apply pair (the single-argument form is
//     typed to return never). Only the compute phase tracks, so
//     session.profile is the sole dependency; everything the apply phase and
//     the async continuation read stays untracked, which matches the Svelte
//     $effect -- its reads inside .then() were untracked there too.
//   - {#if}/{:else if}/{:else} -> <Switch>/<Match>, with the final else as the
//     fallback. {#key expr} -> <Show keyed>, which remounts on a new identity.
import { createEffect, Match, onSettled, Show, Switch } from "solid-js";
import { session } from "~/lib/session";
import { router } from "~/lib/router";
import { ui } from "~/lib/ui";
import { keyboardEventIsOwnedByTarget } from "~/lib/keyboard";
import { settings } from "~/lib/settings";
import { applyTheme, getCachedThemeId, themeReady } from "~/lib/theme";
import { customThemes } from "~/lib/customThemes";
import { library } from "~/lib/library";
import Login from "~/routes/Login";
import Library from "~/routes/Library";
import Read from "~/routes/Read";
import Toaster from "~/components/Toaster";
import OfflineBanner from "~/components/OfflineBanner";
import CommandPalette from "~/components/CommandPalette";
import ShortcutsHelp from "~/components/ShortcutsHelp";

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

// Extracted from the Svelte $effect's .then() callback so the promise chain has
// no callback to lint (promise/always-return) and the await point is explicit.
async function syncProfileOwnedState(profile: string | null): Promise<void> {
  await customThemes.activate(profile);
  // Whichever profile-owned request finishes last (settings or custom themes)
  // gets a chance to resolve the saved id against the complete registry. Keep
  // this tied to activation rather than a global theme effect: Library and Read
  // still own normal theme changes.
  //
  // themeReady() (lib/theme.ts) carries the why: both loads must have
  // SUCCEEDED, not merely finished, or the apply paints and persists the
  // compile-time default over the user's saved theme. Library.tsx fixed this
  // on its own boot path first; the copy here was left behind until b50.
  if (profile !== null && session.profile === profile && themeReady()) {
    applyTheme(settings.value.theme);
  }
}

export default function App() {
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
  // Library owns a second activation call (Library.tsx:203) because its child
  // effects can run before this one after a full-page refresh. activate() is
  // idempotent per profile (library.ts:273), so the duplication is ordering
  // insurance rather than a race (X54).
  createEffect(
    () => session.profile,
    (profile) => {
      library.activate(profile);
      if (profile === null) ui.closeOverlays();
      void syncProfileOwnedState(profile);
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
                id instead of reusing the instance, which is what {#key} did. */}
            <Show when={router.route.params.id} keyed>
              {(id) => <Read bookId={id} />}
            </Show>
          </Match>
        </Switch>
      </main>

      <CommandPalette />
      <ShortcutsHelp />
      <Toaster />
    </>
  );
}
