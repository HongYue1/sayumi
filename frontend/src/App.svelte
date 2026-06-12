<script lang="ts">
  import { onMount } from "svelte";
  import { session } from "~/lib/session.svelte";
  import { router } from "~/lib/router.svelte";
  import { ui } from "~/lib/ui.svelte";
  import { applyTheme } from "~/lib/theme";
  import Login from "~/routes/Login.svelte";
  import Library from "~/routes/Library.svelte";
  import Read from "~/routes/Read.svelte";
  import Toaster from "~/components/Toaster.svelte";
  import OfflineBanner from "~/components/OfflineBanner.svelte";
  import CommandPalette from "~/components/CommandPalette.svelte";
  import ShortcutsHelp from "~/components/ShortcutsHelp.svelte";

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

{#if !session.ready}
  <div class="boot"></div>
{:else if !session.authenticated}
  <Login />
{:else if router.route.path === "/read/:id"}
  {#key router.route.params.id}
    <Read bookId={router.route.params.id} />
  {/key}
{:else}
  <Library />
{/if}

<CommandPalette />
<ShortcutsHelp />
<Toaster />

<style>
  .boot {
    min-height: 100vh;
  }
</style>
