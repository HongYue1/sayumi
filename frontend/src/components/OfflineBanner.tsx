// Offline banner: a fixed overlay shown when the sayumi server becomes
// unreachable. Ported from OfflineBanner.svelte.
//
// Solid 2.0 notes:
//   - onMount -> onSettled; its returned cleanup replaces the Svelte
//     teardown, including the imperative listeners that stand in for
//     <svelte:window>/<svelte:document> bindings.
//   - The $effect that toggled .offline-banner-open on <html> becomes a
//     compute/apply createEffect pair: only the compute phase tracks, and the
//     apply phase's return value is the cleanup, matching $effect semantics.
//   - `offlinePlain` mirrors the signal for the scheduler: a signal read
//     immediately after a write still returns the pre-write value (batched),
//     so scheduleNext() would compute the wrong cadence off the accessor.
import { createEffect, createSignal, onSettled, Show } from "solid-js";
import { checkHealth } from "~/api/client";
import Icon from "~/lib/Icon";
import { WifiOff } from "~/lib/icons";
import { isReachable, subscribeReachability } from "~/lib/reachability";

// While reachable we still poll periodically so a server quit *while the tab
// sits idle* (parked on a cached chapter, or an already-loaded library) is
// noticed within one interval instead of only when the next request happens
// to fail. While unreachable we poll a little slower, just to catch recovery.
// Polling pauses entirely while the tab is hidden.
const HEARTBEAT_MS = 10_000;
const RECOVERY_POLL_MS = 15_000;

function isHidden(): boolean {
  return typeof document !== "undefined" && document.hidden;
}

export default function OfflineBanner() {
  const [offline, setOffline] = createSignal(false);
  let offlinePlain = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let checkInFlight = false;
  let mounted = true;

  // The banner is a fixed overlay, so reserve its height on <html> while it's
  // showing; the global --offline-banner-h variable lets main/.reader/.library
  // shift down instead of being painted over (e.g. the reader's top chrome).
  createEffect(
    () => offline(),
    (isOffline) => {
      const root = document.documentElement;
      root.classList.toggle("offline-banner-open", isOffline);
      return () => root.classList.remove("offline-banner-open");
    },
  );

  function clearTimer(): void {
    if (timer) {
      clearTimeout(timer);
      timer = undefined;
    }
  }

  function scheduleNext(): void {
    clearTimer();
    if (!mounted || isHidden()) return;
    const delay = offlinePlain ? RECOVERY_POLL_MS : HEARTBEAT_MS;
    timer = setTimeout(() => {
      timer = undefined;
      void check();
    }, delay);
  }

  function setOfflineState(value: boolean): void {
    offlinePlain = value;
    setOffline(value);
    scheduleNext();
  }

  async function check(): Promise<void> {
    if (checkInFlight) return;
    checkInFlight = true;
    try {
      const healthy = await checkHealth();
      if (!mounted) return;
      offlinePlain = !healthy;
      setOffline(!healthy);
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
    // The OS network interface is down, but that alone does NOT mean the
    // sayumi server is unreachable: on a localhost deployment 127.0.0.1 still
    // answers with WiFi off, so trusting the `offline` event would flash a
    // false banner. Defer to a real /health probe instead (checkHealth is
    // 5s-bounded and fails fast on a genuinely dead LAN), keeping the banner
    // driven by actual request reachability -- this module's source of truth --
    // in both the localhost and LAN deployments.
    void check();
  }
  function handleVisibility(): void {
    // Don't burn requests on a backgrounded tab; check immediately on return
    // so the banner is correct the moment the reader/library is looked at
    // again.
    if (isHidden()) clearTimer();
    else void check();
  }

  onSettled(() => {
    // Drive the banner off real request reachability, not navigator.onLine
    // (which stays true when the local server is quit). Any failed or
    // successful API call flips this signal, and the heartbeat above catches a
    // server that dies while the tab is idle.
    setOfflineState(!isReachable());
    const unsubscribe = subscribeReachability((reachable) => {
      if (!mounted) return;
      setOfflineState(!reachable);
    });
    void check();

    window.addEventListener("focus", handleOnline);
    window.addEventListener("online", handleOnline);
    window.addEventListener("offline", handleOffline);
    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      mounted = false;
      clearTimer();
      unsubscribe();
      window.removeEventListener("focus", handleOnline);
      window.removeEventListener("online", handleOnline);
      window.removeEventListener("offline", handleOffline);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  });

  return (
    <Show when={offline()}>
      <div class="offline-banner" role="alert">
        <Icon icon={WifiOff} size={15} />
        Server unreachable
        <button
          type="button"
          class="offline-banner-retry"
          onClick={() => void check()}
        >
          Retry
        </button>
      </div>
    </Show>
  );
}
