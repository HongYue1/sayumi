<script lang="ts">
  import { settings } from "~/lib/settings.svelte";
  import { THEMES } from "~/lib/themes";
  import { applyTheme, getTheme } from "~/lib/theme";

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);

  const current = $derived(getTheme(settings.value.theme));
  const lightThemes = THEMES.filter((t) => t.group === "light");
  const darkThemes = THEMES.filter((t) => t.group === "dark");

  function toggle(): void {
    open = !open;
  }
  function close(restoreFocus = true): void {
    open = false;
    if (restoreFocus) trigger?.focus();
  }
  function choose(id: string): void {
    settings.update({ theme: id });
    applyTheme(id);
    close();
  }
  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    }
  }
</script>

<div class="theme-dd">
  <button
    bind:this={trigger}
    class="trigger"
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label="Change theme"
    onclick={toggle}
  >
    <span class="swatch" style:background={current.bg} style:color={current.fg} aria-hidden="true">
      <span class="aa">Aa</span>
      <span class="dot" style:background={current.accent}></span>
    </span>
  </button>

  {#if open}
    <button class="scrim" aria-label="Close theme menu" onclick={() => close(false)}></button>
    <div class="menu" role="menu" tabindex="-1" aria-label="Theme" onkeydown={onKeydown}>
      <p class="group eyebrow">Light</p>
      <div class="swatches">
        {#each lightThemes as t, i (t.id)}
          {@const active = settings.value.theme === t.id}
          <button
            class="swatch pick"
            class:active
            role="menuitemradio"
            aria-checked={active}
            title={t.label}
            aria-label={t.label}
            style:background={t.bg}
            style:color={t.fg}
            onclick={() => choose(t.id)}
            {@attach (el) => {
              if (i === 0) (el as HTMLButtonElement).focus();
            }}
          >
            <span class="aa">Aa</span>
            <span class="dot" style:background={t.accent}></span>
          </button>
        {/each}
      </div>
      <p class="group eyebrow">Dark</p>
      <div class="swatches">
        {#each darkThemes as t (t.id)}
          {@const active = settings.value.theme === t.id}
          <button
            class="swatch pick"
            class:active
            role="menuitemradio"
            aria-checked={active}
            title={t.label}
            aria-label={t.label}
            style:background={t.bg}
            style:color={t.fg}
            onclick={() => choose(t.id)}
          >
            <span class="aa">Aa</span>
            <span class="dot" style:background={t.accent}></span>
          </button>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .theme-dd {
    position: relative;
    display: inline-flex;
  }
  .trigger {
    border: 1px solid var(--hairline-strong);
    background: var(--bg);
    border-radius: var(--radius);
    padding: 0.25rem;
    cursor: pointer;
    line-height: 0;
    transition:
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .trigger:hover {
    border-color: var(--accent);
  }
  .trigger:active {
    transform: scale(0.97);
  }
  .swatch {
    position: relative;
    display: grid;
    place-items: center;
    width: 1.9rem;
    height: 1.5rem;
    border-radius: 0.3rem;
  }
  .swatch .aa {
    font-family: var(--font-display);
    font-size: 0.8rem;
    font-weight: 600;
  }
  .swatch .dot {
    position: absolute;
    right: 2px;
    bottom: 2px;
    width: 0.34rem;
    height: 0.34rem;
    border-radius: 50%;
  }

  .scrim {
    position: fixed;
    inset: 0;
    z-index: 20;
    border: none;
    background: transparent;
    cursor: default;
  }
  .menu {
    position: absolute;
    top: calc(100% + 0.4rem);
    right: 0;
    z-index: 21;
    width: 16rem;
    padding: var(--sp-3);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    box-shadow: 0 8px 22px color-mix(in srgb, var(--fg) 22%, transparent);
    transform-origin: top right;
    animation: theme-menu-in var(--dur-fast) var(--ease-out) both;
  }
  @keyframes theme-menu-in {
    from {
      opacity: 0;
      transform: scale(0.97) translateY(-3px);
    }
    to {
      opacity: 1;
      transform: scale(1) translateY(0);
    }
  }
  .group {
    margin: 0.2rem 0 0.35rem;
  }
  .group:not(:first-child) {
    margin-top: 0.7rem;
  }
  .swatches {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(2.4rem, 1fr));
    gap: var(--sp-2);
  }
  .swatch.pick {
    width: 100%;
    height: 2rem;
    border: 2px solid var(--hairline);
    cursor: pointer;
    transition:
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .swatch.pick:hover {
    border-color: var(--accent);
  }
  .swatch.pick:active {
    transform: scale(0.95);
  }
  .swatch.pick.active {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent);
  }
</style>
