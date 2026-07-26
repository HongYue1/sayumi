<script lang="ts">
  import { settings } from "~/lib/settings.svelte";
  import { THEMES } from "~/lib/themes";
  import { applyTheme, getTheme } from "~/lib/theme";
  import { customThemes } from "~/lib/customThemes.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { Check, ChevronDown } from "@lucide/svelte";

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let menuEl = $state<HTMLElement | null>(null);

  // Read through the reactive profile list before the global resolver so a
  // custom-theme load/retry refreshes the trigger even when the saved theme id
  // itself did not change.
  const current = $derived(
    customThemes.get(settings.value.theme) ?? getTheme(settings.value.theme),
  );
  const lightThemes = THEMES.filter((t) => t.group === "light");
  const darkThemes = THEMES.filter((t) => t.group === "dark");
  // When the active theme is a CUSTOM one, no built-in swatch is active — and
  // with every item at tabindex -1 the menu would be a keyboard dead-end (no
  // initial focus, arrows/Home/End find activeElement outside the menu). Fall
  // back to treating the first light swatch as the roving-focus entry point.
  const hasBuiltInActive = $derived(
    THEMES.some((t) => t.id === settings.value.theme),
  );

  function toggle(): void {
    open = !open;
    // App boot normally loads the profile registry. If that non-fatal request
    // failed, opening a theme selector is an explicit, bounded retry point.
    if (open && !customThemes.loaded) {
      void customThemes.load().then(() => {
        if (customThemes.loaded) applyTheme(settings.value.theme);
      });
    }
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
      class="menu paper"
      role="menu"
      tabindex="-1"
      aria-label="Theme"
      onkeydown={onKeydown}
    >
      <p class="group eyebrow" id="theme-grp-light">Light</p>
      <div class="swatches" role="group" aria-labelledby="theme-grp-light">
        {#each lightThemes as t, i (t.id)}
          {@const active = settings.value.theme === t.id}
          {@const focusEntry = active || (i === 0 && !hasBuiltInActive)}
          <button
            class="pick"
            class:active
            role="menuitemradio"
            aria-checked={active}
            tabindex={focusEntry ? 0 : -1}
            title={t.label}
            aria-label={t.label}
            onclick={() => choose(t.id)}
            {@attach (el) => {
              // Focus the currently-selected theme on open (menuitemradio
              // model), or the first swatch when a custom theme is active.
              if (focusEntry) (el as HTMLButtonElement).focus();
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
    border: none;
    background: transparent;
    border-radius: var(--radius);
    padding: 0.3rem 0.45rem;
    cursor: pointer;
    transition:
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .trigger:hover,
  .trigger.open {
    background: var(--surface);
  }
  .trigger:active {
    transform: scale(0.96);
  }
  .trigger :global(.caret) {
    color: var(--muted);
    transition: transform var(--dur) var(--ease-spring);
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
    box-shadow: inset 0 0 0 1px
      light-dark(rgb(0 0 0 / 0.1), rgb(255 255 255 / 0.14));
  }
  .swatch .aa {
    font-family: var(--font-display);
    font-size: 0.8rem;
    font-weight: 600;
  }
  .swatch .dot {
    position: absolute;
    right: 3px;
    bottom: 3px;
    width: 0.34rem;
    height: 0.34rem;
    border-radius: 50%;
  }

  .menu {
    position: absolute;
    top: calc(100% + 0.5rem);
    right: 0;
    z-index: 21;
    width: 19rem;
    max-height: min(70vh, 28rem);
    overflow-x: hidden;
    overflow-y: auto;
    padding: var(--sp-3);
    transform-origin: top right;
    animation: app-menu-pop-in var(--dur) var(--ease-out) both;
  }
  .group {
    margin: 0.2rem 0 0.45rem;
  }
  .group:not(:first-child) {
    margin-top: 0.9rem;
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
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .pick:hover {
    background: var(--surface);
    border-color: var(--hairline);
  }
  .pick:active {
    transform: scale(0.97);
  }
  .pick.active {
    border-color: var(--accent);
    background: var(--accent-soft);
  }
  .preview {
    position: relative;
    display: grid;
    place-items: center;
    height: 2.3rem;
    border-radius: var(--radius-sm);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--fg) 14%, transparent);
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
    font-weight: 540;
    text-align: center;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted);
  }
  .pick.active .name {
    color: var(--fg);
    font-weight: 640;
  }
</style>
