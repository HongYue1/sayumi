<script lang="ts">
  import { onMount } from "svelte";
  import { checkHealth } from "~/api/client";
  import { subscribeReachability, isReachable } from "~/lib/reachability";
  import Icon from "~/lib/Icon.svelte";
  import { WifiOff } from "@lucide/svelte";

  // While reachable we still poll periodically so a server quit *while the tab
  // sits idle* (parked on a cached chapter, or an already-loaded library) is
  // noticed within one interval instead of only when the next request happens
  // to fail. While unreachable we poll a little slower, just to catch recovery.
  // Polling pauses entirely while the tab is hidden.
  const HEARTBEAT_MS = 10_000;
  const RECOVERY_POLL_MS = 15_000;

  let offline = $state(false);

  let timer: ReturnType<typeof setTimeout> | undefined;
  let checkInFlight = false;
  let mounted = true;

  // The banner is a fixed overlay, so reserve its height on <html> while it's
  // showing; the global --offline-banner-h variable lets main/.reader/.library
  // shift down instead of being painted over (e.g. the reader's top chrome).
  $effect(() => {
    const root = document.documentElement;
    root.classList.toggle("offline-banner-open", offline);
    return () => root.classList.remove("offline-banner-open");
  });

  function isHidden(): boolean {
    return typeof document !== "undefined" && document.hidden;
  }

  function clearTimer(): void {
    if (timer) {
      clearTimeout(timer);
      timer = undefined;
    }
  }

  function scheduleNext(): void {
    clearTimer();
    if (!mounted || isHidden()) return;
    const delay = offline ? RECOVERY_POLL_MS : HEARTBEAT_MS;
    timer = setTimeout(() => {
      timer = undefined;
      void check();
    }, delay);
  }

  function setOffline(value: boolean): void {
    offline = value;
    scheduleNext();
  }

  async function check(): Promise<void> {
    if (checkInFlight) return;
    checkInFlight = true;
    try {
      const healthy = await checkHealth();
      if (!mounted) return;
      offline = !healthy;
    } finally {
      checkInFlight = false;
      // Keep the heartbeat loop alive regardless of outcome, at the cadence
      // that matches the current online/offline state.
      scheduleNext();
    }
  }

  function handleOnline(): void {
    void check();
  }
  function handleOffline(): void {
    // The OS network interface is down, but that alone does NOT mean the sayumi
    // server is unreachable: on a localhost deployment 127.0.0.1 still answers
    // with WiFi off, so trusting the `offline` event would flash a false banner.
    // Defer to a real /health probe instead (checkHealth is 5s-bounded and
    // fails fast on a genuinely dead LAN), keeping the banner driven by actual
    // request reachability — this module's source of truth — in both the
    // localhost and LAN deployments.
    void check();
  }
  function handleVisibility(): void {
    // Don't burn requests on a backgrounded tab; check immediately on return so
    // the banner is correct the moment the reader/library is looked at again.
    if (isHidden()) clearTimer();
    else void check();
  }

  onMount(() => {
    // Drive the banner off real request reachability, not navigator.onLine
    // (which stays true when the local server is quit). Any failed or
    // successful API call flips this signal, and the heartbeat above catches a
    // server that dies while the tab is idle.
    setOffline(!isReachable());
    const unsubscribe = subscribeReachability((reachable) => {
      if (!mounted) return;
      setOffline(!reachable);
    });
    void check();
    return () => {
      mounted = false;
      clearTimer();
      unsubscribe();
    };
  });
</script>

<svelte:window
  onfocus={handleOnline}
  ononline={handleOnline}
  onoffline={handleOffline}
/>
<svelte:document onvisibilitychange={handleVisibility} />

{#if offline}
  <div class="banner" role="alert">
    <Icon icon={WifiOff} size={15} />
    Server unreachable
    <button type="button" class="retry" onclick={() => void check()}
      >Retry</button
    >
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
    height: 2.5rem;
    padding: 0 1rem;
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
</style>
