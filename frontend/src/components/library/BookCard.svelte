<script lang="ts">
  import { getCoverUrl, type BookMeta, type FlairDef } from "~/api/client";
  import { findFlair, flairTextColor } from "~/lib/flairs";
  import Icon from "~/lib/Icon.svelte";
  import { Trash2, Tag, Check, Pencil, Settings, Share2 } from "@lucide/svelte";

  interface Props {
    book: BookMeta;
    flairs: FlairDef[];
    index?: number;
    onopen: (id: string) => void;
    onremove: (id: string) => void;
    onedit: (id: string) => void;
    onshare: (id: string) => void;
    onsetflair: (bookId: string, flairId: string | null) => void;
  }

  let {
    book,
    flairs,
    index = 0,
    onopen,
    onremove,
    onedit,
    onshare,
    onsetflair,
  }: Props = $props();
  // One-shot entrance stagger: index is read once on mount by design (cards are
  // keyed by book.id, so each instance keeps its original position), and the
  // delay only drives the mount animation. Capped so large libraries don't
  // accumulate long delays.
  // svelte-ignore state_referenced_locally
  const enterDelay = Math.min(index, 16) * 28;

  let coverFailed = $state(false);
  let openMenu = $state<"flair" | "actions" | null>(null);
  const showCover = $derived(book.hasCover && !coverFailed);
  const pct = $derived(
    Math.round(Math.max(0, Math.min(1, book.progress)) * 100),
  );
  const flair = $derived(findFlair(book.flairId, flairs));
  // Whether the book's current flair is one of the selectable options, so the
  // menu can open with focus on the checked item (menuitemradio model, matching
  // ThemeDropdown) and fall back to the first item only when nothing is set.
  const hasActiveFlair = $derived(flairs.some((f) => f.id === book.flairId));

  let flairBtn = $state<HTMLButtonElement | null>(null);
  let actionsBtn = $state<HTMLButtonElement | null>(null);
  let menuEl = $state<HTMLDivElement | null>(null);
  // The trigger that owns the currently-open popover, so dismiss/Escape can
  // restore focus to the right chip (gear or flair) regardless of which menu
  // is open.
  const activeTrigger = $derived(
    openMenu === "flair"
      ? flairBtn
      : openMenu === "actions"
        ? actionsBtn
        : null,
  );
  // Flip the flair popover inward when a card near the right/bottom viewport
  // edge would otherwise open it off-screen.
  let flipX = $state(false);
  let flipY = $state(false);

  function toggleFlair(e: MouseEvent): void {
    e.stopPropagation();
    openMenu = openMenu === "flair" ? null : "flair";
  }

  function toggleActions(e: MouseEvent): void {
    e.stopPropagation();
    openMenu = openMenu === "actions" ? null : "actions";
  }

  function closeMenu(restoreFocus = true): void {
    // Capture the trigger before clearing openMenu, since activeTrigger derives
    // from it and would otherwise read null.
    const trigger = activeTrigger;
    openMenu = null;
    if (restoreFocus) trigger?.focus();
  }

  function chooseEdit(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu(false);
    onedit(book.id);
  }

  function chooseShare(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu(false);
    onshare(book.id);
  }

  function chooseDelete(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu(false);
    if (confirm(`Remove “${book.title}” from your library?`)) onremove(book.id);
  }

  // Dismiss on outside-click / Escape via window listeners instead of a fixed
  // scrim. `.card` keeps a `transform` after its entrance animation (fill mode
  // `both` leaves translateY(0) applied), so the card is the containing block
  // for `position: fixed` descendants — a "fixed inset:0" scrim was clipped to
  // the card box, so only clicks ON the card closed the menu, and Escape was
  // missed whenever focus sat on the trigger (a sibling of the menu, so its
  // keydown never bubbled to the menu's handler). The capture-phase click also
  // swallows the dismissing click (like the old scrim) so it doesn't fall
  // through and open a book.
  $effect(() => {
    if (!openMenu) return;
    const onClick = (e: MouseEvent): void => {
      const t = e.target as Node | null;
      if (menuEl?.contains(t) || activeTrigger?.contains(t)) return;
      e.preventDefault();
      e.stopPropagation();
      closeMenu(false);
    };
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeMenu();
      }
    };
    window.addEventListener("click", onClick, true);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("click", onClick, true);
      window.removeEventListener("keydown", onKeyDown);
    };
  });

  // Measure the open menu once and flip it inward if it spills past the
  // viewport edge; reset on close so the next open re-measures from the
  // default top-left position rather than inheriting a stale flip. Reads
  // menuOpen/menuEl only (never flipX/flipY), so applying a flip doesn't
  // re-trigger this effect.
  $effect(() => {
    if (!openMenu || !menuEl) {
      flipX = false;
      flipY = false;
      return;
    }
    const r = menuEl.getBoundingClientRect();
    const margin = 8;
    if (r.right > window.innerWidth - margin) flipX = true;
    if (r.bottom > window.innerHeight - margin) flipY = true;
  });

  function pick(e: MouseEvent, id: string): void {
    e.stopPropagation();
    onsetflair(book.id, book.flairId === id ? null : id);
    closeMenu();
  }

  // Escape closes the popover and returns focus to its trigger.
  function onMenuKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMenu();
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
    // Roving focus across the radio items, matching the menu role's keyboard
    // model so arrow keys move between flairs.
    const menu = e.currentTarget as HTMLElement;
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>(".menu-item"),
    );
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
        // focus can't escape into the grid behind the open popover.
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

<div class="card" style:--enter-delay={`${enterDelay}ms`} role="listitem">
  <!-- A real button carries native Enter/Space + focus semantics for "open",
       so the card no longer nests action buttons inside a role="button". -->
  <button
    class="open-overlay"
    aria-label={`Open ${book.title}`}
    onclick={() => onopen(book.id)}
  ></button>
  <div class="cover">
    {#if showCover}
      <img
        src={getCoverUrl(book.id, book.updatedAt)}
        alt=""
        loading={index < 8 ? "eager" : "lazy"}
        fetchpriority={index === 0 ? "high" : undefined}
        decoding="async"
        onerror={() => (coverFailed = true)}
      />
    {:else}
      <div class="placeholder">
        <span>{book.title}</span>
      </div>
    {/if}

    {#if pct > 0}
      <div class="progress" title={`${pct}% read`}>
        <div class="bar" style:width={`${pct}%`}></div>
      </div>
    {/if}

    {#if flair}
      <span
        class="flair-badge"
        style:background={flair.color}
        style:color={flairTextColor()}
      >
        {flair.label}
      </span>
    {/if}
  </div>

  <!-- Corner actions live at card level (NOT inside .cover): the cover's
       hover-lift transform establishes a stacking context, which would trap
       these buttons beneath the z-index:1 open-overlay and swallow their
       clicks + :hover. At card level they stay above the overlay (z-index:3). -->
  <button
    bind:this={actionsBtn}
    class="chip-btn actions-btn"
    title="Actions"
    aria-label="Book actions"
    aria-haspopup="menu"
    aria-expanded={openMenu === "actions"}
    onclick={toggleActions}
  >
    <Icon icon={Settings} size={15} />
  </button>
  <button
    bind:this={flairBtn}
    class="chip-btn flair-btn"
    title="Set flair"
    aria-label="Set flair"
    aria-haspopup="menu"
    aria-expanded={openMenu === "flair"}
    onclick={toggleFlair}
  >
    <Icon icon={Tag} size={15} />
  </button>

  <div class="meta">
    <div class="title" title={book.title}>{book.title}</div>
    {#if book.author}<div class="author" title={book.author}>
        {book.author}
      </div>{/if}
  </div>

  {#if openMenu === "flair"}
    <div
      bind:this={menuEl}
      class="flair-menu"
      class:flip-x={flipX}
      class:flip-y={flipY}
      role="menu"
      tabindex="-1"
      aria-label="Set flair"
      onkeydown={onMenuKeydown}
    >
      <p class="menu-heading eyebrow" aria-hidden="true">Set flair</p>
      {#each flairs as f, i (f.id)}
        {@const isActive = book.flairId === f.id}
        <button
          class="menu-item"
          class:active={isActive}
          role="menuitemradio"
          aria-checked={isActive}
          tabindex={isActive || (i === 0 && !hasActiveFlair) ? 0 : -1}
          {@attach (el) => {
            // Open with focus on the checked flair (menuitemradio model); fall
            // back to the first item only when the book has no flair set.
            if (isActive || (i === 0 && !hasActiveFlair))
              (el as HTMLButtonElement).focus();
          }}
          onclick={(e) => pick(e, f.id)}
        >
          <span class="dot" style:background={f.color} aria-hidden="true"
          ></span>
          <span class="menu-label">{f.label}</span>
          {#if isActive}<span class="check" aria-hidden="true"
              ><Icon icon={Check} size={15} /></span
            >{/if}
        </button>
      {/each}
    </div>
  {/if}

  {#if openMenu === "actions"}
    <div
      bind:this={menuEl}
      class="actions-menu"
      class:flip-x={flipX}
      class:flip-y={flipY}
      role="menu"
      tabindex="-1"
      aria-label="Book actions"
      onkeydown={onMenuKeydown}
    >
      <button
        class="menu-item"
        role="menuitem"
        tabindex="0"
        {@attach (el) => (el as HTMLButtonElement).focus()}
        onclick={chooseEdit}
      >
        <span class="menu-ico" aria-hidden="true"
          ><Icon icon={Pencil} size={15} /></span
        >
        <span class="menu-label">Edit</span>
      </button>
      <button
        class="menu-item"
        role="menuitem"
        tabindex="-1"
        onclick={chooseShare}
      >
        <span class="menu-ico" aria-hidden="true"
          ><Icon icon={Share2} size={15} /></span
        >
        <span class="menu-label">Share</span>
      </button>
      <button
        class="menu-item danger"
        role="menuitem"
        tabindex="-1"
        onclick={chooseDelete}
      >
        <span class="menu-ico" aria-hidden="true"
          ><Icon icon={Trash2} size={15} /></span
        >
        <span class="menu-label">Delete</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .card {
    position: relative;
    display: flex;
    flex-direction: column;
    cursor: pointer;
    /* One unified card: a full-bleed cover on top + a faint --surface panel
       behind the meta below, wrapped by a hairline border + --radius. A tight
       --radius (not --radius-lg) reads more refined, closer to the original.
       The cover keeps its natural width (no inset/mat); the tile shows only
       behind the title/author so the two read as a single card. No
       overflow:hidden here — the flair-menu popover must escape the card box. */
    border: 1px solid var(--hairline);
    border-radius: var(--radius);
    background: var(--surface);
    text-align: left;
    animation: card-in var(--dur-slow) var(--ease-out) both;
    animation-delay: var(--enter-delay, 0ms);
    /* Skip layout + paint for off-screen cards so a large library only renders
       what's near the viewport. contain-intrinsic-size keeps the scrollbar
       stable before a card's first render; the `auto` keyword then remembers
       each card's real measured size. content-visibility implies paint
       containment, which would clip a tall flair menu — the card is
       deliberately not overflow:hidden so that menu can escape — so the
       :has() rule below drops containment for whichever card has it open. */
    content-visibility: auto;
    contain-intrinsic-size: auto 320px;
  }

  /* While a popover (flair or actions) is open, drop containment on that one
     card so the menu can overflow the card box (the menu element exists only
     while open, so this targets exactly the active card). */
  .card:has(.flair-menu),
  .card:has(.actions-menu) {
    content-visibility: visible;
  }

  /* Transparent full-card hit target for "open". Sits above the cover art but
     below the corner action buttons (z-index: 3) and the flair menu/scrim. */
  .open-overlay {
    position: absolute;
    inset: 0;
    z-index: 1;
    padding: 0;
    border: none;
    /* Tokenized to the card radius; this is an invisible full-card hit target. */
    border-radius: var(--radius);
    background: transparent;
    cursor: pointer;
  }

  @keyframes card-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .cover {
    position: relative;
    aspect-ratio: 2 / 3;
    /* Full-bleed cover: rounds only its TOP corners to match the card's tighter
       --radius; its bottom edge meets the meta panel flush so cover + tile read
       as one card. No own border (the card's border frames everything) and no
       hover lift (the card is one unit — hover outlines the whole card). */
    border-radius: var(--radius) var(--radius) 0 0;
    overflow: hidden;
    background: var(--surface);
  }
  .card:hover,
  .card:has(.open-overlay:focus-visible) {
    /* Hover/focus draws a 2px --accent outline around the WHOLE card (cover +
       tile), following --radius-lg. An outline (not a border-width change or a
       shadow) won't reflow and isn't clipped by the card's rounded corners. */
    outline: 2px solid var(--accent);
    outline-offset: 0;
  }
  /* The real focus target is the overlay button; route its focus ring to the
     whole-card outline above (keyboard focus == hover) and suppress the
     default ring on the button itself. */
  .open-overlay:focus-visible {
    outline: none;
  }

  .cover img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
    /* Slow editorial zoom on hover. Transform-only, clipped by the cover's
       overflow:hidden, so it composites without repainting the image. */
    transition: transform var(--dur-slow) var(--ease-out);
  }
  .card:hover .cover img,
  .card:has(.open-overlay:focus-visible) .cover img {
    transform: scale(1.04);
  }

  .placeholder {
    width: 100%;
    height: 100%;
    display: grid;
    place-items: center;
    padding: var(--sp-3);
    text-align: center;
    font-family: var(--font-display);
    font-size: var(--text-sm);
    font-weight: 500;
    line-height: var(--lh-snug);
    color: var(--muted);
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }

  /* Progress as a thin accent rule pinned to the foot of the cover. */
  .progress {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 3px;
    background: color-mix(in srgb, var(--fg) 14%, transparent);
  }
  .bar {
    height: 100%;
    background: var(--accent);
  }

  /* Shared style for the two corner actions (gear + flair). */
  .chip-btn {
    position: absolute;
    /* Cover is full-bleed at the card's top edge, so corner buttons offset by
       --sp-1 from the card edge to sit just inside the cover's corner (chips
       are card-level, not inside .cover, per the stacking note in the markup). */
    top: var(--sp-1);
    z-index: 3;
    display: grid;
    place-items: center;
    width: 1.6rem;
    height: 1.6rem;
    border: none;
    border-radius: 50%;
    background: color-mix(in srgb, #000 55%, transparent);
    color: #fff;
    cursor: pointer;
    opacity: 0;
    transition:
      opacity var(--dur-fast) var(--ease-out),
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .card:hover .chip-btn,
  .card:focus-within .chip-btn {
    opacity: 1;
  }
  .chip-btn:active {
    transform: scale(0.95);
  }
  /* The gear opens the actions menu (edit / share / delete) at top-right. */
  .actions-btn {
    right: var(--sp-1);
  }
  .actions-btn:hover {
    background: color-mix(in srgb, #000 72%, transparent);
  }
  .flair-btn {
    left: var(--sp-1);
  }
  .flair-btn:hover {
    background: color-mix(in srgb, #000 72%, transparent);
  }

  .flair-badge {
    position: absolute;
    bottom: var(--sp-1);
    left: var(--sp-1);
    max-width: calc(100% - var(--sp-2));
    padding: 0.1rem 0.4rem;
    border-radius: var(--radius-sm);
    font-size: 0.66rem;
    font-weight: 700;
    line-height: 1.3;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.3);
  }

  .flair-menu {
    position: absolute;
    top: 2rem;
    left: var(--sp-1);
    z-index: 21;
    min-width: 10rem;
    padding: var(--sp-1);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    transform-origin: top left;
    --menu-pop-y: -2px;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  /* Edge-aware flips: keep the popover inside the viewport for cards in the
     last column / near the bottom. flip-y anchors the menu's bottom to where
     its top would have been (just below the trigger) and grows upward. */
  .flair-menu.flip-x {
    left: auto;
    right: var(--sp-1);
    transform-origin: top right;
  }
  .flair-menu.flip-y {
    top: auto;
    bottom: calc(100% - 2rem);
    transform-origin: bottom left;
  }
  .flair-menu.flip-x.flip-y {
    transform-origin: bottom right;
  }

  /* The gear's actions menu mirrors the flair popover's framing but anchors to
     the top-right (under the gear). It's right-anchored, so it only needs the
     vertical flip near the viewport's bottom edge. */
  .actions-menu {
    position: absolute;
    top: 2rem;
    right: var(--sp-1);
    z-index: 21;
    min-width: 9rem;
    padding: var(--sp-1);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    transform-origin: top right;
    --menu-pop-y: -2px;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  .actions-menu.flip-y {
    top: auto;
    bottom: calc(100% - 2rem);
    transform-origin: bottom right;
  }
  .menu-heading {
    margin: 0.1rem 0.4rem 0.3rem;
  }
  .menu-item {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    width: 100%;
    padding: 0.35rem 0.4rem;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    text-align: left;
    cursor: pointer;
    transition:
      background var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .menu-item:hover {
    background: var(--surface-hover);
  }
  .menu-item:active {
    transform: scale(0.98);
  }
  .menu-item.active {
    font-weight: 600;
  }
  .menu-item .dot {
    width: 0.7rem;
    height: 0.7rem;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .menu-item .menu-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .menu-item .check {
    display: inline-flex;
    color: var(--accent);
  }
  .menu-item .menu-ico {
    display: inline-flex;
    flex-shrink: 0;
    color: var(--muted);
  }
  .menu-item.danger {
    color: var(--danger);
  }
  .menu-item.danger .menu-ico {
    color: var(--danger);
  }
  .menu-item.danger:hover {
    background: var(--danger-surface);
  }

  .meta {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    /* The meta panel is the visible part of the tile; pad the text off the card
       edges so the title/author sit comfortably on the --surface fill while the
       cover above stays full-bleed (tile + cover share the full card width). */
    padding: var(--sp-2) var(--sp-3) var(--sp-3);
  }
  .title {
    font-family: var(--font-display);
    font-size: var(--text-base);
    font-weight: 500;
    line-height: var(--lh-snug);
    overflow: hidden;
    text-overflow: ellipsis;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    transition: color var(--dur) var(--ease-out);
  }
  .card:hover .title,
  .card:has(.open-overlay:focus-visible) .title {
    color: var(--accent);
  }
  .author {
    font-size: var(--text-xs);
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
