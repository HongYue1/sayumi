<script lang="ts">
  import { toast } from "~/lib/toast.svelte";
</script>

<div
  class="container"
  role="log"
  aria-label="Notifications"
  aria-live="polite"
  aria-relevant="additions"
>
  {#each toast.items as item (item.id)}
    <div class="toast" class:exiting={item.exiting}>{item.message}</div>
  {/each}
</div>

<style>
  .container {
    position: fixed;
    bottom: 4rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 200;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-2);
    pointer-events: none;
  }
  /* Inverted ink slip: the theme's own ink as paper, its paper as ink. */
  .toast {
    max-width: min(420px, calc(100vw - 32px));
    padding: 0.55rem 1.1rem;
    background: color-mix(in srgb, var(--fg) 92%, var(--bg));
    color: var(--bg);
    border-radius: 999px;
    font-size: var(--text-sm);
    font-weight: 560;
    letter-spacing: 0.01em;
    text-align: center;
    overflow-wrap: anywhere;
    box-shadow: var(--shadow-2);
    animation: toast-in var(--dur-slow) var(--ease-spring) forwards;
  }
  .toast.exiting {
    animation: toast-out var(--dur-fast) var(--ease-in) forwards;
  }
  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(12px) scale(0.92);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }
  @keyframes toast-out {
    from {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
    to {
      opacity: 0;
      transform: translateY(-8px) scale(0.96);
    }
  }
</style>
