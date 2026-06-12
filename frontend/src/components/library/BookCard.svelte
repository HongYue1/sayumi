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

  let { book, flairs, index = 0, onopen, onremove, onsetflair }: Props = $props();
  // One-shot entrance stagger: index is read once on mount by design (cards are
  // keyed by book.id, so each instance keeps its original position), and the
  // delay only drives the mount animation. Capped so large libraries don't
  // accumulate long delays.
  // svelte-ignore state_referenced_locally
  const enterDelay = Math.min(index, 16) * 28;

  let coverFailed = $state(false);
  let menuOpen = $state(false);
  const showCover = $derived(book.hasCover && !coverFailed);
  const pct = $derived(Math.round(Math.max(0, Math.min(1, book.progress)) * 100));
  const flair = $derived(findFlair(book.flairId, flairs));

  function remove(e: MouseEvent): void {
    e.stopPropagation();
    if (confirm(`Remove “${book.title}” from your library?`)) onremove(book.id);
  }

  let flairBtn = $state<HTMLButtonElement | null>(null);

  function toggleMenu(e: MouseEvent): void {
    e.stopPropagation();
    menuOpen = !menuOpen;
  }

  function closeMenu(restoreFocus = true): void {
    menuOpen = false;
    if (restoreFocus) flairBtn?.focus();
  }

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
    }
  }
</script>

<div
  class="card"
  role="button"
  tabindex="0"
  style:--enter-delay={`${enterDelay}ms`}
  onclick={() => onopen(book.id)}
  onkeydown={(e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onopen(book.id);
    }
  }}
>
  <div class="cover">
    {#if showCover}
      <img
        src={getCoverUrl(book.id)}
        alt={book.title}
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
      <span class="flair-badge" style:background={flair.color} style:color={flairTextColor()}>
        {flair.label}
      </span>
    {/if}

    <button class="chip-btn remove" title="Remove" aria-label="Remove book" onclick={remove}>
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
  </div>

  <div class="meta">
    <div class="title" title={book.title}>{book.title}</div>
    {#if book.author}<div class="author" title={book.author}>{book.author}</div>{/if}
  </div>

  {#if menuOpen}
    <!-- Scrim closes the menu; sits above the card, below the menu. -->
    <button
      class="menu-scrim"
      aria-label="Close menu"
      onclick={(e) => {
        e.stopPropagation();
        closeMenu();
      }}
    ></button>
    <div class="flair-menu" role="menu" tabindex="-1" aria-label="Set flair" onkeydown={onMenuKeydown}>
      <p class="menu-heading eyebrow" aria-hidden="true">Set flair</p>
      {#each flairs as f, i (f.id)}
        {@const isActive = book.flairId === f.id}
        <button
          class="menu-item"
          class:active={isActive}
          role="menuitemradio"
          aria-checked={isActive}
          {@attach (el) => {
            // Move focus into the menu on open so keyboard users land inside it.
            if (i === 0) (el as HTMLButtonElement).focus();
          }}
          onclick={(e) => pick(e, f.id)}
        >
          <span class="dot" style:background={f.color} aria-hidden="true"></span>
          <span class="menu-label">{f.label}</span>
          {#if isActive}<span class="check" aria-hidden="true"><Icon icon={Check} size={15} /></span>{/if}
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
    border-radius: 3px;
    overflow: hidden;
    background: var(--surface);
    /* Restrained, editorial elevation: a hairline border + one subtle, grounded
       shadow (no heavy drop shadow). Hover lifts via transform only. */
    border: 1px solid var(--hairline);
    box-shadow: 0 1px 3px color-mix(in srgb, var(--fg) 12%, transparent);
    /* Animate transform only (GPU-composited). The resting shadow stays static
       so the hover lift never triggers per-frame paint. */
    transition: transform var(--dur) var(--ease-out);
  }
  .card:hover .cover,
  .card:focus-visible .cover {
    transform: translateY(-3px);
  }
  /* Rely on a cover-targeted ring rather than the global card ring, so the
     focus indicator hugs the artwork instead of the whole card column. */
  .card:focus-visible {
    outline: none;
    box-shadow: none;
  }
  .card:focus-visible .cover {
    border-color: var(--accent);
    box-shadow: var(--focus);
  }

  .cover img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
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

  .menu-scrim {
    position: fixed;
    inset: 0;
    z-index: 20;
    border: none;
    background: transparent;
    cursor: default;
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
    animation: menu-in var(--dur-fast) var(--ease-out) both;
  }
  @keyframes menu-in {
    from {
      opacity: 0;
      transform: scale(0.97) translateY(-2px);
    }
    to {
      opacity: 1;
      transform: scale(1) translateY(0);
    }
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
