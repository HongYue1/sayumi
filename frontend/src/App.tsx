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
import { settings } from "~/lib/settings";
import { applyTheme, getCachedThemeId } from "~/lib/theme";
import { customThemes } from "~/lib/customThemes";
import { library } from "~/lib/library";
import Login from "~/routes/Login";
import Library from "~/routes/Library";
import Read from "~/routes/Read";
import Toaster from "~/components/Toaster";
import OfflineBanner from "~/components/OfflineBanner";
import CommandPalette from "~/components/CommandPalette";
import ShortcutsHelp from "~/components/ShortcutsHelp";

// Global shortcuts. Only active once signed in; ignored while typing so the
// palette doesn't hijack normal text entry.
function onWindowKey(e: KeyboardEvent): void {
  if (!session.authenticated) return;
  if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
    e.preventDefault();
    ui.togglePalette();
    return;
  }
  // Narrowed with instanceof rather than the Svelte version's cast: .svelte
  // files were never linted, but .tsx is, and no-unsafe-type-assertion is an
  // error here.
  const active = document.activeElement;
  const tag = active instanceof HTMLElement ? active.tagName : "";
  const typing = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
  if (e.key === "?" && !typing && !e.ctrlKey && !e.metaKey) {
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
  if (profile !== null && session.profile === profile && customThemes.loaded) {
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
          <Match when={!session.ready}>
            <div class="boot" role="status" aria-busy="true">
              <span class="sr-only">Loading…</span>
            </div>
          </Match>
          <Match when={!session.authenticated}>
            <Login />
          </Match>
          <Match when={router.route.path === "/read/:id"}>
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
