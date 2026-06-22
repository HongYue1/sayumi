<script lang="ts">
  import { settings } from "~/lib/settings.svelte";
  import { THEMES } from "~/lib/themes";
  import { applyTheme, getTheme } from "~/lib/theme";
  import Icon from "~/lib/Icon.svelte";
  import { Check, ChevronDown } from "@lucide/svelte";

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let menuEl = $state<HTMLElement | null>(null);

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
  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the sticky
  // masthead's backdrop-filter establishes a containing block, which clips a
  // position:fixed scrim to the masthead box (so clicks on the shelf below would
  // never reach it). A window listener is container-proof, matching ProfileMenu.
  function onOutside(e: PointerEvent): void {
    const t = e.target as Node;
    if (menuEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }
  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End" &&
      e.key !== "Tab"
    ) {
      return;
    }
    // Roving focus across all swatches (light + dark) so the menu role's
    // keyboard model works, matching the flair menu in BookCard.
    const menu = e.currentTarget as HTMLElement;
    const items = Array.from(menu.querySelectorAll<HTMLButtonElement>(".pick"));
    if (items.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const cur = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    switch (e.key) {
      case "Home":
        next = 0;
        break;
      case "End":
        next = items.length - 1;
        break;
      case "Tab":
        // Contain focus: Tab wraps forward, Shift+Tab backward, so keyboard
        // focus can't escape into the page behind the open popover.
        next = e.shiftKey
          ? cur < 0
            ? items.length - 1
            : (cur - 1 + items.length) % items.length
          : cur < 0
            ? 0
            : (cur + 1) % items.length;
        break;
      case "ArrowDown":
        next = cur < 0 ? 0 : (cur + 1) % items.length;
        break;
      default:
        next =
          cur < 0 ? items.length - 1 : (cur - 1 + items.length) % items.length;
    }
    items[next].focus();
  }
</script>

<svelte:window onpointerdown={open ? onOutside : undefined} />

<div class="theme-dd">
  <button
    bind:this={trigger}
    class="trigger"
    class:open
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={`Change theme (current: ${current.label})`}
    onclick={toggle}
  >
    <span
      class="swatch"
      style:background={current.bg}
      style:color={current.fg}
      aria-hidden="true"
    >
      <span class="aa">Aa</span>
      <span class="dot" style:background={current.accent}></span>
    </span>
    <Icon icon={ChevronDown} size={14} class="caret" />
  </button>

  {#if open}
    <div
      bind:this={menuEl}
      class="menu"
      role="menu"
      tabindex="-1"
      aria-label="Theme"
      onkeydown={onKeydown}
    >
      <p class="group eyebrow" id="theme-grp-light">Light</p>
      <div class="swatches" role="group" aria-labelledby="theme-grp-light">
        {#each lightThemes as t (t.id)}
          {@const active = settings.value.theme === t.id}
          <button
            class="pick"
            class:active
            role="menuitemradio"
            aria-checked={active}
            tabindex={active ? 0 : -1}
            title={t.label}
            aria-label={t.label}
            onclick={() => choose(t.id)}
            {@attach (el) => {
              // Focus the currently-selected theme on open (menuitemradio model).
              if (active) (el as HTMLButtonElement).focus();
            }}
          >
            <span class="preview" style:background={t.bg} style:color={t.fg}>
              <span class="aa">Aa</span>
              <span class="dot" style:background={t.accent}></span>
              {#if active}
                <span class="check" aria-hidden="true"
                  ><Icon icon={Check} size={11} /></span
                >
              {/if}
            </span>
            <span class="name">{t.label}</span>
          </button>
        {/each}
      </div>
      <p class="group eyebrow" id="theme-grp-dark">Dark</p>
      <div class="swatches" role="group" aria-labelledby="theme-grp-dark">
        {#each darkThemes as t (t.id)}
          {@const active = settings.value.theme === t.id}
          <button
            class="pick"
            class:active
            role="menuitemradio"
            aria-checked={active}
            tabindex={active ? 0 : -1}
            title={t.label}
            aria-label={t.label}
            onclick={() => choose(t.id)}
            {@attach (el) => {
              if (active) (el as HTMLButtonElement).focus();
            }}
          >
            <span class="preview" style:background={t.bg} style:color={t.fg}>
              <span class="aa">Aa</span>
              <span class="dot" style:background={t.accent}></span>
              {#if active}
                <span class="check" aria-hidden="true"
                  ><Icon icon={Check} size={11} /></span
                >
              {/if}
            </span>
            <span class="name">{t.label}</span>
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
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    border: 1px solid var(--hairline-strong);
    background: var(--bg);
    border-radius: var(--radius);
    padding: 0.25rem 0.4rem;
    cursor: pointer;
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
  .trigger :global(.caret) {
    color: var(--muted);
    transition: transform var(--dur-fast) var(--ease-out);
  }
  .trigger.open :global(.caret) {
    transform: rotate(180deg);
  }
  .swatch {
    position: relative;
    display: grid;
    place-items: center;
    width: 1.9rem;
    height: 1.5rem;
    border-radius: var(--radius-sm);
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

  .menu {
    position: absolute;
    top: calc(100% + 0.4rem);
    right: 0;
    z-index: 21;
    width: 18.5rem;
    max-height: min(70vh, 28rem);
    overflow-x: hidden;
    overflow-y: auto;
    padding: var(--sp-3);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    box-shadow: var(--shadow-pop);
    transform-origin: top right;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  .group {
    margin: 0.2rem 0 0.35rem;
  }
  .group:not(:first-child) {
    margin-top: 0.7rem;
  }
  .swatches {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.4rem;
  }
  .pick {
    display: flex;
    flex-direction: column;
    min-width: 0;
    gap: 0.3rem;
    padding: 0.35rem;
    border: 1px solid var(--hairline);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .pick:hover {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 7%, var(--bg));
  }
  .pick:active {
    transform: scale(0.97);
  }
  .pick.active {
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
  }
  .preview {
    position: relative;
    display: grid;
    place-items: center;
    height: 2.2rem;
    border-radius: var(--radius-sm);
    border: 1px solid color-mix(in srgb, var(--fg) 12%, transparent);
  }
  .preview .aa {
    font-family: var(--font-display);
    font-size: 0.85rem;
    font-weight: 600;
  }
  .preview .dot {
    position: absolute;
    right: 4px;
    bottom: 4px;
    width: 0.4rem;
    height: 0.4rem;
    border-radius: 50%;
  }
  .preview .check {
    position: absolute;
    top: 3px;
    left: 3px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1rem;
    height: 1rem;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-fg);
  }
  .name {
    overflow: hidden;
    font-size: var(--text-xs);
    text-align: center;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted);
  }
  .pick.active .name {
    color: var(--fg);
    font-weight: 500;
  }
</style>
