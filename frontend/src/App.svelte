<script lang="ts">
  import { onMount } from "svelte";
  import { session } from "~/lib/session.svelte";
  import { router } from "~/lib/router.svelte";
  import { ui } from "~/lib/ui.svelte";
  import { applyTheme } from "~/lib/theme";
  import Login from "~/routes/Login.svelte";
  import Library from "~/routes/Library.svelte";
  import Toaster from "~/components/Toaster.svelte";
  import OfflineBanner from "~/components/OfflineBanner.svelte";

  // Lazily-loaded chunks kept out of the initial bundle. Each loader memoises
  // its import() promise (??=) so re-renders reuse the same module-cached
  // promise instead of re-triggering the load and re-flashing the await block.
  // - Read pulls in the whole reader engine (ChapterFrame -> buildFrameHtml ->
  //   the inlined frame.ts script), only needed once a book is open.
  // - The command palette and shortcuts sheet render only on demand (Ctrl+K / ?),
  //   so their code is fetched the first time they are opened.
  let readModule: Promise<typeof import("~/routes/Read.svelte")> | undefined;
  const loadRead = () => (readModule ??= import("~/routes/Read.svelte"));

  let paletteModule:
    | Promise<typeof import("~/components/CommandPalette.svelte")>
    | undefined;
  const loadPalette = () =>
    (paletteModule ??= import("~/components/CommandPalette.svelte"));

  let shortcutsModule:
    | Promise<typeof import("~/components/ShortcutsHelp.svelte")>
    | undefined;
  const loadShortcuts = () =>
    (shortcutsModule ??= import("~/components/ShortcutsHelp.svelte"));

  onMount(() => {
    applyTheme("light");
    session.init();
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
      ui.shortcuts = true;
    }
  }
</script>

<svelte:window onkeydown={onWindowKey} />

<OfflineBanner />

<main>
{#if !session.ready}
  <div class="boot"></div>
{:else if !session.authenticated}
  <Login />
{:else if router.route.path === "/read/:id"}
  {#await loadRead()}
    <div class="boot"></div>
  {:then Read}
    {#key router.route.params.id}
      <Read.default bookId={router.route.params.id} />
    {/key}
  {/await}
{:else}
  <Library />
{/if}
</main>

{#if ui.palette}
  {#await loadPalette() then Palette}
    <Palette.default />
  {/await}
{/if}
{#if ui.shortcuts}
  {#await loadShortcuts() then Shortcuts}
    <Shortcuts.default />
  {/await}
{/if}
<Toaster />

<style>
  .boot {
    min-height: 100vh;
  }
</style>
