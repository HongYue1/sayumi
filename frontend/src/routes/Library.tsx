// Library route: the shelf. The `library` store's getters are reactive and
// `sort` is a settable property; the sort menu reuses the BookCard /
// ThemeDropdown roving-focus menuitemradio pattern.
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onSettled,
  Show,
} from "solid-js";
import type { FlairDef } from "~/api/client";
import { library, SORT_OPTIONS, type SortKey } from "~/lib/library";
import { session } from "~/lib/session";
import { settings } from "~/lib/settings";
import { router } from "~/lib/router";
import BookCard from "~/components/library/BookCard";
import CardSizeControl from "~/components/library/CardSizeControl";
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
import { cardSizeCss } from "~/lib/cardSize";

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
  // Non-reactive mirror of that counter, same doctrine as library.ts's
  // #uploadingPlain / #rescanningPlain. Solid batches writes, so `dragDepth()`
  // read immediately after a set still returns the pre-write value.
  // dragenter/dragleave arrive in bursts as the pointer crosses child elements
  // and can land inside one flush window, where a read-modify-write through the
  // accessor loses increments. The counter then never returns to zero and
  // .lib-dropzone -- position:fixed, inset:0 -- stays mounted over the entire
  // viewport until reload. Control flow reads this field; the signal only
  // drives the <Show>.
  let depth = 0;
  // Single consumer (the dropzone <Show>), so a memo only adds a node to the
  // graph; the comparison it caches is cheaper than the node itself.
  const dragging = () => dragDepth() > 0;

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
  // clip it to the bar box. A window listener is container-proof — and the
  // pass-through doctrine (an outside click closes the menu AND lands on its
  // target) is shared with every menu: ThemeDropdown, ProfileMenu, the
  // reader's more menu, and BookCard (whose only swallow is its own
  // open-book overlay).
  function onSortOutside(e: PointerEvent): void {
    // Narrow, don't cast -- ThemeDropdown's onOutside uses exactly this
    // guard. The
    // cast form lied to the compiler: a non-Element target (shadow-DOM
    // retargeting, or a synthetic event with no target) made both contains()
    // calls return false, so this menu treated it as an outside click and
    // closed while every peer menu stayed open. The parity the comment above
    // claims was real for the listener and false for the hit test.
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (sortMenuEl?.contains(t) || sortTrigger?.contains(t)) return;
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
    if (e.key === "Tab") {
      // Tab must LEAVE the menu, never wrap inside it. WCAG 2.1.2 (No Keyboard
      // Trap) and the APG Menu Button pattern both require it, and this popover
      // has no visible close control -- containment left keyboard and switch
      // users with Escape as the only exit, which is not discoverable. Close
      // and let the browser continue the tab order from the restored trigger.
      closeSort();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End"
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

    // Match the App activation even if this child settles first after refresh.
    void settings.activate(session.profile);

    // A drag that ends outside the window -- dropped on another application, or
    // cancelled with Escape -- fires neither dragleave nor drop, so the depth
    // counter never unwinds and the full-viewport dropzone stays up.
    window.addEventListener("dragend", resetDrag);
    window.addEventListener("drop", resetDrag);

    // onCleanup() is forbidden inside onSettled's callback
    // (CLEANUP_IN_FORBIDDEN_SCOPE); returning the teardown is the sanctioned
    // form, and it is what makes the promise above owned rather than orphaned.
    return () => {
      window.removeEventListener("dragend", resetDrag);
      window.removeEventListener("drop", resetDrag);
    };
  });

  function onDragEnter(e: DragEvent): void {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth += 1;
    setDragDepth(depth);
  }

  function onDragLeave(): void {
    if (depth === 0) return;
    depth -= 1;
    setDragDepth(depth);
  }

  function resetDrag(): void {
    if (depth === 0) return;
    depth = 0;
    setDragDepth(0);
  }

  async function onDrop(e: DragEvent): Promise<void> {
    if (!hasFiles(e)) return;
    e.preventDefault();
    resetDrag();
    const files = Array.from(e.dataTransfer?.files ?? []);
    if (files.length) await library.uploadFiles(files);
  }

  // aria-disabled, not disabled, while an upload runs: a real disabled
  // attribute would blur the button the user just activated, so the picker
  // opener guards instead. uploadFiles carries its own plain-flag reentry
  // guard for the same-tick case.
  function openFilePicker(): void {
    if (library.uploading) return;
    fileInput?.click();
  }

  // Non-reactive in-flight guard, same doctrine as the drag counter above: a
  // signal read immediately after its own write still returns the pre-write
  // value, so a signal-based check would let two Enter presses in one flush
  // window both through. That matters here because addCustomFlair reads
  // getNextPaletteColor(customFlairs.length) BEFORE its await, so the second
  // call sees the pre-insert length and mints a duplicate chip in the same
  // colour. The paired signal exists only to drive the disabled state.
  let adding = false;
  const [addingFlair, setAddingFlair] = createSignal(false);

  async function addFlair(): Promise<void> {
    const name = newFlair().trim();
    if (!name || adding) return;
    adding = true;
    setAddingFlair(true);
    try {
      // addCustomFlair swallows its own errors and reports outcome by return
      // value. Clearing unconditionally threw away what the user typed on every
      // failure, leaving a transient toast as the only trace of it.
      const created = await library.addCustomFlair(name);
      if (created) setNewFlair("");
    } finally {
      adding = false;
      setAddingFlair(false);
    }
  }

  // Deleting a custom flair is destructive and, unlike every other destructive
  // control on this page, was a bare one-click: the store strips the flair from
  // every book carrying it, optimistically, before the server round-trip.
  // BookCard confirms a single-book delete, so an action that can clear dozens
  // of assignments cannot be quieter than that.
  function removeFlair(f: FlairDef): void {
    const n = library.books.filter((b) => b.flairId === f.id).length;
    const scope =
      n === 0
        ? ""
        : n === 1
          ? " It is currently on 1 book."
          : ` It is currently on ${n} books.`;
    if (confirm(`Delete the flair “${f.label}”?${scope}`))
      void library.removeCustomFlair(f.id);
  }

  // Text for the single persistent status live region below.
  const statusText = () =>
    library.loading
      ? "Loading…"
      : library.books.length > 0 && library.visible.length === 0
        ? "No books match your search or filters."
        : "";

  // The outside-pointerdown listener lives only while the menu is open.
  // Compute/apply pair: the single-argument createEffect is a one-shot in
  // Solid 2.0 and silently drops the returned cleanup (MISSING_EFFECT_FN),
  // so this listener would never attach on open.
  createEffect(
    () => sortOpen(),
    (open) => {
      if (!open) return undefined;
      window.addEventListener("pointerdown", onSortOutside);
      return () => window.removeEventListener("pointerdown", onSortOutside);
    },
  );

  // Move focus into the sort menu on open. The items' self-focusing ref could
  // never do it: Solid runs element refs while the node is still detached, so
  // .focus() no-oped and the active element stayed on the trigger -- which
  // left the roving arrow keys unreachable (they listen on the menu, and key
  // events never bubble UP to it) and made aria-expanded assert a focus move
  // that never happened. One microtask after open, matching ThemeDropdown and
  // ProfileMenu; the tabindex="0" item -- the active sort -- is the entry
  // point the markup nominates.
  let sortGen = 0;
  createEffect(
    () => sortOpen(),
    (open) => {
      const gen = ++sortGen;
      if (!open) return undefined;
      queueMicrotask(() => {
        if (gen !== sortGen) return;
        const el = sortMenuEl;
        if (!el) return;
        const items = Array.from(
          el.querySelectorAll<HTMLButtonElement>(".lib-sort-item"),
        );
        const preferred = items.find(
          (it) => it.getAttribute("tabindex") === "0",
        );
        (preferred ?? items[0] ?? el).focus();
      });
      return undefined;
    },
  );

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
            <Icon icon={ArrowUpDown} size={15} decorative />
            <span class="lib-sort-label">{sortLabel()}</span>
            <Icon icon={ChevronDown} size={14} class="caret" decorative />
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
              {/* SORT_OPTIONS is a frozen module-level constant: the array
                  never changes identity or contents, so <For>'s keyed
                  reconciliation had nothing to reconcile and cost a keyed diff
                  every time the menu opened. Solid 2.0 does not export <Index>,
                  and this list needs no reconciler node at all -- a plain
                  .map() emits the five buttons once. */}
              {SORT_OPTIONS.map((opt) => {
                const active = () => library.sort === opt.key;
                return (
                  <button
                    type="button"
                    class={["lib-sort-item", active() ? "active" : ""]}
                    role="menuitemradio"
                    aria-checked={active() ? "true" : "false"}
                    tabindex={active() ? "0" : "-1"}
                    onClick={() => chooseSort(opt.key)}
                  >
                    <span class="lib-sort-item-label">{opt.label}</span>
                    <Show when={active()}>
                      <span class="lib-check" aria-hidden="true">
                        <Icon icon={Check} size={15} decorative />
                      </span>
                    </Show>
                  </button>
                );
              })}
            </div>
          </Show>
        </div>

        <button
          type="button"
          class={["icon-btn press", library.rescanning ? "active" : ""]}
          onClick={() => {
            // aria-disabled, not disabled: a real disabled attribute would
            // blur the button mid-scan. The guard refuses re-entry instead
            // (the store's own plain-flag guard backs it same-tick).
            if (library.rescanning) return;
            void library.rescan();
          }}
          aria-disabled={library.rescanning ? "true" : "false"}
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
            labelFromParent
          />
        </button>

        <button
          type="button"
          class="btn press lib-upload"
          onClick={openFilePicker}
          aria-disabled={library.uploading ? "true" : "false"}
        >
          <Icon icon={Plus} size={16} decorative />
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
        <CardSizeControl />
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
                    title={
                      active()
                        ? `Remove the ${f.label} filter`
                        : `Show only books with the ${f.label} flair`
                    }
                    onClick={() => library.toggleFlairFilter(f.id)}
                  >
                    <Show
                      when={active()}
                      fallback={
                        <span class="lib-dot" style={{ background: f.color }} />
                      }
                    >
                      {/* The check marks the active filter at rest; on
                          hover/focus CSS swaps it for the remove mark, so an
                          active chip advertises its clear affordance. */}
                      <span
                        class="lib-chip-check"
                        style={{ color: f.color }}
                        aria-hidden="true"
                      >
                        <Icon icon={Check} size={13} decorative />
                      </span>
                      <span class="lib-chip-remove" aria-hidden="true">
                        <Icon icon={X} size={13} decorative />
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
                      onClick={() => removeFlair(f)}
                    >
                      <Icon icon={X} size={13} labelFromParent />
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
              onKeyDown={(e) => {
                if (e.key === "Enter") void addFlair();
              }}
              aria-label="New flair name"
            />
            <button
              type="button"
              onClick={() => void addFlair()}
              aria-disabled={
                !newFlair().trim() || addingFlair() ? "true" : "false"
              }
            >
              {addingFlair() ? "Adding…" : "Add"}
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

      {/* Live regions must already be in the accessibility tree BEFORE their
          text appears (WCAG 4.1.3). Mounting the region and its content in the
          same tick gives AT no "before" to diff against, and NVDA and JAWS drop
          the announcement outright -- which is why an upload failure and an
          empty result set were both silent. These two stay mounted for the life
          of the route and only their text changes; .lib-live collapses them to
          zero height while empty WITHOUT display:none, which would take them
          back out of the a11y tree and reintroduce the bug. */}
      <p class="lib-error lib-live" role="alert">
        {library.error}
      </p>
      <p class="lib-state lib-live" role="status">
        {statusText()}
      </p>

      {library.loading ? null : library.books.length === 0 ? (
        <div class="lib-empty">
          <span class="fleuron lib-empty-mark" aria-hidden="true">
            ❦
          </span>
          <p class="lib-empty-title display">An empty shelf.</p>
          <p class="lib-empty-sub">Every library starts with a single book.</p>
          <button
            type="button"
            class="btn press"
            onClick={openFilePicker}
            aria-disabled={library.uploading ? "true" : "false"}
          >
            <Icon icon={Plus} size={16} decorative />
            {library.uploading ? "Uploading…" : "Add your first book"}
          </button>
          <p class="lib-hint">
            …or drop .epub files into your <code>Library</code> folder.
          </p>
        </div>
      ) : library.visible.length === 0 ? (
        <div class="lib-noresults">
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
        // The shelf's column floor. cardSizeCss() always returns a valid
        // value -- `initial` when no size has been chosen -- so app.css can
        // fall back to its fluid default; see lib/cardSize.ts.
        <div
          class="lib-grid"
          role="list"
          style={{ "--card-size": cardSizeCss() }}
        >
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
              <Icon icon={Plus} size={36} decorative />
            </span>
            <p class="display">Add to your library</p>
            <span class="lib-dropzone-sub">Drop .epub files anywhere</span>
          </div>
        </div>
      </Show>
    </div>
  );
}
