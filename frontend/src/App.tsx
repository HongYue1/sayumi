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
import {
  createEffect,
  createSignal,
  Errored,
  Match,
  onSettled,
  Show,
  Switch,
} from "solid-js";
import { getErrorMessage } from "~/lib/errors";
import { session } from "~/lib/session";
import { router } from "~/lib/router";
import { ui } from "~/lib/ui";
import { isRichTextHost, keyboardEventIsOwnedByTarget } from "~/lib/keyboard";
import { settings } from "~/lib/settings";
import { applyTheme, getCachedThemeId, previewTheme } from "~/lib/theme";
import { getTheme, isBuiltInTheme } from "~/lib/themes";
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
//
// Module scope is deliberate: one shell exists per document, so a single
// stable reference is what makes the attach in onSettled and the detach in
// its cleanup a matched pair. A per-instance closure would only change
// behaviour for a second concurrent shell, which is a bug in its own right.
function onWindowKey(e: KeyboardEvent): void {
  if (!session.authenticated) return;
  // A held key is not a second request. Auto-repeat delivers one keydown per
  // repeat, each in its own tick, so the palette chord below would flip open
  // and shut at the OS repeat rate and settle wherever the key came up. The
  // batching that masks two toggles raised within a single keydown
  // (lib/ui.ts) cannot help across ticks.
  if (e.repeat) return;
  // AltGr arrives as ctrlKey+altKey on Windows and most Linux layouts, where
  // it is an ordinary character modifier: AltGr+K types a character on Polish,
  // Croatian and Vietnamese layouts, and claiming it here would open the
  // palette and swallow the keystroke. frame.ts:1302 already excludes it inside
  // the book iframe; this is the parent-document half of the same rule. The
  // "?" branch below deliberately does NOT exclude altKey -- AltGr is how "?"
  // is typed on several layouts, so excluding it there would break the
  // shortcut instead of protecting it.
  //
  // The chord is checked before the target stand-down below because the help
  // sheet calls it global and readers expect it to be: typing a filter in the
  // library search and reaching for ⌘K used to do nothing at all. A plain
  // input has no native binding for it. Rich-text hosts still keep their veto
  // — Ctrl/⌘+K is the conventional "insert link" there — as does composition.
  if (
    (e.ctrlKey || e.metaKey) &&
    !e.altKey &&
    (e.key === "k" || e.key === "K") &&
    !e.isComposing &&
    !isRichTextHost(e.target) &&
    !isRichTextHost(document.activeElement)
  ) {
    e.preventDefault();
    ui.togglePalette();
    return;
  }
  if (keyboardEventIsOwnedByTarget(e, document.activeElement)) return;
  if (e.key === "?" && !e.ctrlKey && !e.metaKey) {
    e.preventDefault();
    ui.openShortcuts();
  }
}

export default function App() {
  let appliedThemeKey: string | null = null;
  // Whether the current chrome paint came from an unsaved draft. A draft is the
  // one palette no later run is guaranteed to replace, so the stand-down below
  // has to undo it explicitly.
  let paintedDraft = false;

  onSettled(() => {
    // Re-apply the cached theme (already set pre-paint by the index.html
    // bootstrap) so SPA state and data-theme stay in sync; falls back to the
    // default theme for a fresh visitor, which is the palette app.css already
    // painted. The saved server theme is applied once settings load.
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
        return {
          profile,
          id: draft.id,
          theme: draft,
          draft: true,
          dead: false,
        };
      }
      // A custom theme deleted from another tab or device leaves a dead id
      // here. getTheme resolves it to the light fallback, but applyTheme
      // refuses to paint a fallback under a foreign id -- it reuses the
      // pre-paint cache and waits for a later paint that, for an id no
      // registry will ever hold again, never comes. The shell would stay
      // painted in a deleted theme, the menu would check nothing, and the
      // reader payload would ship themeVars: null. Repair the id instead,
      // but only once the registry is loaded enough to prove the absence.
      // The deleted definition's group died with it, so getTheme's fallback
      // is the only honest target.
      const id = settings.value.theme;
      const dead =
        customThemes.loaded && !isBuiltInTheme(id) && !customThemes.get(id);
      return { profile, id, theme: getTheme(id), draft: false, dead };
    },
    (active) => {
      if (active === null) {
        // A draft outlives its editor. Sign-out unmounts the reader route, and
        // with it the dialog whose teardown clears the draft -- but this
        // resolver stands down in the same tick and nothing else paints chrome,
        // so the login screen would keep a palette the user never saved, down
        // to data-theme="draft-preview". Revert to the pre-paint cache, the
        // same source boot paints from.
        if (paintedDraft) applyTheme(getCachedThemeId());
        paintedDraft = false;
        appliedThemeKey = null;
        return undefined;
      }
      const { profile, id, theme, draft, dead } = active;
      if (dead) {
        // settings.update mutates its store synchronously, so this effect's
        // next compute sees the replacement id and the branch stands down:
        // one write, no loop. Painting is left to that run, so the repair
        // never flashes a theme the user did not choose.
        settings.update({ theme: theme.id });
        return undefined;
      }
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
        paintedDraft = draft;
        // previewTheme paints without writing the pre-paint cache, so a draft
        // cannot survive a reload.
        if (draft) previewTheme(theme);
        else applyTheme(id, theme);
      }
      return undefined;
    },
  );

  // A caught render error replaces everything inside the boundary, so a
  // role on the fallback card would arrive in the same tick as its copy and
  // never be announced. The fallback mirrors its message into the assertive
  // region instead -- that region sits outside the boundary, so it predates
  // the failure -- and clears it again when reset() rebuilds the subtree.
  const [boundaryFailure, setBoundaryFailure] = createSignal("");
  const boundaryFallbackMessage =
    "Sayumi stopped drawing this page. Retrying rebuilds it from the current state.";
  const MirrorBoundaryFailure = (props: { message: string }) => {
    createEffect(
      () => props.message,
      (message) => {
        setBoundaryFailure(message);
        return undefined;
      },
    );
    // Clearing on teardown is load-bearing: reset() unmounts this without
    // re-running the effect, so a stale message would otherwise sit in an
    // assertive region for the rest of the session.
    onSettled(() => () => setBoundaryFailure(""));
    return null;
  };

  // The chrome outside <main> -- the offline banner above it, the overlay
  // layer below it -- gets boundaries of its own, sharing one sentence.
  // Neither offers a retry: they hold no content of their own, so a reload is
  // the recovery.
  const chromeFallbackMessage =
    "Sayumi stopped drawing part of the app chrome. Reload the page to bring it back.";
  // Fallback for the overlay layer: no visible UI, plus a stand-down of the
  // flags whose overlays are no longer on screen.
  const OverlayFailure = (props: { message: string }) => {
    onSettled(() => {
      ui.closeOverlays();
    });
    return <MirrorBoundaryFailure message={props.message} />;
  };

  // Text for the two pre-mounted regions below. Both boot states mount
  // together with their copy, which NVDA and JAWS do not announce (WCAG
  // 4.1.3), so neither arm carries a role and these regions -- in the
  // accessibility tree from first paint -- speak for them. Progress stays
  // polite; the blocking failure keeps the assertive severity the card
  // itself used to carry.
  const bootProgress = (): string =>
    session.status === "checking" ? "Checking sign-in status\u2026" : "";
  const bootFailure = (): string =>
    boundaryFailure() ||
    (session.status === "unavailable"
      ? "Sayumi is unavailable. Your sign-in status is unknown because the server could not be reached."
      : "");

  return (
    <>
      {/* The banner renders above <main>, which puts it outside <main>'s
          boundary: a throw here would reach the render root and take the
          routes down with it. It keeps a boundary of its own because a
          failing banner says nothing about the overlays, and vice versa. */}
      <Errored
        fallback={(err) => (
          <MirrorBoundaryFailure
            message={`Something went wrong. ${getErrorMessage(
              err(),
              chromeFallbackMessage,
            )}`}
          />
        )}
      >
        <OfflineBanner />
      </Errored>

      <main>
        <p class="sr-only" role="status">
          {bootProgress()}
        </p>
        <p class="sr-only" role="alert">
          {bootFailure()}
        </p>
        {/* One boundary around everything routing can render. Without it a
            throw while rendering a route -- or one travelling through the
            reactive graph -- tears down the subtree and leaves an empty
            <main> with no way back. reset() recomputes the failing sources,
            which is a real recovery for a transient fault and simply errors
            again for a persistent one. Errors raised inside event handlers
            are still handled where they happen (see lib/errors.ts). */}
        <Errored
          fallback={(err, reset) => (
            <div class="boot boot-unavailable">
              <MirrorBoundaryFailure
                message={`Something went wrong. ${getErrorMessage(
                  err(),
                  boundaryFallbackMessage,
                )}`}
              />
              <section
                class="boot-card paper"
                aria-labelledby="boot-error-title"
              >
                <p class="eyebrow">Unexpected error</p>
                <h1 id="boot-error-title" class="display">
                  Something went wrong
                </h1>
                <p>{getErrorMessage(err(), boundaryFallbackMessage)}</p>
                <button class="btn press" type="button" onClick={reset}>
                  Try again
                </button>
              </section>
            </div>
          )}
        >
          <Switch fallback={<Library />}>
            <Match when={session.status === "checking"}>
              {/* aria-busy only: the wording lives in the region above, which
                  existed before this placeholder did. */}
              <div class="boot" aria-busy="true" />
            </Match>
            <Match when={session.status === "unavailable"}>
              <div class="boot boot-unavailable">
                <section
                  class="boot-card paper"
                  aria-labelledby="boot-unavailable-title"
                >
                  <p class="eyebrow">Connection</p>
                  <h1 id="boot-unavailable-title" class="display">
                    Sayumi is unavailable
                  </h1>
                  <p>
                    Your sign-in status is unknown because the server could not
                    be reached.
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
        </Errored>
      </main>

      {/* The overlay layer renders after </main>, so the routing boundary
          cannot reach it either -- and these four read the same library,
          settings and theme data the routes do. Clearing the overlay flags is
          part of the fallback: nothing is left on screen for Escape to close,
          and the reader's keyboard stand-down reads anyOverlayOpen. That write
          also gives a layer whose failure needed an open overlay a chance to
          rebuild, since a boundary retries when its sources change, while a
          repeat write of already-false flags notifies nobody (lib/ui.ts) -- so
          a persistent failure settles instead of looping. */}
      <Errored
        fallback={(err) => (
          <OverlayFailure
            message={`Something went wrong. ${getErrorMessage(
              err(),
              chromeFallbackMessage,
            )}`}
          />
        )}
      >
        <CommandPalette />
        <ShortcutsHelp />
        <AboutDialog />
        <Toaster />
      </Errored>
    </>
  );
}
