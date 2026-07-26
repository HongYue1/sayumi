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
  const enterDelay = Math.min(index, 16) * 32;

  // Remember the exact URL that failed rather than permanently suppressing the
  // cover for this book id. Metadata/cover edits advance updatedAt while the
  // keyed card instance stays mounted, so a new URL must get a fresh attempt.
  let failedCoverUrl = $state<string | null>(null);
  const coverUrl = $derived(getCoverUrl(book.id, book.updatedAt));
  let openMenu = $state<"flair" | "actions" | null>(null);
  const showCover = $derived(book.hasCover && failedCoverUrl !== coverUrl);
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
    // The dialog focus trap snapshots the active element on mount. Restore the
    // trigger before removing this focused menu item so focus can round-trip.
    closeMenu();
    onedit(book.id);
  }

  function chooseShare(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu();
    onshare(book.id);
  }

  function chooseDelete(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu();
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

  <!-- The volume: cover art treated as a physical book — spine shading on the
       left, a soft ink shadow beneath, and a lift on hover. -->
  <div class="volume">
    <div class="cover">
      {#if showCover}
        <img
          src={coverUrl}
          alt=""
          loading={index < 8 ? "eager" : "lazy"}
          fetchpriority={index === 0 ? "high" : undefined}
          decoding="async"
          onerror={() => (failedCoverUrl = coverUrl)}
        />
      {:else}
        <!-- Publisher's plain jacket for books without cover art. -->
        <div class="placeholder">
          <span class="ph-frame" aria-hidden="true"></span>
          <span class="ph-fleuron" aria-hidden="true">❦</span>
          <span class="ph-title display">{book.title}</span>
        </div>
      {/if}

      {#if pct > 0}
        <div
          class="progress"
          role="progressbar"
          aria-label={`Reading progress for ${book.title}`}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
          aria-valuetext={`${pct}% read`}
          title={`${pct}% read`}
        >
          <div class="bar" style:width={`${pct}%`}></div>
        </div>
      {/if}

      {#if flair}
        <span
          class="flair-badge"
          style:background={flair.color}
          style:color={flairTextColor(flair.color)}
        >
          {flair.label}
        </span>
      {/if}
    </div>
  </div>

  <!-- Corner actions live at card level (NOT inside .volume): the volume's
       hover-lift transform establishes a stacking context, which would trap
       these buttons beneath the z-index:1 open-overlay and swallow their
       clicks + :hover. At card level they stay above the overlay (z-index:3). -->
  <button
    bind:this={actionsBtn}
    class="chip-btn actions-btn"
    title="Actions"
    aria-label={`Book actions for ${book.title}`}
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
    aria-label={`Set flair for ${book.title}`}
    aria-haspopup="menu"
    aria-expanded={openMenu === "flair"}
    onclick={toggleFlair}
  >
    <Icon icon={Tag} size={15} />
  </button>

  <!-- Catalog caption beneath the volume. -->
  <div class="meta">
    <div class="title display" title={book.title}>{book.title}</div>
    <div class="byline">
      {#if book.author}<span class="author" title={book.author}
          >{book.author}</span
        >{/if}
      {#if pct > 0}<span class="pct tnum">{pct}%</span>{/if}
    </div>
  </div>

  {#if openMenu === "flair"}
    <div
      bind:this={menuEl}
      class="flair-menu paper"
      class:flip-x={flipX}
      class:flip-y={flipY}
      role="menu"
      tabindex="-1"
      aria-label={`Set flair for ${book.title}`}
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
      class="actions-menu paper"
      class:flip-x={flipX}
      class:flip-y={flipY}
      role="menu"
      tabindex="-1"
      aria-label={`Book actions for ${book.title}`}
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
    text-align: left;
    animation: card-in var(--dur-slower) var(--ease-out) both;
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
     while open, so this targets exactly the active card), and raise the card
     above sibling cards — each card's entrance transform makes it a stacking
     context, so a later sibling would otherwise paint over a menu that spills
     into its area (narrow grids on small screens). */
  .card:has(.flair-menu),
  .card:has(.actions-menu) {
    content-visibility: visible;
    z-index: 25;
  }

  /* Transparent full-card hit target for "open". Sits above the cover art but
     below the corner action buttons (z-index: 3) and the popover menus. */
  .open-overlay {
    position: absolute;
    inset: 0;
    z-index: 1;
    padding: 0;
    border: none;
    border-radius: var(--radius);
    background: transparent;
    cursor: pointer;
  }

  @keyframes card-in {
    from {
      opacity: 0;
      transform: translateY(16px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  /* The physical volume: transform-only lift, shadow crossfaded on a pseudo
     element so hover never repaints the cover art. */
  .volume {
    position: relative;
    transition: transform var(--dur-slow) var(--ease-out);
  }
  .card:hover .volume,
  .card:has(.open-overlay:focus-visible) .volume {
    transform: translateY(-6px) rotate(-0.4deg);
  }

  .cover {
    position: relative;
    aspect-ratio: 2 / 3;
    /* A book's squarer spine edge on the left, softer page edge on the right. */
    border-radius: 3px 8px 8px 3px;
    overflow: hidden;
    background: var(--raised);
    box-shadow: var(--shadow-2);
  }
  /* Hover shadow lives on the volume (under the cover), crossfaded via opacity. */
  .volume::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: 3px 8px 8px 3px;
    box-shadow: var(--shadow-3);
    opacity: 0;
    pointer-events: none;
    transition: opacity var(--dur-slow) var(--ease-out);
    z-index: -1;
  }
  .card:hover .volume::after,
  .card:has(.open-overlay:focus-visible) .volume::after {
    opacity: 1;
  }
  /* Spine shading + a whisper of gloss along the fore-edge. */
  .cover::after {
    content: "";
    position: absolute;
    inset: 0;
    border-radius: inherit;
    pointer-events: none;
    background: linear-gradient(
      to right,
      rgb(0 0 0 / 0.22),
      rgb(255 255 255 / 0.08) 3%,
      transparent 8%,
      transparent 97%,
      rgb(0 0 0 / 0.08)
    );
    /* Hairline keyline so near-white covers don't dissolve into the page. */
    box-shadow: inset 0 0 0 1px
      light-dark(rgb(0 0 0 / 0.08), rgb(255 255 255 / 0.08));
  }

  /* Keyboard focus draws the ring around the volume itself. */
  .open-overlay:focus-visible {
    outline: none;
  }
  .card:has(.open-overlay:focus-visible) .cover {
    box-shadow:
      var(--shadow-2),
      0 0 0 2px var(--bg),
      0 0 0 4px var(--accent);
  }

  .cover img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  /* Publisher's plain jacket: typeset title inside a hairline frame. */
  .placeholder {
    position: relative;
    width: 100%;
    height: 100%;
    display: grid;
    place-items: center;
    padding: var(--sp-5) var(--sp-4);
    text-align: center;
    background: linear-gradient(
      160deg,
      var(--surface),
      color-mix(in srgb, var(--accent) 10%, transparent)
    );
  }
  .ph-frame {
    position: absolute;
    inset: 9px;
    border: 1px solid var(--hairline-strong);
    border-radius: 2px;
    pointer-events: none;
  }
  .ph-frame::after {
    content: "";
    position: absolute;
    inset: 3px;
    border: 1px solid var(--hairline);
    border-radius: 1px;
  }
  .ph-fleuron {
    position: absolute;
    top: 16%;
    left: 0;
    right: 0;
    font-size: var(--text-sm);
    color: var(--faint);
  }
  .ph-title {
    font-size: var(--text-base);
    font-style: italic;
    font-weight: 520;
    line-height: var(--lh-snug);
    color: var(--muted);
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 5;
    line-clamp: 5;
    -webkit-box-orient: vertical;
  }

  /* Progress as a thin accent rule pinned to the foot of the cover. */
  .progress {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    height: 3px;
    background: light-dark(rgb(0 0 0 / 0.18), rgb(255 255 255 / 0.18));
  }
  .bar {
    height: 100%;
    background: var(--accent);
    border-radius: 0 2px 2px 0;
  }

  /* Shared style for the two corner actions (gear + flair): small ink-glass
     squares that fade in on hover. */
  .chip-btn {
    position: absolute;
    top: var(--sp-2);
    z-index: 3;
    display: grid;
    place-items: center;
    width: 1.75rem;
    height: 1.75rem;
    border: none;
    border-radius: var(--radius-sm);
    background: rgb(0 0 0 / 0.5);
    -webkit-backdrop-filter: blur(6px);
    backdrop-filter: blur(6px);
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
  /* Coarse pointers cannot discover hover-only controls before tapping them. */
  @media (hover: none), (pointer: coarse) {
    .chip-btn {
      opacity: 1;
    }
  }
  .chip-btn:hover {
    background: rgb(0 0 0 / 0.72);
  }
  .chip-btn:active {
    transform: scale(0.94);
  }
  /* The gear opens the actions menu (edit / share / delete) at top-right. */
  .actions-btn {
    right: var(--sp-2);
  }
  .flair-btn {
    left: var(--sp-2);
  }

  .flair-badge {
    position: absolute;
    bottom: var(--sp-2);
    left: var(--sp-2);
    max-width: calc(100% - var(--sp-4));
    padding: 0.14rem 0.5rem;
    border-radius: 999px;
    font-size: 0.66rem;
    font-weight: 700;
    letter-spacing: 0.03em;
    line-height: 1.4;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    box-shadow: 0 1px 4px rgb(0 0 0 / 0.35);
  }

  .flair-menu {
    position: absolute;
    top: 2.4rem;
    left: var(--sp-2);
    z-index: 21;
    min-width: 10.5rem;
    max-height: min(calc(50dvh - var(--sp-2)), 28rem);
    overflow-x: hidden;
    overflow-y: auto;
    padding: var(--sp-2);
    transform-origin: top left;
    --menu-pop-y: -3px;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  /* Edge-aware flips: keep the popover inside the viewport for cards in the
     last column / near the bottom. flip-y anchors the menu's bottom to where
     its top would have been (just below the trigger) and grows upward. */
  .flair-menu.flip-x {
    left: auto;
    right: var(--sp-2);
    transform-origin: top right;
  }
  .flair-menu.flip-y {
    top: auto;
    bottom: calc(100% - 2.4rem);
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
    top: 2.4rem;
    right: var(--sp-2);
    z-index: 21;
    min-width: 9.5rem;
    padding: var(--sp-2);
    transform-origin: top right;
    --menu-pop-y: -3px;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
  }
  .actions-menu.flip-y {
    top: auto;
    bottom: calc(100% - 2.4rem);
    transform-origin: bottom right;
  }
  .menu-heading {
    margin: 0.15rem 0.45rem 0.4rem;
  }
  .menu-item {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    width: 100%;
    padding: 0.42rem 0.5rem;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 520;
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
    font-weight: 650;
  }
  .menu-item .dot {
    width: 0.65rem;
    height: 0.65rem;
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
    color: var(--danger-surface-fg);
  }
  .menu-item.danger:hover .menu-ico {
    color: var(--danger-surface-fg);
  }

  /* Catalog caption: open type under the volume, no tile. */
  .meta {
    display: flex;
    flex-direction: column;
    gap: 0.18rem;
    padding: var(--sp-2) var(--sp-1) 0;
  }
  .title {
    font-size: 0.98rem;
    font-weight: 540;
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
  .byline {
    display: flex;
    align-items: baseline;
    gap: var(--sp-2);
  }
  .author {
    flex: 1;
    min-width: 0;
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.09em;
    text-transform: uppercase;
    color: var(--faint);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .pct {
    flex: none;
    margin-left: auto;
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--accent);
  }
</style>
