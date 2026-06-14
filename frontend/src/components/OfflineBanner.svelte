<script lang="ts">
  import { onMount } from "svelte";
  import { checkHealth } from "~/api/client";
  import Icon from "~/lib/Icon.svelte";
  import { WifiOff } from "@lucide/svelte";

  const RECOVERY_POLL_MS = 15_000;

  let offline = $state(false);

  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let checkInFlight = false;
  let mounted = true;

  function clearPoll(): void {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = undefined;
    }
  }

  function schedulePoll(): void {
    if (pollTimer || !offline) return;
    pollTimer = setTimeout(() => {
      pollTimer = undefined;
      if (offline) void check();
    }, RECOVERY_POLL_MS);
  }

  async function check(): Promise<void> {
    if (checkInFlight) return;
    checkInFlight = true;
    try {
      const healthy = await checkHealth();
      if (!mounted) return;
      offline = !healthy;
      if (healthy) clearPoll();
      else schedulePoll();
    } finally {
      checkInFlight = false;
    }
  }

  function handleOnline(): void {
    void check();
  }
  function handleOffline(): void {
    offline = true;
    schedulePoll();
  }

  onMount(() => {
    void check();
    return () => {
      mounted = false;
      clearPoll();
    };
  });
</script>

<svelte:window ononline={handleOnline} onoffline={handleOffline} />

{#if offline}
  <div class="banner" role="alert">
    <Icon icon={WifiOff} size={15} />
    Server unreachable
    <button type="button" class="retry" onclick={() => void check()}>Retry</button>
  </div>
{/if}

<style>
  .banner {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    z-index: 250;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    padding: 0.5rem 1rem;
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
    font-size: var(--text-sm);
    animation: slide-down var(--dur) var(--ease-out) forwards;
  }
  @keyframes slide-down {
    from {
      transform: translateY(-100%);
    }
    to {
      transform: translateY(0);
    }
  }
  .retry {
    margin-left: 0.5rem;
    padding: 2px 0.5rem;
    font: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    color: #fff;
    background: rgba(255, 255, 255, 0.2);
    border: none;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .retry:hover {
    background: rgba(255, 255, 255, 0.32);
  }
  .retry:active {
    transform: scale(0.96);
  }
  @media (prefers-reduced-motion: reduce) {
    .banner {
      animation: none;
    }
  }
</style>
