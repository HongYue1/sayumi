<script lang="ts">
  import { onMount } from "svelte";
  import { library, SORT_OPTIONS } from "~/lib/library.svelte";
  import { session } from "~/lib/session.svelte";
  import { settings } from "~/lib/settings.svelte";
  import { applyTheme } from "~/lib/theme";
  import { router } from "~/lib/router.svelte";
  import BookCard from "~/components/library/BookCard.svelte";
  import ThemeDropdown from "~/components/library/ThemeDropdown.svelte";

  import { DEFAULT_FLAIRS } from "~/lib/flairs";

  let fileInput = $state<HTMLInputElement | null>(null);
  let newFlair = $state("");
  // Drag-and-drop: a counter (not a bool) avoids flicker as the pointer crosses
  // child elements, since dragenter/dragleave fire per element.
  let dragDepth = $state(0);
  const dragging = $derived(dragDepth > 0);

  onMount(() => {
    library.load();
    // Reflect the profile's saved theme in the library (not just the reader).
    settings.load().then(() => applyTheme(settings.value.theme));
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
        <span class="who" title={session.profile ?? ""}>{session.profile}</span>
        <button class="signout" onclick={() => session.logout()}>Sign out</button>
      </div>
    </div>

    <div class="controls">
      <input
        class="search"
        type="search"
        placeholder="Search title or author…"
        bind:value={library.query}
        aria-label="Search library"
      />

      <select class="sort" bind:value={library.sort} aria-label="Sort by">
        {#each SORT_OPTIONS as opt (opt.key)}
          <option value={opt.key}>{opt.label}</option>
        {/each}
      </select>

      <button
        class="ghost-btn"
        onclick={() => library.rescan()}
        disabled={library.rescanning}
        title="Scan the Library folder for new files"
      >
        {library.rescanning ? "Scanning…" : "Rescan"}
      </button>

      <button
        class="upload"
        onclick={() => fileInput?.click()}
        disabled={library.uploading}
      >
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
        Your Library · {library.books.length}
        {library.books.length === 1 ? "book" : "books"}
      </p>

      <div class="flairbar">
        {#each library.allFlairs as f (f.id)}
          {@const active = library.flairFilters.includes(f.id)}
          <span class="chip" class:active style:--chip={f.color}>
            <button class="chip-toggle" onclick={() => library.toggleFlairFilter(f.id)}>
              <span class="dot" style:background={f.color}></span>
              {f.label}
            </button>
            {#if isCustom(f.id)}
              <button
                class="chip-del"
                title="Delete flair"
                aria-label={`Delete flair ${f.label}`}
                onclick={() => library.removeCustomFlair(f.id)}
              >×</button>
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
          <button class="clear-filters" onclick={() => library.clearFlairFilters()}>Clear</button>
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
      <button class="upload" onclick={() => fileInput?.click()} disabled={library.uploading}>
        {library.uploading ? "Uploading…" : "Add your first book"}
      </button>
      <p class="hint">…or drop .epub files into your <code>Library</code> folder.</p>
    </div>
  {:else if library.visible.length === 0}
    <p class="state">No books match “{library.query}”.</p>
  {:else}
    <div class="grid">
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

  {#if dragging}
    <div class="dropzone" aria-hidden="true">
      <div class="dropzone-inner">
        <span class="dropzone-mark">＋</span>
        <p>Drop .epub files to add them</p>
      </div>
    </div>
  {/if}
</div>

<style>
  .library {
    position: relative;
    min-height: 100vh;
    padding: var(--sp-8) var(--sp-8) var(--sp-12);
    max-width: 1180px;
    margin: 0 auto;
  }

  .dropzone {
    position: fixed;
    inset: 0;
    z-index: 40;
    display: grid;
    place-items: center;
    padding: 1.5rem;
    background: color-mix(in srgb, var(--bg) 78%, transparent);
    backdrop-filter: blur(2px);
    animation: dz-in 0.12s var(--ease);
  }
  @keyframes dz-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  .dropzone-inner {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--sp-3);
    width: min(36rem, 90%);
    padding: var(--sp-12) var(--sp-8);
    border: 2px dashed color-mix(in srgb, var(--accent) 60%, transparent);
    border-radius: 1rem;
    color: var(--fg);
    text-align: center;
  }
  .dropzone-mark {
    font-size: 2.6rem;
    line-height: 1;
    color: var(--accent);
  }
  .dropzone-inner p {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-lg);
  }

  /* ---- masthead ---- */
  .masthead {
    padding-bottom: var(--sp-4);
    border-bottom: 1px solid var(--hairline);
    margin-bottom: var(--sp-6);
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

  .controls {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    flex-wrap: wrap;
  }
  .search {
    flex: 1;
    min-width: 12rem;
    padding: 0.5rem 0.8rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
  }
  .search::placeholder {
    color: var(--muted);
  }
  .search:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  .sort {
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
  }
  .upload {
    padding: 0.5rem 1rem;
    border: none;
    border-radius: var(--radius);
    background: var(--accent);
    color: #fff;
    font: inherit;
    font-weight: 700;
    cursor: pointer;
    transition: opacity 0.15s var(--ease);
  }
  .upload:hover:not(:disabled) {
    opacity: 0.88;
  }
  .upload:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .profile {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-shrink: 0;
  }

  .ghost-btn {
    padding: 0.5rem 0.8rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition: background 0.15s var(--ease);
  }
  .ghost-btn:hover:not(:disabled) {
    background: var(--surface-hover);
  }
  .ghost-btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }
  .who {
    color: var(--muted);
    font-size: var(--text-sm);
    max-width: 10rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .signout {
    border: none;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: var(--text-sm);
    padding: 0.2rem 0;
    cursor: pointer;
    border-bottom: 1px solid transparent;
    transition: color 0.15s var(--ease), border-color 0.15s var(--ease);
  }
  .signout:hover {
    color: var(--fg);
    border-bottom-color: var(--accent);
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
    gap: 0.4rem;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    overflow: hidden;
    transition: border-color 0.15s var(--ease), background 0.15s var(--ease);
  }
  .chip.active {
    border-color: var(--chip);
    background: color-mix(in srgb, var(--chip) 16%, transparent);
  }
  .chip-toggle {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.25rem 0.65rem;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    letter-spacing: 0.02em;
    cursor: pointer;
  }
  .chip .dot {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .chip-del {
    border: none;
    background: transparent;
    color: var(--muted);
    font-size: 0.9rem;
    line-height: 1;
    padding: 0 0.45rem 0 0.1rem;
    cursor: pointer;
  }
  .chip-del:hover {
    color: #b3402f;
  }
  .addflair {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
  }
  .addflair input {
    width: 8rem;
    padding: 0.3rem 0.6rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
  }
  .addflair input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  .addflair button {
    padding: 0.3rem 0.7rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 999px;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-xs);
    cursor: pointer;
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
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: var(--sp-8) var(--sp-4);
  }

  .state,
  .hint {
    color: var(--muted);
  }

  .error {
    color: #b3402f;
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
  .empty code {
    font-family: ui-monospace, monospace;
    background: var(--surface);
    padding: 0.1rem 0.3rem;
    border-radius: 0.25rem;
  }
</style>
