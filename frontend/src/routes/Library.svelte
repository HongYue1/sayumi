<script lang="ts">
  import { onMount } from "svelte";
  import { library, SORT_OPTIONS } from "~/lib/library.svelte";
  import { session } from "~/lib/session.svelte";
  import { settings } from "~/lib/settings.svelte";
  import { applyTheme } from "~/lib/theme";
  import { router } from "~/lib/router.svelte";
  import BookCard from "~/components/library/BookCard.svelte";
  import ThemeDropdown from "~/components/library/ThemeDropdown.svelte";
  import ProfileMenu from "~/components/library/ProfileMenu.svelte";
  import ProfileDialog from "~/components/library/ProfileDialog.svelte";
  import EditBookDialog from "~/components/library/EditBookDialog.svelte";
  import ShareDialog from "~/components/library/ShareDialog.svelte";
  import Icon from "~/lib/Icon.svelte";
  import {
    Plus,
    RefreshCw,
    ArrowUpDown,
    Check,
    ChevronDown,
    X,
  } from "@lucide/svelte";

  import { DEFAULT_FLAIRS } from "~/lib/flairs";

  let fileInput = $state<HTMLInputElement | null>(null);
  let newFlair = $state("");
  // Drag-and-drop: a counter (not a bool) avoids flicker as the pointer crosses
  // child elements, since dragenter/dragleave fire per element.
  let dragDepth = $state(0);
  const dragging = $derived(dragDepth > 0);

  // Which profile dialog (if any) is open. Rendered at .library level — never
  // inside the command bar, whose backdrop-filter would clip a fixed overlay
  // to the bar box.
  let profileDialog = $state<"clone" | "delete" | null>(null);

  // The book currently being edited, tracked by id so the open dialog reflects
  // live store updates (and auto-closes if the book is removed) rather than
  // pinning a stale snapshot.
  let editingId = $state<string | null>(null);
  const editingBook = $derived(
    editingId ? (library.books.find((b) => b.id === editingId) ?? null) : null,
  );

  // Same id-tracked pattern for the share dialog so it reflects live store
  // updates and auto-closes if the book is removed.
  let sharingId = $state<string | null>(null);
  const sharingBook = $derived(
    sharingId ? (library.books.find((b) => b.id === sharingId) ?? null) : null,
  );

  // ---- custom sort menu (native <select> popups can't be themed) ----------
  let sortOpen = $state(false);
  let sortTrigger = $state<HTMLButtonElement | null>(null);
  let sortMenuEl = $state<HTMLElement | null>(null);
  const sortLabel = $derived(
    SORT_OPTIONS.find((o) => o.key === library.sort)?.label ?? "Sort",
  );

  function closeSort(restoreFocus = true): void {
    if (!sortOpen) return;
    sortOpen = false;
    if (restoreFocus) sortTrigger?.focus();
  }
  function chooseSort(key: (typeof SORT_OPTIONS)[number]["key"]): void {
    library.sort = key;
    closeSort();
  }
  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the
  // sticky bar's backdrop-filter establishes a containing block, which would
  // clip it to the bar box. A window listener is container-proof, matching
  // ThemeDropdown / ProfileMenu.
  function onSortOutside(e: PointerEvent): void {
    const t = e.target as Node;
    if (sortMenuEl?.contains(t) || sortTrigger?.contains(t)) return;
    closeSort(false);
  }
  function onSortKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeSort();
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
    // model used by ThemeDropdown / ProfileMenu / BookCard.
    const menu = e.currentTarget as HTMLElement;
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>(".sort-item"),
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

  onMount(() => {
    // Activate and load together: child onMount can run before App's profile
    // effect after a full-page refresh, so a bare load() could still see the
    // store's initial null profile and silently no-op.
    void library.loadForProfile(session.profile);
    // Reflect the profile's saved theme in the library (not just the reader).
    // Guard the fetch: on failure, apply whatever theme we already have rather
    // than leaving an unhandled rejection (and a stuck default theme).
    settings
      .load()
      .then(() => applyTheme(settings.value.theme))
      .catch(() => applyTheme(settings.value.theme));
  });

  function openBook(id: string): void {
    router.navigate(`/read/${encodeURIComponent(id)}`);
  }

  async function onFilePicked(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const files = Array.from(input.files ?? []);
    if (files.length) await library.uploadFiles(files);
    input.value = ""; // allow re-uploading the same file
  }

  function hasFiles(e: DragEvent): boolean {
    return Array.from(e.dataTransfer?.types ?? []).includes("Files");
  }
  function onDragEnter(e: DragEvent): void {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth += 1;
  }
  function onDragOver(e: DragEvent): void {
    if (hasFiles(e)) e.preventDefault(); // allow drop
  }
  function onDragLeave(): void {
    if (dragDepth > 0) dragDepth -= 1;
  }
  async function onDrop(e: DragEvent): Promise<void> {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    const files = Array.from(e.dataTransfer?.files ?? []);
    if (files.length) await library.uploadFiles(files);
  }

  const isCustom = (id: string) => !DEFAULT_FLAIRS.some((f) => f.id === id);

  async function addFlair(): Promise<void> {
    const name = newFlair.trim();
    if (!name) return;
    await library.addCustomFlair(name);
    newFlair = "";
  }
</script>

<svelte:window onpointerdown={sortOpen ? onSortOutside : undefined} />

<div
  class="library"
  ondragenter={onDragEnter}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  role="region"
  aria-label="Library"
>
  <!-- One compact command bar: identity + every library control in a single
       pinned row, so the shelf owns the screen. -->
  <header class="bar">
    <h1 class="lockup">
      <span class="wordmark">Sayumi</span>
      <span class="lockup-mark" aria-hidden="true">❦</span>
    </h1>

    <input
      class="field search"
      type="search"
      placeholder="Search title or author…"
      value={library.query}
      oninput={(e) => library.setQuery(e.currentTarget.value)}
      aria-label="Search library"
    />

    <div class="sort-dd">
      <button
        bind:this={sortTrigger}
        class="btn-ghost press sort-trigger"
        class:open={sortOpen}
        aria-haspopup="menu"
        aria-expanded={sortOpen}
        aria-label={`Sort by (current: ${sortLabel})`}
        onclick={() => (sortOpen = !sortOpen)}
      >
        <Icon icon={ArrowUpDown} size={15} />
        <span class="sort-label">{sortLabel}</span>
        <Icon icon={ChevronDown} size={14} class="caret" />
      </button>
      {#if sortOpen}
        <div
          bind:this={sortMenuEl}
          class="sort-menu paper"
          role="menu"
          tabindex="-1"
          aria-label="Sort by"
          onkeydown={onSortKeydown}
        >
          {#each SORT_OPTIONS as opt (opt.key)}
            {@const active = library.sort === opt.key}
            <button
              class="sort-item"
              class:active
              role="menuitemradio"
              aria-checked={active}
              tabindex={active ? 0 : -1}
              {@attach (el) => {
                // Focus the current sort on open (menuitemradio model).
                if (active) (el as HTMLButtonElement).focus();
              }}
              onclick={() => chooseSort(opt.key)}
            >
              <span class="sort-item-label">{opt.label}</span>
              {#if active}<span class="check" aria-hidden="true"
                  ><Icon icon={Check} size={15} /></span
                >{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <button
      class="icon-btn press"
      class:active={library.rescanning}
      onclick={() => library.rescan()}
      disabled={library.rescanning}
      aria-label={library.rescanning
        ? "Scanning library folder…"
        : "Rescan the Library folder for new files"}
      title="Scan the Library folder for new files"
    >
      <Icon
        icon={RefreshCw}
        size={17}
        class={library.rescanning ? "spin" : ""}
      />
    </button>

    <button
      class="btn press upload"
      onclick={() => fileInput?.click()}
      disabled={library.uploading}
    >
      <Icon icon={Plus} size={16} />
      <span class="upload-label"
        >{library.uploading ? "Uploading…" : "Add book"}</span
      >
    </button>
    <input
      bind:this={fileInput}
      type="file"
      accept=".epub,application/epub+zip"
      multiple
      hidden
      onchange={onFilePicked}
    />

    <span class="bar-divider" aria-hidden="true"></span>
    <ThemeDropdown />
    <ProfileMenu
      onclone={() => (profileDialog = "clone")}
      ondelete={() => (profileDialog = "delete")}
    />
  </header>

  {#if library.books.length > 0}
    <div class="flairbar">
      <p class="eyebrow count">
        <span class="tnum">{library.books.length}</span>
        {library.books.length === 1 ? "book" : "books"}
      </p>
      <span class="flair-divider" aria-hidden="true"></span>
      {#each library.allFlairs as f (f.id)}
        {@const active = library.flairFilters.includes(f.id)}
        <span class="chip" class:active style:--chip={f.color}>
          <button
            class="chip-toggle"
            aria-pressed={active}
            onclick={() => library.toggleFlairFilter(f.id)}
          >
            {#if active}
              <span class="chip-check" style:color={f.color} aria-hidden="true"
                ><Icon icon={Check} size={13} /></span
              >
            {:else}
              <span class="dot" style:background={f.color}></span>
            {/if}
            {f.label}
          </button>
          {#if isCustom(f.id)}
            <button
              class="chip-del"
              title="Delete flair"
              aria-label={`Delete flair ${f.label}`}
              onclick={() => library.removeCustomFlair(f.id)}
              ><Icon icon={X} size={13} /></button
            >
          {/if}
        </span>
      {/each}

      <span class="addflair">
        <input
          type="text"
          placeholder="New flair…"
          maxlength="40"
          bind:value={newFlair}
          onkeydown={(e) => e.key === "Enter" && addFlair()}
          aria-label="New flair name"
        />
        <button onclick={addFlair} disabled={!newFlair.trim()}>Add</button>
      </span>

      {#if library.flairFilters.length > 0}
        <button
          class="clear-filters"
          onclick={() => library.clearFlairFilters()}>Clear</button
        >
      {/if}
    </div>
  {/if}

  {#if library.error}
    <p class="error" role="alert">{library.error}</p>
  {/if}

  {#if library.loading}
    <p class="state" role="status">Loading…</p>
  {:else if library.books.length === 0}
    <div class="empty">
      <span class="fleuron empty-mark" aria-hidden="true">❦</span>
      <p class="empty-title display">An empty shelf.</p>
      <p class="empty-sub">Every library starts with a single book.</p>
      <button
        class="btn press"
        onclick={() => fileInput?.click()}
        disabled={library.uploading}
      >
        <Icon icon={Plus} size={16} />
        {library.uploading ? "Uploading…" : "Add your first book"}
      </button>
      <p class="hint">
        …or drop .epub files into your <code>Library</code> folder.
      </p>
    </div>
  {:else if library.visible.length === 0}
    <div class="noresults" role="status">
      <p class="empty-title display">Nothing on this shelf.</p>
      <p class="state">No books match your search or filters.</p>
      <button
        class="btn-ghost press"
        onclick={() => {
          library.setQuery("");
          library.clearFlairFilters();
        }}>Clear search &amp; filters</button
      >
    </div>
  {:else}
    <div class="grid" role="list">
      {#each library.visible as book, i (book.id)}
        <BookCard
          {book}
          index={i}
          flairs={library.allFlairs}
          onopen={openBook}
          onremove={(id) => library.remove(id)}
          onedit={(id) => (editingId = id)}
          onshare={(id) => (sharingId = id)}
          onsetflair={(id, flairId) => library.setFlair(id, flairId)}
        />
      {/each}
    </div>
  {/if}

  {#if profileDialog && session.profile}
    <ProfileDialog
      mode={profileDialog}
      profileName={session.profile}
      onclose={() => (profileDialog = null)}
    />
  {/if}

  {#if editingBook}
    <EditBookDialog book={editingBook} onclose={() => (editingId = null)} />
  {/if}

  {#if sharingBook}
    <ShareDialog book={sharingBook} onclose={() => (sharingId = null)} />
  {/if}

  {#if dragging}
    <div class="dropzone" aria-hidden="true">
      <div class="dropzone-inner">
        <span class="dropzone-mark"><Icon icon={Plus} size={36} /></span>
        <p class="display">Add to your library</p>
        <span class="dropzone-sub">Drop .epub files anywhere</span>
      </div>
    </div>
  {/if}
</div>

<style>
  .library {
    /* Full-bleed layout: the shelf uses the whole viewport, with a fluid
       gutter that stays modest even on very wide screens. */
    --pagex: clamp(1rem, 2.5vw, 2.75rem);
    position: relative;
    min-height: calc(100vh - var(--offline-banner-h, 0px));
    padding: 0 var(--pagex) var(--sp-16);
  }

  .dropzone {
    position: fixed;
    inset: 0;
    z-index: 40;
    display: grid;
    place-items: center;
    padding: var(--sp-6);
    background: color-mix(in srgb, var(--bg) 72%, transparent);
    backdrop-filter: blur(6px);
    -webkit-backdrop-filter: blur(6px);
    animation: app-overlay-in var(--dur-fast) var(--ease-out);
  }
  .dropzone-inner {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    width: min(34rem, 90%);
    padding: var(--sp-12) var(--sp-8);
    border: 1px dashed var(--accent-line);
    outline: 1px dashed var(--accent-line);
    outline-offset: 6px;
    border-radius: var(--radius-xl);
    color: var(--fg);
    text-align: center;
    background: var(--accent-soft);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  .dropzone-mark {
    display: inline-flex;
    color: var(--accent);
  }
  .dropzone-inner p {
    margin: 0;
    font-size: var(--text-xl);
    font-style: italic;
  }
  .dropzone-sub {
    color: var(--muted);
    font-size: var(--text-sm);
  }

  /* ---- command bar ---- */
  /* One pinned row: identity + search + sort + scan + add + theme + profile.
     Full-bleed glass (negative margins re-span the page gutters) with a
     hairline base; z-index above the card stacking contexts so its dropdown
     menus always paint on top. */
  .bar {
    --control-h: 2.4rem;
    position: sticky;
    top: 0;
    z-index: 40;
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    flex-wrap: wrap;
    margin-inline: calc(var(--pagex) * -1);
    padding: var(--sp-2) var(--pagex);
    margin-bottom: var(--sp-3);
    /* Tonal separation instead of a rule: the band is the theme's own ink
       washed into translucent paper (≈6% fg), so the header reads as its own
       sheet of stock on every palette — light themes get a shade darker,
       dark themes a breath lighter. A soft shadow settles the edge without
       reintroducing a hard line. */
    background: color-mix(
      in srgb,
      var(--fg) 6%,
      color-mix(in srgb, var(--bg) 86%, transparent)
    );
    -webkit-backdrop-filter: blur(12px);
    backdrop-filter: blur(12px);
    box-shadow: var(--shadow-1);
    animation: app-rise-in var(--dur-slower) var(--ease-out) both;
  }
  .lockup {
    display: inline-flex;
    align-items: baseline;
    gap: 0.3rem;
    margin: 0 var(--sp-2) 0 0;
    font-size: 1.5rem;
    line-height: 1;
    font-weight: 520;
    white-space: nowrap;
  }
  .lockup-mark {
    font-size: 0.6em;
    color: var(--accent);
    opacity: 0.8;
    align-self: center;
  }

  .search {
    flex: 1 1 12rem;
    min-width: 9rem;
    max-width: 34rem;
    height: var(--control-h);
  }

  /* Custom themed sort menu. */
  .sort-dd {
    position: relative;
    display: inline-flex;
  }
  .sort-trigger {
    height: var(--control-h);
    font-weight: 540;
  }
  .sort-trigger :global(.caret) {
    color: var(--muted);
    transition: transform var(--dur) var(--ease-spring);
  }
  .sort-trigger.open :global(.caret) {
    transform: rotate(180deg);
  }
  .sort-menu {
    position: absolute;
    top: calc(100% + 0.5rem);
    right: 0;
    z-index: 21;
    min-width: 11.5rem;
    padding: var(--sp-2);
    transform-origin: top right;
    animation: app-menu-pop-in var(--dur) var(--ease-out) both;
  }
  .sort-item {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    width: 100%;
    padding: 0.45rem 0.6rem;
    border: none;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 520;
    text-align: left;
    cursor: pointer;
    transition: background var(--dur-fast) var(--ease-out);
  }
  .sort-item:hover,
  .sort-item:focus-visible {
    background: var(--surface-hover);
    outline: none;
  }
  .sort-item.active {
    color: var(--accent);
    font-weight: 640;
    background: var(--accent-soft);
  }
  .sort-item-label {
    flex: 1;
  }
  .sort-item .check {
    display: inline-flex;
    color: var(--accent);
  }

  .upload {
    height: var(--control-h);
  }
  /* Spin the rescan glyph while a scan is in flight. */
  .bar :global(.spin) {
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .bar-divider {
    width: 1px;
    height: 1.4rem;
    background: var(--hairline-strong);
    margin: 0 var(--sp-1);
  }

  /* Tight quarters: drop the lockup text and button label before wrapping. */
  @media (max-width: 860px) {
    .upload-label {
      display: none;
    }
    .upload {
      width: var(--control-h);
      padding: 0;
    }
  }
  @media (max-width: 640px) {
    .lockup {
      display: none;
    }
  }

  /* ---- flair chips ---- */
  .flairbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--sp-2);
    padding: var(--sp-1) 0;
    margin-bottom: var(--sp-5);
    animation: app-rise-in var(--dur-slower) var(--ease-out) 80ms both;
  }
  .count {
    margin: 0;
  }
  .flair-divider {
    width: 1px;
    height: 1rem;
    background: var(--hairline);
  }
  .chip {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--hairline);
    border-radius: 999px;
    background: var(--surface);
    overflow: hidden;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur) var(--ease-out);
  }
  .chip:hover {
    border-color: var(--hairline-strong);
  }
  /* Selected chips read as selected by fill + a check (not colour alone). */
  .chip.active {
    border-color: color-mix(in srgb, var(--chip) 55%, transparent);
    background: color-mix(in srgb, var(--chip) 14%, transparent);
  }
  .chip-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.3rem 0.75rem;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    font-weight: 560;
    letter-spacing: 0.02em;
    cursor: pointer;
    transition: transform var(--dur-fast) var(--ease-out);
  }
  .chip-toggle:active {
    transform: scale(0.96);
  }
  .chip .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .chip-check {
    display: inline-flex;
    flex-shrink: 0;
  }
  .chip-del {
    display: inline-flex;
    align-items: center;
    border: none;
    background: transparent;
    color: var(--muted);
    line-height: 1;
    padding: 0 0.5rem 0 0.1rem;
    cursor: pointer;
    transition: color var(--dur-fast) var(--ease-out);
  }
  .chip-del:hover {
    color: var(--danger);
  }
  .addflair {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
  }
  .addflair input {
    width: 8rem;
    padding: 0.32rem 0.75rem;
    border: 1px dashed var(--hairline-strong);
    border-radius: 999px;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    transition: border-color var(--dur) var(--ease-out);
    /* Adding a flair inserts a new .chip before .addflair, so this input gets
       relaid-out (shifted/wrapped) in the flex flow. Without its own layer,
       the rounded border's paint backing isn't fully invalidated at the old
       rect, so the :hover border ghosts at the pre-shift position. Promoting
       to a compositor layer makes the reflow reposition the input's paint
       atomically (no extra cost — one tiny static layer). */
    transform: translateZ(0);
  }
  .addflair input::placeholder {
    color: var(--faint);
  }
  .addflair input:hover,
  .addflair input:focus {
    border-color: var(--accent-line);
    border-style: solid;
    outline: none;
  }
  .addflair button {
    padding: 0.32rem 0.8rem;
    border: none;
    border-radius: 999px;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .addflair button:hover:not(:disabled) {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .addflair button:active:not(:disabled) {
    transform: scale(0.96);
  }
  .addflair button:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .clear-filters {
    border: none;
    background: transparent;
    color: var(--accent);
    font: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    cursor: pointer;
    padding: 0.32rem 0.5rem;
    border-radius: 999px;
  }
  .clear-filters:hover {
    background: var(--accent-soft);
  }

  /* ---- grid ---- */
  /* Fluid columns: scale the volume size with the viewport so wide screens
     get more books per row instead of wider gutters. */
  .grid {
    display: grid;
    grid-template-columns: repeat(
      auto-fill,
      minmax(clamp(148px, 10.5vw, 196px), 1fr)
    );
    gap: var(--sp-8) var(--sp-4);
  }
  @media (max-width: 768px) {
    .grid {
      grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
      gap: var(--sp-6) var(--sp-3);
    }
  }

  .state,
  .hint {
    color: var(--muted);
  }

  .noresults {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    margin-top: 12vh;
    text-align: center;
  }
  .noresults .state {
    margin: 0;
  }

  .error {
    color: var(--danger);
  }

  /* ---- empty shelf ---- */
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    margin-top: 14vh;
    text-align: center;
    animation: app-rise-in var(--dur-slower) var(--ease-out) both;
  }
  .empty-mark {
    font-size: var(--text-lg);
  }
  .empty-title {
    font-size: var(--text-2xl);
    font-style: italic;
    font-weight: 480;
    color: var(--fg);
    margin: 0;
  }
  .empty-sub {
    margin: 0;
    color: var(--muted);
  }
  .empty code {
    font-family: ui-monospace, monospace;
    font-size: 0.85em;
    background: var(--surface);
    padding: 0.1rem 0.35rem;
    border-radius: var(--radius-sm);
  }
</style>
