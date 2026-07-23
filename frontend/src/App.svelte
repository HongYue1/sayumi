<script lang="ts">
  import { onMount } from "svelte";
  import { session } from "~/lib/session.svelte";
  import { router } from "~/lib/router.svelte";
  import { ui } from "~/lib/ui.svelte";
  import { settings } from "~/lib/settings.svelte";
  import { applyTheme } from "~/lib/theme";
  import { customThemes } from "~/lib/customThemes.svelte";
  import Login from "~/routes/Login.svelte";
  import Library from "~/routes/Library.svelte";
  import Read from "~/routes/Read.svelte";
  import Toaster from "~/components/Toaster.svelte";
  import OfflineBanner from "~/components/OfflineBanner.svelte";
  import CommandPalette from "~/components/CommandPalette.svelte";
  import ShortcutsHelp from "~/components/ShortcutsHelp.svelte";

  onMount(() => {
    // Re-apply the cached theme (already set pre-paint by the index.html
    // bootstrap) so SPA state and data-theme stay in sync; falls back to light
    // for a fresh visitor. The saved server theme is applied once settings load.
    applyTheme(localStorage.getItem("sayumi:theme") ?? "light");
    session.init();
  });

  // Keep profile-owned singleton state aligned with the active session. A
  // profile change clears the old custom-theme registry immediately, and the
  // store generation-guards the async replacement so a late response from the
  // previous profile cannot publish into the new one. Closing global overlays
  // on sign-out/session loss also keeps stale library commands and focus traps
  // off the login screen.
  $effect(() => {
    const profile = session.profile;
    if (profile === null) ui.closeOverlays();
    void customThemes.activate(profile).then(() => {
      // Whichever profile-owned request finishes last (settings or custom
      // themes) gets a chance to resolve the saved id with the complete
      // registry. Keep this tied to activation rather than a global theme
      // effect: Library/Read still own normal theme changes.
      if (
        profile !== null &&
        session.profile === profile &&
        customThemes.loaded
      ) {
        applyTheme(settings.value.theme);
      }
    });
  });

  // Global shortcuts. Only active once signed in; ignored while typing so the
  // palette doesn't hijack normal text entry.
  function onWindowKey(e: KeyboardEvent): void {
    if (!session.authenticated) return;
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
      e.preventDefault();
      ui.togglePalette();
      return;
    }
    const tag = (document.activeElement as HTMLElement | null)?.tagName ?? "";
    const typing = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
    if (e.key === "?" && !typing && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      ui.openShortcuts();
    }
  }
</script>

<svelte:window onkeydown={onWindowKey} />

<OfflineBanner />

<main>
  {#if !session.ready}
    <div class="boot" role="status" aria-busy="true">
      <span class="sr-only">Loading…</span>
    </div>
  {:else if !session.authenticated}
    <Login />
  {:else if router.route.path === "/read/:id"}
    {#key router.route.params.id}
      <Read bookId={router.route.params.id} />
    {/key}
  {:else}
    <Library />
  {/if}
</main>

<CommandPalette />
<ShortcutsHelp />
<Toaster />

<style>
  /* Shift all routed content below the fixed offline banner when it's showing.
     The variable is 0px otherwise, so this is a no-op while online. */
  main {
    padding-top: var(--offline-banner-h, 0px);
  }
  .boot {
    min-height: calc(100vh - var(--offline-banner-h, 0px));
  }
  /* Visually hidden but announced by AT during boot (no global utility exists). */
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
