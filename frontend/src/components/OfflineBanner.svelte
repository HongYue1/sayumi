<script lang="ts">
  import { onMount } from "svelte";
  import { checkHealth } from "~/api/client";

  const RECOVERY_POLL_MS = 15_000;

  let offline = $state(false);

  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let checkInFlight = false;

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
    return clearPoll;
  });
</script>

<svelte:window ononline={handleOnline} onoffline={handleOffline} />

{#if offline}
  <div class="banner" role="alert">
    <span class="dot" aria-hidden="true"></span>
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
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    background: #b3261e;
    color: #fff;
    font-size: 0.8rem;
    animation: slide-down 0.2s ease forwards;
  }
  @keyframes slide-down {
    from {
      transform: translateY(-100%);
    }
    to {
      transform: translateY(0);
    }
  }
  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.5);
    flex-shrink: 0;
  }
  .retry {
    margin-left: 0.5rem;
    padding: 2px 0.5rem;
    font: inherit;
    font-size: 0.72rem;
    font-weight: 600;
    color: #fff;
    background: rgba(255, 255, 255, 0.2);
    border: none;
    border-radius: 0.35rem;
    cursor: pointer;
    transition: background 0.12s ease;
  }
  .retry:hover {
    background: rgba(255, 255, 255, 0.3);
  }
  @media (prefers-reduced-motion: reduce) {
    .banner {
      animation: none;
    }
  }
</style>
