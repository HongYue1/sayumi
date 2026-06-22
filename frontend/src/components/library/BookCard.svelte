<script lang="ts">
  import { getCoverUrl, type BookMeta, type FlairDef } from "~/api/client";
  import { findFlair, flairTextColor } from "~/lib/flairs";
  import Icon from "~/lib/Icon.svelte";
  import { Trash2, Tag, Check } from "@lucide/svelte";

  interface Props {
    book: BookMeta;
    flairs: FlairDef[];
    index?: number;
    onopen: (id: string) => void;
    onremove: (id: string) => void;
    onsetflair: (bookId: string, flairId: string | null) => void;
  }

  let {
    book,
    flairs,
    index = 0,
    onopen,
    onremove,
    onsetflair,
  }: Props = $props();
  // One-shot entrance stagger: index is read once on mount by design (cards are
  // keyed by book.id, so each instance keeps its original position), and the
  // delay only drives the mount animation. Capped so large libraries don't
  // accumulate long delays.
  // svelte-ignore state_referenced_locally
  const enterDelay = Math.min(index, 16) * 28;

  let coverFailed = $state(false);
  let menuOpen = $state(false);
  const showCover = $derived(book.hasCover && !coverFailed);
  const pct = $derived(
    Math.round(Math.max(0, Math.min(1, book.progress)) * 100),
  );
  const flair = $derived(findFlair(book.flairId, flairs));
  // Whether the book's current flair is one of the selectable options, so the
  // menu can open with focus on the checked item (menuitemradio model, matching
  // ThemeDropdown) and fall back to the first item only when nothing is set.
  const hasActiveFlair = $derived(flairs.some((f) => f.id === book.flairId));

  function remove(e: MouseEvent): void {
    e.stopPropagation();
    if (confirm(`Remove “${book.title}” from your library?`)) onremove(book.id);
  }

  let flairBtn = $state<HTMLButtonElement | null>(null);
  let menuEl = $state<HTMLDivElement | null>(null);

  function toggleMenu(e: MouseEvent): void {
    e.stopPropagation();
    menuOpen = !menuOpen;
  }

  function closeMenu(restoreFocus = true): void {
    menuOpen = false;
    if (restoreFocus) flairBtn?.focus();
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
    if (!menuOpen) return;
    const onClick = (e: MouseEvent): void => {
      const t = e.target as Node | null;
      if (menuEl?.contains(t) || flairBtn?.contains(t)) return;
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

<div class="card" style:--enter-delay={`${enterDelay}ms`}>
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
        src={getCoverUrl(book.id)}
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
    class="chip-btn remove"
    title="Remove"
    aria-label="Remove book"
    onclick={remove}
  >
    <Icon icon={Trash2} size={15} />
  </button>
  <button
    bind:this={flairBtn}
    class="chip-btn flair-btn"
    title="Set flair"
    aria-label="Set flair"
    aria-haspopup="menu"
    aria-expanded={menuOpen}
    onclick={toggleMenu}
  >
    <Icon icon={Tag} size={15} />
  </button>

  <div class="meta">
    <div class="title" title={book.title}>{book.title}</div>
    {#if book.author}<div class="author" title={book.author}>
        {book.author}
      </div>{/if}
  </div>

  {#if menuOpen}
    <div
      bind:this={menuEl}
      class="flair-menu"
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
</div>

<style>
  .card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--sp-2);
    cursor: pointer;
    border: none;
    background: none;
    text-align: left;
    animation: card-in var(--dur-slow) var(--ease-out) both;
    animation-delay: var(--enter-delay, 0ms);
  }

  /* Transparent full-card hit target for "open". Sits above the cover art but
     below the corner action buttons (z-index: 3) and the flair menu/scrim. */
  .open-overlay {
    position: absolute;
    inset: 0;
    z-index: 1;
    padding: 0;
    border: none;
    border-radius: 6px;
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
    border-radius: 6px;
    overflow: hidden;
    background: var(--surface);
    /* Glossier shelf: a hairline border + a resting two-layer elevation so each
       card sits slightly proud even at rest. Hover lifts it further and deepens
       the shadow so the book reads as picked up off the shelf. */
    border: 1px solid var(--hairline);
    box-shadow:
      0 2px 6px color-mix(in srgb, var(--fg) 14%, transparent),
      0 1px 2px color-mix(in srgb, var(--fg) 10%, transparent);
    /* The lift is transform (GPU-composited); the shadow + border only change
       on hover/focus. Hover repaints one card at a time and never runs at load,
       so it stays clear of the Lighthouse budget. */
    transition:
      transform var(--dur) var(--ease-out),
      box-shadow var(--dur) var(--ease-out),
      border-color var(--dur) var(--ease-out);
  }
  .card:hover .cover,
  .card:has(.open-overlay:focus-visible) .cover {
    transform: translateY(-6px);
    border-color: color-mix(in srgb, var(--accent) 35%, var(--hairline));
    box-shadow:
      0 12px 28px color-mix(in srgb, var(--fg) 22%, transparent),
      0 3px 8px color-mix(in srgb, var(--fg) 14%, transparent);
  }
  /* Cover-targeted focus ring hugs the artwork instead of the whole column.
     Driven off the overlay button via :has, since the card itself is no longer
     the focusable element. */
  .open-overlay:focus-visible {
    outline: none;
  }
  .card:has(.open-overlay:focus-visible) .cover {
    border-color: var(--accent);
    box-shadow: var(--focus);
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

  /* Shared style for the two corner actions (remove + flair). */
  .chip-btn {
    position: absolute;
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
  .remove {
    right: var(--sp-1);
  }
  .remove:hover {
    background: #b3402f;
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
    border-radius: 0.3rem;
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
    box-shadow: 0 6px 20px color-mix(in srgb, var(--fg) 22%, transparent);
    transform-origin: top left;
    --menu-pop-y: -2px;
    animation: app-menu-pop-in var(--dur-fast) var(--ease-out) both;
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
    border-radius: 0.35rem;
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

  .meta {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
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
