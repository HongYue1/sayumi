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
  import { Plus, RefreshCw, ArrowUpDown, Check, X } from "@lucide/svelte";

  import { DEFAULT_FLAIRS } from "~/lib/flairs";

  let fileInput = $state<HTMLInputElement | null>(null);
  let newFlair = $state("");
  // Drag-and-drop: a counter (not a bool) avoids flicker as the pointer crosses
  // child elements, since dragenter/dragleave fire per element.
  let dragDepth = $state(0);
  const dragging = $derived(dragDepth > 0);

  // Which profile dialog (if any) is open. Rendered at .library level — never
  // inside the toolbar, whose backdrop-filter would clip a fixed overlay to
  // the toolbar box.
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

<div
  class="library"
  ondragenter={onDragEnter}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  role="region"
  aria-label="Library"
>
  <!-- Masthead — the journal front page. Not sticky; the toolbar below pins. -->
  <header class="masthead">
    <div class="meta-row">
      <p class="eyebrow count">
        {#if library.books.length > 0}
          Your library · <span class="tnum">{library.books.length}</span>
          {library.books.length === 1 ? "book" : "books"}
        {:else}
          A quiet place to read
        {/if}
      </p>
      <div class="profile">
        <ThemeDropdown />
        <span class="profile-divider" aria-hidden="true"></span>
        <ProfileMenu
          onclone={() => (profileDialog = "clone")}
          ondelete={() => (profileDialog = "delete")}
        />
      </div>
    </div>

    <h1 class="brand display">
      Sayumi<span class="brand-mark" aria-hidden="true"> ❦</span>
    </h1>

    <hr class="rule-double" />
  </header>

  <!-- Pinned toolbar: search / sort / scan / add stay reachable on long shelves. -->
  <div class="toolbar">
    <input
      class="field search"
      type="search"
      placeholder="Search title or author…"
      value={library.query}
      oninput={(e) => library.setQuery(e.currentTarget.value)}
      aria-label="Search library"
    />

    <div class="select-wrap">
      <Icon icon={ArrowUpDown} size={15} class="select-icon" />
      <select class="sort" bind:value={library.sort} aria-label="Sort by">
        {#each SORT_OPTIONS as opt (opt.key)}
          <option value={opt.key}>{opt.label}</option>
        {/each}
      </select>
    </div>

    <button
      class="btn-ghost press rescan"
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
      class="btn press upload"
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

  {#if library.books.length > 0}
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
    position: relative;
    min-height: calc(100vh - var(--offline-banner-h, 0px));
    padding: var(--sp-6) clamp(var(--sp-4), 4vw, var(--sp-10)) var(--sp-16);
    max-width: 1480px;
    margin: 0 auto;
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

  /* ---- masthead ---- */
  .masthead {
    /* The entrance animation's retained transform makes the masthead a
       stacking context at z:0 — later siblings (toolbar z:30, card contexts)
       would paint over its dropdown menus (theme/profile). Raise the whole
       masthead above them; it never visually overlaps the pinned toolbar
       (it has scrolled away by the time the toolbar sticks). */
    position: relative;
    z-index: 40;
    padding-top: var(--sp-2);
    animation: app-rise-in var(--dur-slower) var(--ease-out) both;
  }
  .meta-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-4);
    min-height: 2.5rem;
  }
  .count {
    margin: 0;
  }
  .brand {
    margin: var(--sp-1) 0 var(--sp-5);
    font-size: clamp(2.9rem, 6.5vw, 4.2rem);
    font-style: italic;
    font-weight: 460;
    line-height: 1;
  }
  .brand-mark {
    font-size: 0.4em;
    font-style: normal;
    color: var(--faint);
    vertical-align: 0.5em;
    letter-spacing: 0;
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

  /* ---- pinned toolbar ---- */
  /* Sticky so search/sort stay reachable while scrolling a long shelf. The
     translucent fill + backdrop blur only reads once covers slide underneath.
     backdrop-filter is a scroll-time effect, not a load-time cost. */
  .toolbar {
    --control-h: 2.5rem;
    position: sticky;
    top: 0;
    z-index: 30;
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    flex-wrap: wrap;
    padding: var(--sp-3) 0;
    margin-bottom: var(--sp-4);
    background: color-mix(in srgb, var(--bg) 84%, transparent);
    -webkit-backdrop-filter: blur(10px);
    backdrop-filter: blur(10px);
    border-bottom: 1px solid var(--hairline);
    animation: app-rise-in var(--dur-slower) var(--ease-out) 60ms both;
  }
  .search {
    flex: 1;
    min-width: 12rem;
    height: var(--control-h);
  }

  .select-wrap {
    position: relative;
    display: inline-flex;
    align-items: center;
  }
  .select-wrap :global(.select-icon) {
    position: absolute;
    left: 0.65rem;
    color: var(--muted);
    pointer-events: none;
  }
  .sort {
    height: var(--control-h);
    padding: 0 0.7rem 0 2rem;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 540;
    cursor: pointer;
    appearance: none;
    transition:
      background var(--dur-fast) var(--ease-out),
      border-color var(--dur-fast) var(--ease-out);
  }
  .sort:hover {
    background: var(--surface-hover);
  }
  .sort:focus-visible {
    border-color: var(--accent-line);
  }

  .rescan,
  .upload {
    height: var(--control-h);
  }
  /* Spin the rescan glyph while a scan is in flight. */
  .rescan :global(.spin) {
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* ---- flair chips ---- */
  .flairbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--sp-2);
    margin-bottom: var(--sp-8);
    animation: app-rise-in var(--dur-slower) var(--ease-out) 120ms both;
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
    padding: 0.32rem 0.75rem;
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
    padding: 0.35rem 0.75rem;
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
    padding: 0.35rem 0.8rem;
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
    padding: 0.35rem 0.5rem;
    border-radius: 999px;
  }
  .clear-filters:hover {
    background: var(--accent-soft);
  }

  /* ---- grid ---- */
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(178px, 1fr));
    gap: var(--sp-10) var(--sp-5);
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
