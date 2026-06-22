<script lang="ts">
  import { toast } from "~/lib/toast.svelte";
</script>

<div class="container" role="status" aria-live="polite" aria-atomic="false">
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
  .toast {
    max-width: min(420px, calc(100vw - 32px));
    padding: 0.5rem 1rem;
    background: color-mix(in srgb, var(--fg) 88%, var(--bg));
    color: var(--bg);
    border-radius: var(--radius);
    font-size: var(--text-sm);
    text-align: center;
    box-shadow: var(--shadow-toast);
    pointer-events: auto;
    animation: toast-in var(--dur) var(--ease-out) forwards;
  }
  .toast.exiting {
    animation: toast-out var(--dur-fast) var(--ease-in) forwards;
  }
  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(10px) scale(0.94);
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
      transform: translateY(-6px) scale(0.96);
    }
  }
</style>
