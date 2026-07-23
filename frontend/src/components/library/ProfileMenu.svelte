<script lang="ts">
  import { session } from "~/lib/session.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { ChevronDown, Copy, Trash2, LogOut, User } from "@lucide/svelte";

  interface Props {
    onclone: () => void;
    ondelete: () => void;
  }
  let { onclone, ondelete }: Props = $props();

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let menuEl = $state<HTMLElement | null>(null);

  function toggle(): void {
    open = !open;
  }
  function close(restoreFocus = true): void {
    if (!open) return;
    open = false;
    if (restoreFocus) trigger?.focus();
  }

  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the sticky
  // masthead's backdrop-filter establishes a containing block, which clips a
  // position:fixed scrim to the masthead box (so clicks on the shelf below would
  // never reach it). A window listener is container-proof.
  function onWindowPointerDown(e: PointerEvent): void {
    const t = e.target as Node;
    if (menuEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }

  function onMenuKeydown(e: KeyboardEvent): void {
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
    // Roving focus across the menu items, matching ThemeDropdown / BookCard.
    const menu = e.currentTarget as HTMLElement;
    const items = Array.from(menu.querySelectorAll<HTMLButtonElement>(".item"));
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
        // Contain focus so it can't escape into the page behind the popover.
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

  function pick(action: () => void): void {
    // Restore focus to the trigger BEFORE running the action. Clone/Delete open
    // a focusTrap'd dialog that snapshots document.activeElement on mount and
    // restores it on close; if we closed with restoreFocus=false the menu item
    // would be removed, activeElement would fall to <body>, and the dialog would
    // hand focus back to <body> instead of this trigger. Focusing the trigger
    // here makes it the snapshot, so focus round-trips correctly.
    close(true);
    action();
  }

  function signOut(): void {
    // logout clears local session state in its finally block even if the request
    // fails; consume that rejection after cleanup so it never becomes unhandled.
    void session.logout().catch(() => undefined);
  }
</script>

<svelte:window onpointerdown={open ? onWindowPointerDown : undefined} />

<div class="profile-menu">
  <button
    bind:this={trigger}
    class="trigger"
    class:open
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={`Profile: ${session.profile ?? ""}`}
    onclick={toggle}
  >
    <Icon icon={User} size={15} class="who-icon" />
    <span class="who" title={session.profile ?? ""}>{session.profile}</span>
    <Icon icon={ChevronDown} size={14} class="caret" />
  </button>

  {#if open}
    <div
      bind:this={menuEl}
      class="menu"
      role="menu"
      tabindex="-1"
      aria-label="Profile"
      onkeydown={onMenuKeydown}
    >
      <button
        class="item"
        role="menuitem"
        tabindex="0"
        onclick={() => pick(onclone)}
        {@attach (el) => (el as HTMLButtonElement).focus()}
      >
        <Icon icon={Copy} size={16} />
        Clone profile…
      </button>
      <button
        class="item danger"
        role="menuitem"
        tabindex="-1"
        onclick={() => pick(ondelete)}
      >
        <Icon icon={Trash2} size={16} />
        Delete profile…
      </button>
      <div class="sep" role="separator"></div>
      <button
        class="item"
        role="menuitem"
        tabindex="-1"
        onclick={() => pick(signOut)}
      >
        <Icon icon={LogOut} size={16} />
        Sign out
      </button>
    </div>
  {/if}
</div>

<style>
  .profile-menu {
    position: relative;
    display: inline-flex;
    min-width: 0;
  }
  .trigger {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
    min-width: 0;
    max-width: min(14rem, 26vw);
    padding: 0.3rem 0.5rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    cursor: pointer;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .trigger:hover {
    border-color: var(--accent);
    background: var(--surface-hover);
  }
  .trigger:active {
    transform: scale(0.97);
  }
  .trigger :global(.who-icon) {
    color: var(--muted);
    flex-shrink: 0;
  }
  .who {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .trigger :global(.caret) {
    color: var(--muted);
    flex-shrink: 0;
    transition: transform var(--dur-fast) var(--ease-out);
  }
  .trigger.open :global(.caret) {
    transform: rotate(180deg);
  }

  .menu {
    position: absolute;
    top: calc(100% + 0.4rem);
    right: 0;
    z-index: 21;
    min-width: 12rem;
    padding: var(--sp-1);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    transform-origin: top right;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  .item {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    width: 100%;
    padding: 0.5rem 0.6rem;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    text-align: left;
    white-space: nowrap;
    cursor: pointer;
    transition: background var(--dur-fast) var(--ease-out);
  }
  .item:hover,
  .item:focus-visible {
    background: var(--surface-hover);
    outline: none;
  }
  .item.danger {
    color: var(--fg);
  }
  .item.danger:hover,
  .item.danger:focus-visible {
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
  }
  .sep {
    height: 1px;
    margin: var(--sp-1) 0;
    background: var(--hairline);
  }

  @media (max-width: 360px) {
    .trigger :global(.who-icon) {
      display: none;
    }
  }
</style>
