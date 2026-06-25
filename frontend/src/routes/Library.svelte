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
  import Icon from "~/lib/Icon.svelte";
  import { Plus, RefreshCw, ArrowUpDown, Check, X } from "@lucide/svelte";

  import { DEFAULT_FLAIRS } from "~/lib/flairs";

  let fileInput = $state<HTMLInputElement | null>(null);
  let newFlair = $state("");
  // Drag-and-drop: a counter (not a bool) avoids flicker as the pointer crosses
  // child elements, since dragenter/dragleave fire per element.
  let dragDepth = $state(0);
  const dragging = $derived(dragDepth > 0);

  // Which profile dialog (if any) is open. Rendered at .library level — never
  // inside the masthead, whose backdrop-filter would clip a fixed overlay to
  // the masthead box.
  let profileDialog = $state<"clone" | "delete" | null>(null);

  onMount(() => {
    library.load();
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

<div
  class="library"
  ondragenter={onDragEnter}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  role="region"
  aria-label="Library"
>
  <header class="masthead">
    <div class="brand-row">
      <h1 class="brand">Sayumi</h1>
      <div class="profile">
        <ThemeDropdown />
        <span class="profile-divider" aria-hidden="true"></span>
        <ProfileMenu
          onclone={() => (profileDialog = "clone")}
          ondelete={() => (profileDialog = "delete")}
        />
      </div>
    </div>

    <div class="controls">
      <input
        class="search"
        type="search"
        placeholder="Search title or author…"
        value={library.query}
        oninput={(e) => library.setQuery(e.currentTarget.value)}
        aria-label="Search library"
      />

      <div class="select-wrap">
        <Icon icon={ArrowUpDown} size={16} class="select-icon" />
        <select class="sort" bind:value={library.sort} aria-label="Sort by">
          {#each SORT_OPTIONS as opt (opt.key)}
            <option value={opt.key}>{opt.label}</option>
          {/each}
        </select>
      </div>

      <button
        class="ghost-btn"
        onclick={() => library.rescan()}
        disabled={library.rescanning}
        title="Scan the Library folder for new files"
      >
        <Icon
          icon={RefreshCw}
          size={16}
          class={library.rescanning ? "spin" : ""}
        />
        {library.rescanning ? "Scanning…" : "Rescan"}
      </button>

      <button
        class="upload"
        onclick={() => fileInput?.click()}
        disabled={library.uploading}
      >
        <Icon icon={Plus} size={16} />
        {library.uploading ? "Uploading…" : "Add book"}
      </button>
      <input
        bind:this={fileInput}
        type="file"
        accept=".epub,application/epub+zip"
        multiple
        hidden
        onchange={onFilePicked}
      />
    </div>
  </header>

  {#if library.books.length > 0}
    <div class="browsebar">
      <p class="eyebrow">
        Your Library · <span class="tnum">{library.books.length}</span>
        {library.books.length === 1 ? "book" : "books"}
      </p>

      <div class="flairbar">
        {#each library.allFlairs as f (f.id)}
          {@const active = library.flairFilters.includes(f.id)}
          <span class="chip" class:active style:--chip={f.color}>
            <button
              class="chip-toggle"
              aria-pressed={active}
              onclick={() => library.toggleFlairFilter(f.id)}
            >
              {#if active}
                <span
                  class="chip-check"
                  style:color={f.color}
                  aria-hidden="true"><Icon icon={Check} size={14} /></span
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
    </div>
  {/if}

  {#if library.error}
    <p class="error" role="alert">{library.error}</p>
  {/if}

  {#if library.loading}
    <p class="state">Loading…</p>
  {:else if library.books.length === 0}
    <div class="empty">
      <p>Your library is empty.</p>
      <button
        class="upload"
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
    <div class="noresults">
      <p class="state">No books match your search or filters.</p>
      <button
        class="clear-filters"
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

  {#if dragging}
    <div class="dropzone" aria-hidden="true">
      <div class="dropzone-inner">
        <span class="dropzone-mark"><Icon icon={Plus} size={40} /></span>
        <p>Drop .epub files to add them</p>
      </div>
    </div>
  {/if}
</div>

<style>
  .library {
    position: relative;
    min-height: calc(100vh - var(--offline-banner-h, 0px));
    padding: var(--sp-8) var(--sp-8) var(--sp-12);
    max-width: 1440px;
    margin: 0 auto;
  }

  .dropzone {
    position: fixed;
    inset: 0;
    z-index: 40;
    display: grid;
    place-items: center;
    padding: var(--sp-6);
    background: color-mix(in srgb, var(--bg) 78%, transparent);
    backdrop-filter: blur(2px);
    animation: app-overlay-in var(--dur-fast) var(--ease-out);
  }
  .dropzone-inner {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    width: min(36rem, 90%);
    padding: var(--sp-12) var(--sp-8);
    border: 2px dashed color-mix(in srgb, var(--accent) 60%, transparent);
    border-radius: var(--radius-lg);
    color: var(--fg);
    text-align: center;
  }
  .dropzone-mark {
    display: inline-flex;
    color: var(--accent);
  }
  .dropzone-inner p {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-lg);
  }

  /* ---- masthead ---- */
  /* Sticky so search/sort/flairs stay reachable while scrolling a long shelf.
     The translucent fill + backdrop blur only reads once content scrolls
     underneath; the hairline doubles as the divider when pinned. backdrop-filter
     is a hover/scroll-time effect (not a load-time cost), so the Lighthouse
     budget is untouched. */
  .masthead {
    position: sticky;
    top: 0;
    z-index: 30;
    padding-top: var(--sp-4);
    padding-bottom: var(--sp-4);
    margin-bottom: var(--sp-6);
    background: color-mix(in srgb, var(--bg) 82%, transparent);
    -webkit-backdrop-filter: blur(8px);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--hairline);
  }
  .brand-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--sp-4);
    margin-bottom: var(--sp-4);
  }
  .brand {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-3xl);
    font-weight: 500;
    line-height: 1;
    letter-spacing: 0.01em;
  }

  /* A shared control height keeps the search, sort, and buttons on one baseline. */
  .controls {
    --control-h: 2.4rem;
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    flex-wrap: wrap;
  }
  .search {
    flex: 1;
    min-width: 12rem;
    height: var(--control-h);
    padding: 0 0.8rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    transition: border-color var(--dur) var(--ease-out);
  }
  .search::placeholder {
    color: var(--muted);
  }
  .search:hover {
    border-color: var(--accent);
  }

  .select-wrap {
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .select-wrap :global(.select-icon) {
    position: absolute;
    left: 0.6rem;
    color: var(--muted);
    pointer-events: none;
  }
  .sort {
    height: var(--control-h);
    padding: 0 0.6rem 0 2rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition: border-color var(--dur) var(--ease-out);
  }
  .sort:hover {
    border-color: var(--accent);
  }

  /* Buttons share the control height and gain an icon + label row. */
  .ghost-btn,
  .upload {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
    height: var(--control-h);
    border-radius: var(--radius);
    font: inherit;
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      opacity var(--dur) var(--ease-out),
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .ghost-btn:active:not(:disabled),
  .upload:active:not(:disabled) {
    transform: scale(0.97);
  }
  .upload {
    padding: 0 var(--sp-4);
    border: none;
    background: var(--accent);
    color: var(--accent-fg);
    font-weight: 700;
  }
  .upload:hover:not(:disabled) {
    opacity: 0.88;
  }
  .upload:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  .ghost-btn {
    padding: 0 0.8rem;
    border: 1px solid var(--hairline-strong);
    background: transparent;
    color: var(--fg);
  }
  .ghost-btn:hover:not(:disabled) {
    background: var(--surface-hover);
    border-color: var(--accent);
  }
  .ghost-btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  /* Spin the rescan glyph while a scan is in flight. */
  .ghost-btn :global(.spin) {
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .profile {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    flex-shrink: 0;
  }
  .profile-divider {
    width: 1px;
    height: 1.4rem;
    background: var(--hairline-strong);
  }
  /* ---- browse bar (label + flairs) ---- */
  .browsebar {
    margin-bottom: var(--sp-6);
  }
  .browsebar .eyebrow {
    margin: 0 0 var(--sp-3);
  }
  .flairbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--sp-2);
  }
  .chip {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    overflow: hidden;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur) var(--ease-out);
  }
  /* Selected chips read as selected by fill + a check (not colour alone). */
  .chip.active {
    border-color: var(--chip);
    background: color-mix(in srgb, var(--chip) 16%, transparent);
  }
  .chip-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.3rem 0.7rem;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    letter-spacing: 0.02em;
    cursor: pointer;
    transition: transform var(--dur-fast) var(--ease-out);
  }
  .chip-toggle:active {
    transform: scale(0.96);
  }
  .chip .dot {
    width: 0.55rem;
    height: 0.55rem;
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
    padding: 0 0.45rem 0 0.1rem;
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
    padding: 0.35rem 0.7rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    background: var(--bg);
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
  .addflair input:hover {
    border-color: var(--accent);
  }
  .addflair button {
    padding: 0.35rem 0.8rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .addflair button:hover:not(:disabled) {
    background: var(--surface-hover);
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
    cursor: pointer;
  }

  /* ---- grid ---- */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(176px, 1fr));
    gap: var(--sp-8) var(--sp-4);
  }
  @media (max-width: 768px) {
    .grid {
      grid-template-columns: repeat(auto-fill, minmax(128px, 1fr));
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
    gap: var(--sp-2);
    margin-top: 12vh;
    text-align: center;
  }
  .clear-filters {
    padding: 0.45rem 0.9rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition: border-color var(--dur) var(--ease-out);
  }
  .clear-filters:hover {
    border-color: var(--accent);
    color: var(--accent);
  }

  .error {
    color: var(--danger);
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    margin-top: 16vh;
    text-align: center;
  }
  .empty p:first-child {
    font-family: var(--font-display);
    font-size: var(--text-xl);
    color: var(--fg);
    margin: 0;
  }
  .empty .upload {
    padding: 0 1.1rem;
  }
  .empty code {
    font-family: ui-monospace, monospace;
    background: var(--surface);
    padding: 0.1rem 0.3rem;
    border-radius: var(--radius-sm);
  }
</style>
