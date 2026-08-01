// Library route: the shelf — Solid 2.0 port.
// (Solid notes: onMount → onSettled; the conditional svelte:window pointerdown
// listener becomes a compute→apply effect with a cleanup; the sort menu reuses
// the BookCard/ThemeDropdown roving-focus menuitemradio pattern; `library` is
// the batch-2 store class — getters are reactive, `sort` has a setter.)
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onSettled,
  Show,
} from "solid-js";
import { library, SORT_OPTIONS, type SortKey } from "~/lib/library";
import { session } from "~/lib/session";
import { settings } from "~/lib/settings";
import { applyTheme } from "~/lib/theme";
import { router } from "~/lib/router";
import BookCard from "~/components/library/BookCard";
import ThemeDropdown from "~/components/library/ThemeDropdown";
import ProfileMenu from "~/components/library/ProfileMenu";
import ProfileDialog from "~/components/library/ProfileDialog";
import EditBookDialog from "~/components/library/EditBookDialog";
import ShareDialog from "~/components/library/ShareDialog";
import Icon from "~/lib/Icon";
import {
  ArrowUpDown,
  Check,
  ChevronDown,
  Plus,
  RefreshCw,
  X,
} from "~/lib/icons";
import { DEFAULT_FLAIRS } from "~/lib/flairs";

function hasFiles(e: DragEvent): boolean {
  return Array.from(e.dataTransfer?.types ?? []).includes("Files");
}

function isCustomFlair(id: string): boolean {
  return !DEFAULT_FLAIRS.some((f) => f.id === id);
}

function openBook(id: string): void {
  router.navigate(`/read/${encodeURIComponent(id)}`);
}

async function onFilePicked(
  e: Event & { currentTarget: HTMLInputElement },
): Promise<void> {
  const input = e.currentTarget;
  const files = Array.from(input.files ?? []);
  if (files.length) await library.uploadFiles(files);
  input.value = ""; // allow re-uploading the same file
}

function onDragOver(e: DragEvent): void {
  if (hasFiles(e)) e.preventDefault(); // allow drop
}

export default function Library() {
  let fileInput: HTMLInputElement | undefined;
  const [newFlair, setNewFlair] = createSignal("");
  // Drag-and-drop: a counter (not a bool) avoids flicker as the pointer crosses
  // child elements, since dragenter/dragleave fire per element.
  const [dragDepth, setDragDepth] = createSignal(0);
  const dragging = createMemo(() => dragDepth() > 0);

  // Which profile dialog (if any) is open. Rendered at page level — never
  // inside the command bar, whose backdrop-filter would clip a fixed overlay
  // to the bar box.
  const [profileDialog, setProfileDialog] = createSignal<
    "clone" | "delete" | null
  >(null);

  // The book currently being edited, tracked by id so the open dialog reflects
  // live store updates (and auto-closes if the book is removed) rather than
  // pinning a stale snapshot.
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const editingBook = createMemo(() => {
    const id = editingId();
    return id ? (library.books.find((b) => b.id === id) ?? null) : null;
  });

  // Same id-tracked pattern for the share dialog so it reflects live store
  // updates and auto-closes if the book is removed.
  const [sharingId, setSharingId] = createSignal<string | null>(null);
  const sharingBook = createMemo(() => {
    const id = sharingId();
    return id ? (library.books.find((b) => b.id === id) ?? null) : null;
  });

  // ---- custom sort menu (native <select> popups can't be themed) ----------
  const [sortOpen, setSortOpen] = createSignal(false);
  let sortTrigger: HTMLButtonElement | undefined;
  let sortMenuEl: HTMLDivElement | undefined;
  const sortLabel = createMemo(
    () => SORT_OPTIONS.find((o) => o.key === library.sort)?.label ?? "Sort",
  );

  function closeSort(restoreFocus = true): void {
    if (!sortOpen()) return;
    setSortOpen(false);
    if (restoreFocus) sortTrigger?.focus();
  }

  function chooseSort(key: SortKey): void {
    library.sort = key;
    closeSort();
  }

  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the
  // sticky bar's backdrop-filter establishes a containing block, which would
  // clip it to the bar box. A window listener is container-proof, matching
  // ThemeDropdown / ProfileMenu / BookCard.
  function onSortOutside(e: PointerEvent): void {
    const t = e.target;
    if (
      sortMenuEl?.contains(t as Node | null) ||
      sortTrigger?.contains(t as Node | null)
    )
      return;
    closeSort(false);
  }

  function onSortKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
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
    const menu = e.currentTarget;
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>(".lib-sort-item"),
    );
    if (items.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const cur =
      document.activeElement instanceof HTMLButtonElement
        ? items.indexOf(document.activeElement)
        : -1;
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

  onSettled(() => {
    // Activate and load together: child effects can run before App's profile
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

  function onDragEnter(e: DragEvent): void {
    if (!hasFiles(e)) return;
    e.preventDefault();
    setDragDepth(dragDepth() + 1);
  }

  function onDragLeave(): void {
    if (dragDepth() > 0) setDragDepth(dragDepth() - 1);
  }

  async function onDrop(e: DragEvent): Promise<void> {
    if (!hasFiles(e)) return;
    e.preventDefault();
    setDragDepth(0);
    const files = Array.from(e.dataTransfer?.files ?? []);
    if (files.length) await library.uploadFiles(files);
  }

  async function addFlair(): Promise<void> {
    const name = newFlair().trim();
    if (!name) return;
    await library.addCustomFlair(name);
    setNewFlair("");
  }

  // Replaces <svelte:window onpointerdown={sortOpen ? onSortOutside : undefined}>:
  // the listener lives only while the menu is open.
  createEffect(() => {
    if (!sortOpen()) return undefined;
    window.addEventListener("pointerdown", onSortOutside);
    return () => window.removeEventListener("pointerdown", onSortOutside);
  });

  return (
    <div
      class="lib-page"
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      role="region"
      aria-label="Library"
    >
      {/* One compact command bar: identity + every library control in a single
			     pinned row, so the shelf owns the screen. */}
      <header class="lib-bar">
        <h1 class="lib-lockup">
          <span class="wordmark">Sayumi</span>
          <span class="lib-lockup-mark" aria-hidden="true">
            ❦
          </span>
        </h1>

        <input
          class="field lib-search"
          type="search"
          placeholder="Search title or author…"
          value={library.query}
          onInput={(e) => library.setQuery(e.currentTarget.value)}
          aria-label="Search library"
        />

        <div class="lib-sort-dd">
          <button
            type="button"
            ref={sortTrigger}
            class={[
              "btn-ghost press lib-sort-trigger",
              sortOpen() ? "open" : "",
            ]}
            aria-haspopup="menu"
            aria-expanded={sortOpen() ? "true" : "false"}
            aria-label={`Sort by (current: ${sortLabel()})`}
            onClick={() => setSortOpen(!sortOpen())}
          >
            <Icon icon={ArrowUpDown} size={15} />
            <span class="lib-sort-label">{sortLabel()}</span>
            <Icon icon={ChevronDown} size={14} class="caret" />
          </button>
          <Show when={sortOpen()}>
            <div
              ref={sortMenuEl}
              class="lib-sort-menu paper"
              role="menu"
              tabindex="-1"
              aria-label="Sort by"
              onKeyDown={onSortKeydown}
            >
              <For each={SORT_OPTIONS}>
                {(opt) => {
                  const active = () => library.sort === opt.key;
                  return (
                    <button
                      type="button"
                      class={["lib-sort-item", active() ? "active" : ""]}
                      role="menuitemradio"
                      aria-checked={active() ? "true" : "false"}
                      tabindex={active() ? "0" : "-1"}
                      ref={(el) => {
                        // Focus the current sort on open (menuitemradio model).
                        if (active()) el.focus();
                      }}
                      onClick={() => chooseSort(opt.key)}
                    >
                      <span class="lib-sort-item-label">{opt.label}</span>
                      <Show when={active()}>
                        <span class="lib-check" aria-hidden="true">
                          <Icon icon={Check} size={15} />
                        </span>
                      </Show>
                    </button>
                  );
                }}
              </For>
            </div>
          </Show>
        </div>

        <button
          type="button"
          class={["icon-btn press", library.rescanning ? "active" : ""]}
          onClick={() => library.rescan()}
          disabled={library.rescanning}
          aria-label={
            library.rescanning
              ? "Scanning library folder…"
              : "Rescan the Library folder for new files"
          }
          title="Scan the Library folder for new files"
        >
          <Icon
            icon={RefreshCw}
            size={17}
            class={library.rescanning ? "spin" : ""}
          />
        </button>

        <button
          type="button"
          class="btn press lib-upload"
          onClick={() => fileInput?.click()}
          disabled={library.uploading}
        >
          <Icon icon={Plus} size={16} />
          <span class="lib-upload-label">
            {library.uploading ? "Uploading…" : "Add book"}
          </span>
        </button>
        <input
          ref={fileInput}
          type="file"
          accept=".epub,application/epub+zip"
          multiple
          hidden
          onChange={onFilePicked}
        />

        <span class="lib-bar-divider" aria-hidden="true" />
        <ThemeDropdown />
        <ProfileMenu
          onclone={() => setProfileDialog("clone")}
          ondelete={() => setProfileDialog("delete")}
        />
      </header>

      <Show when={library.books.length > 0}>
        <div class="lib-flairbar">
          <p class="eyebrow lib-count">
            <span class="tnum">{library.books.length}</span>{" "}
            {library.books.length === 1 ? "book" : "books"}
          </p>
          <span class="lib-flair-divider" aria-hidden="true" />
          <For each={library.allFlairs}>
            {(f) => {
              const active = () => library.flairFilters.includes(f.id);
              return (
                <span
                  class={["lib-chip", active() ? "active" : ""]}
                  style={{ "--chip": f.color }}
                >
                  <button
                    type="button"
                    class="lib-chip-toggle"
                    aria-pressed={active() ? "true" : "false"}
                    onClick={() => library.toggleFlairFilter(f.id)}
                  >
                    <Show
                      when={active()}
                      fallback={
                        <span class="lib-dot" style={{ background: f.color }} />
                      }
                    >
                      <span
                        class="lib-chip-check"
                        style={{ color: f.color }}
                        aria-hidden="true"
                      >
                        <Icon icon={Check} size={13} />
                      </span>
                    </Show>
                    {f.label}
                  </button>
                  <Show when={isCustomFlair(f.id)}>
                    <button
                      type="button"
                      class="lib-chip-del"
                      title="Delete flair"
                      aria-label={`Delete flair ${f.label}`}
                      onClick={() => library.removeCustomFlair(f.id)}
                    >
                      <Icon icon={X} size={13} />
                    </button>
                  </Show>
                </span>
              );
            }}
          </For>

          <span class="lib-addflair">
            <input
              type="text"
              placeholder="New flair…"
              maxlength="40"
              value={newFlair()}
              onInput={(e) => setNewFlair(e.currentTarget.value)}
              onKeyDown={(e) => e.key === "Enter" && addFlair()}
              aria-label="New flair name"
            />
            <button
              type="button"
              onClick={addFlair}
              disabled={!newFlair().trim()}
            >
              Add
            </button>
          </span>

          <Show when={library.flairFilters.length > 0}>
            <button
              type="button"
              class="lib-clear-filters"
              onClick={() => library.clearFlairFilters()}
            >
              Clear
            </button>
          </Show>
        </div>
      </Show>

      <Show when={library.error}>
        <p class="lib-error" role="alert">
          {library.error}
        </p>
      </Show>

      {library.loading ? (
        <p class="lib-state" role="status">
          Loading…
        </p>
      ) : library.books.length === 0 ? (
        <div class="lib-empty">
          <span class="fleuron lib-empty-mark" aria-hidden="true">
            ❦
          </span>
          <p class="lib-empty-title display">An empty shelf.</p>
          <p class="lib-empty-sub">Every library starts with a single book.</p>
          <button
            type="button"
            class="btn press"
            onClick={() => fileInput?.click()}
            disabled={library.uploading}
          >
            <Icon icon={Plus} size={16} />
            {library.uploading ? "Uploading…" : "Add your first book"}
          </button>
          <p class="lib-hint">
            …or drop .epub files into your <code>Library</code> folder.
          </p>
        </div>
      ) : library.visible.length === 0 ? (
        <div class="lib-noresults" role="status">
          <p class="lib-empty-title display">Nothing on this shelf.</p>
          <p class="lib-state">No books match your search or filters.</p>
          <button
            type="button"
            class="btn-ghost press"
            onClick={() => {
              library.setQuery("");
              library.clearFlairFilters();
            }}
          >
            Clear search & filters
          </button>
        </div>
      ) : (
        <div class="lib-grid" role="list">
          <For each={library.visible}>
            {(book, i) => (
              <BookCard
                book={book}
                index={i()}
                flairs={library.allFlairs}
                onopen={openBook}
                onremove={(id) => library.remove(id)}
                onedit={(id) => setEditingId(id)}
                onshare={(id) => setSharingId(id)}
                onsetflair={(id, flairId) => library.setFlair(id, flairId)}
              />
            )}
          </For>
        </div>
      )}

      <Show when={session.profile}>
        {(profile) => (
          <Show when={profileDialog()}>
            {(mode) => (
              <ProfileDialog
                mode={mode()}
                profileName={profile()}
                onclose={() => setProfileDialog(null)}
              />
            )}
          </Show>
        )}
      </Show>

      <Show when={editingBook()}>
        {(book) => (
          <EditBookDialog book={book()} onclose={() => setEditingId(null)} />
        )}
      </Show>

      <Show when={sharingBook()}>
        {(book) => (
          <ShareDialog book={book()} onclose={() => setSharingId(null)} />
        )}
      </Show>

      <Show when={dragging()}>
        <div class="lib-dropzone" aria-hidden="true">
          <div class="lib-dropzone-inner">
            <span class="lib-dropzone-mark">
              <Icon icon={Plus} size={36} />
            </span>
            <p class="display">Add to your library</p>
            <span class="lib-dropzone-sub">Drop .epub files anywhere</span>
          </div>
        </div>
      </Show>
    </div>
  );
}
