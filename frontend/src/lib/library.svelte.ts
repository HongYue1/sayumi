import {
  getBooks,
  uploadBook,
  updateBookMeta,
  uploadCover,
  deleteBook,
  rescanLibrary,
  getFlairs,
  createFlair,
  deleteFlair,
  setBookFlair,
  ApiError,
  type BookMeta,
  type FlairDef,
} from "~/api/client";
import { DEFAULT_FLAIRS, getNextPaletteColor } from "~/lib/flairs";
import { toast } from "~/lib/toast.svelte";
import { isReachable } from "~/lib/reachability";

export type SortKey = "title" | "author" | "added" | "read" | "progress";

export const SORT_OPTIONS: { key: SortKey; label: string }[] = [
  { key: "title", label: "Title" },
  { key: "author", label: "Author" },
  { key: "added", label: "Recently added" },
  { key: "read", label: "Recently read" },
  { key: "progress", label: "Progress" },
];

function msg(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

// Reused across every sort comparison. Calling String.prototype.localeCompare
// with an options object can construct a fresh collator on each call, which
// adds up when sorting large libraries; one shared Intl.Collator keeps the same
// natural collation ("Book 2" < "Book 10") while doing the setup work once.
const collator = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: "base",
});

export class Library {
  books = $state<BookMeta[]>([]);
  loading = $state(false);
  uploading = $state(false);
  rescanning = $state(false);
  error = $state("");
  query = $state("");
  /** Debounced mirror of `query`; the heavy `visible` filter reads this so
   *  typing doesn't re-filter+sort the whole library on every keystroke. */
  debouncedQuery = $state("");
  private queryTimer: ReturnType<typeof setTimeout> | null = null;
  sort = $state<SortKey>("title");
  customFlairs = $state<FlairDef[]>([]);
  /** Active flair filters (OR semantics); empty = no flair filtering. */
  flairFilters = $state<string[]>([]);

  /** Profile whose library state is currently published by this singleton. */
  #profile: string | null = null;
  /** Invalidates async work started under a previous profile. */
  #generation = 0;
  /** Current-profile request dedupe; failures clear these so a later call retries. */
  #loadPromise: Promise<void> | null = null;
  #refreshPromise: Promise<void> | null = null;
  #flairsPromise: Promise<void> | null = null;
  #booksLoaded = false;
  #flairsLoaded = false;

  /** Built-in plus custom flairs, for pickers and filter chips. */
  allFlairs = $derived<FlairDef[]>([...DEFAULT_FLAIRS, ...this.customFlairs]);

  /** id → lowercased "title author" haystack, computed once per book and reused
   *  across books-array replacements. Optimistic flair/progress updates replace
   *  the books array but leave title/author untouched, so this avoids
   *  re-lowercasing every title on each such change; an entry is refreshed only
   *  when its title or author actually changes. (Plain Map, not reactive state:
   *  it's a memo cache read inside `visible`, never a render dependency.) */
  private hayCache = new Map<
    string,
    { title: string; author: string; hay: string }
  >();
  private hayFor(b: BookMeta): string {
    const hit = this.hayCache.get(b.id);
    if (hit && hit.title === b.title && hit.author === b.author) return hit.hay;
    const hay = `${b.title} ${b.author}`.toLowerCase();
    this.hayCache.set(b.id, { title: b.title, author: b.author, hay });
    return hay;
  }

  /** Books after search + flair filtering, then sorting (memoised). */
  visible = $derived.by<BookMeta[]>(() => {
    const q = this.debouncedQuery.trim().toLowerCase();
    const filters = this.flairFilters;
    let list = q
      ? this.books.filter((b) => this.hayFor(b).includes(q))
      : this.books.slice();

    if (filters.length > 0) {
      list = list.filter(
        (b) => b.flairId !== undefined && filters.includes(b.flairId),
      );
    }

    const byTitle = (a: BookMeta, b: BookMeta) =>
      // numeric collation so "Book 2" sorts before "Book 10" (natural order).
      collator.compare(a.title, b.title);

    switch (this.sort) {
      case "title":
        list.sort(byTitle);
        break;
      case "author":
        list.sort(
          (a, b) => collator.compare(a.author, b.author) || byTitle(a, b),
        );
        break;
      case "added":
        list.sort((a, b) => (b.addedAt ?? "").localeCompare(a.addedAt ?? ""));
        break;
      case "read":
        list.sort((a, b) =>
          (b.lastReadAt ?? "").localeCompare(a.lastReadAt ?? ""),
        );
        break;
      case "progress":
        list.sort((a, b) => b.progress - a.progress || byTitle(a, b));
        break;
    }
    return list;
  });

  /**
   * Switches this app-lifetime singleton to a profile. Profile-owned state is
   * cleared synchronously so neither the library route nor the command palette
   * can render the previous profile while the replacement request is pending.
   * Loading remains lazy: Library and CommandPalette call load() when needed.
   */
  activate(profile: string | null): void {
    if (this.#profile === profile) return;

    this.#profile = profile;
    this.#generation++;
    this.#loadPromise = null;
    this.#refreshPromise = null;
    this.#flairsPromise = null;
    this.#booksLoaded = false;
    this.#flairsLoaded = false;

    if (this.queryTimer) clearTimeout(this.queryTimer);
    this.queryTimer = null;
    this.books = [];
    this.customFlairs = [];
    this.flairFilters = [];
    this.query = "";
    this.debouncedQuery = "";
    this.loading = false;
    this.uploading = false;
    this.rescanning = false;
    this.error = "";
    this.hayCache.clear();
  }

  #isCurrent(profile: string, generation: number): boolean {
    return this.#profile === profile && this.#generation === generation;
  }

  #loadFlairs(profile: string, generation: number): void {
    if (this.#flairsLoaded || this.#flairsPromise) return;

    const promise = getFlairs()
      .then((flairs) => {
        if (!this.#isCurrent(profile, generation)) return;
        this.customFlairs = flairs;
        this.#flairsLoaded = true;
      })
      .catch(() => {})
      .finally(() => {
        if (this.#isCurrent(profile, generation)) this.#flairsPromise = null;
      });
    this.#flairsPromise = promise;
  }

  async load(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;

    // Custom flairs are non-blocking; built-in flairs always work. Retry them
    // independently so a transient flair failure does not refetch all books.
    this.#loadFlairs(profile, generation);
    if (this.#booksLoaded) return;
    if (this.#loadPromise) return this.#loadPromise;

    this.loading = true;
    this.error = "";
    const promise = (async () => {
      try {
        const books = await getBooks();
        if (!this.#isCurrent(profile, generation)) return;
        this.books = books;
        this.#booksLoaded = true;
      } catch (e) {
        if (!this.#isCurrent(profile, generation)) return;
        // When the server is unreachable the global offline banner already says
        // so; don't stack a redundant red error above the empty list. Genuine
        // errors (e.g. a 500) still surface inline.
        this.error = isReachable() ? msg(e, "Failed to load library") : "";
      } finally {
        // A profile switch increments the generation before a new load starts,
        // so an old request cannot clear the new profile's loading state/promise.
        if (this.#isCurrent(profile, generation)) {
          this.loading = false;
          this.#loadPromise = null;
        }
      }
    })();
    this.#loadPromise = promise;
    return promise;
  }

  /** Refreshes books after an operation that can change the server library.
   *  Refreshes are deduped and wait for any older initial load before issuing
   *  the authoritative post-mutation read. */
  async refresh(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    if (this.#refreshPromise) return this.#refreshPromise;

    const promise = (async () => {
      try {
        const initialLoad = this.#loadPromise;
        if (initialLoad) await initialLoad;
        if (!this.#isCurrent(profile, generation)) return;
        this.#booksLoaded = false;
        await this.load();
      } finally {
        if (this.#isCurrent(profile, generation)) this.#refreshPromise = null;
      }
    })();
    this.#refreshPromise = promise;
    return promise;
  }

  /** Update the search box value instantly but debounce the expensive filter. */
  setQuery(value: string): void {
    this.query = value;
    if (this.queryTimer) clearTimeout(this.queryTimer);
    this.queryTimer = setTimeout(() => {
      this.debouncedQuery = value;
      this.queryTimer = null;
    }, 140);
  }

  toggleFlairFilter(id: string): void {
    this.flairFilters = this.flairFilters.includes(id)
      ? this.flairFilters.filter((f) => f !== id)
      : [...this.flairFilters, id];
  }

  clearFlairFilters(): void {
    this.flairFilters = [];
  }

  /** Assigns/clears a book's flair with an optimistic update + rollback. */
  async setFlair(bookId: string, flairId: string | null): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const prev = this.books;
    this.books = this.books.map((b) =>
      b.id === bookId ? { ...b, flairId: flairId ?? undefined } : b,
    );
    try {
      await setBookFlair(bookId, flairId);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      this.books = prev;
      toast.show(msg(e, "Could not update flair"));
    }
  }

  async addCustomFlair(label: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const trimmed = label.trim();
    if (!trimmed) return;
    const color = getNextPaletteColor(this.customFlairs.length);
    try {
      const flair = await createFlair({ label: trimmed, color });
      if (!this.#isCurrent(profile, generation)) return;
      this.customFlairs = [...this.customFlairs, flair];
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(msg(e, "Could not create flair"));
    }
  }

  async removeCustomFlair(id: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const prevFlairs = this.customFlairs;
    const prevFilters = this.flairFilters;
    const prevBooks = this.books;
    // Optimistic: drop the flair, its filter, and any local assignment.
    this.customFlairs = this.customFlairs.filter((f) => f.id !== id);
    this.flairFilters = this.flairFilters.filter((f) => f !== id);
    this.books = this.books.map((b) =>
      b.flairId === id ? { ...b, flairId: undefined } : b,
    );
    try {
      await deleteFlair(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      this.customFlairs = prevFlairs;
      this.flairFilters = prevFilters;
      this.books = prevBooks;
      toast.show(msg(e, "Could not delete flair"));
    }
  }

  /** Uploads an .epub, then refreshes so dedupe/order reflect the server. */
  async upload(file: File): Promise<void> {
    await this.uploadFiles([file]);
  }

  /** Uploads one or more .epub files (e.g. a drag-and-drop batch). */
  async uploadFiles(files: File[]): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const epubs = files.filter(
      (f) =>
        f.name.toLowerCase().endsWith(".epub") ||
        f.type === "application/epub+zip",
    );
    if (epubs.length === 0) {
      toast.show("Only .epub files can be added");
      return;
    }
    // Guard against a re-entrant call (e.g. a drag-drop while the previous batch
    // is still importing): a second worker pool would double-load, and the
    // first to finish would clear `uploading` mid-flight, re-enabling the UI
    // and emitting a duplicate "Added N books" toast.
    if (this.uploading) {
      toast.show("Still importing the previous batch…");
      return;
    }
    this.uploading = true;
    let ok = 0;
    let failed = 0;
    // Import with a small concurrency cap rather than strictly one-at-a-time:
    // faster for multi-file drops without hammering the backend. The shared
    // cursor is safe to increment — JS is single-threaded and there is no await
    // between reading and bumping it.
    const CONCURRENCY = 3;
    let cursor = 0;
    const worker = async (): Promise<void> => {
      while (cursor < epubs.length) {
        const file = epubs[cursor++];
        try {
          await uploadBook(file);
          ok += 1;
        } catch {
          failed += 1;
        }
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(CONCURRENCY, epubs.length) }, worker),
    );
    if (!this.#isCurrent(profile, generation)) return;
    try {
      await this.refresh();
    } catch (e) {
      if (this.#isCurrent(profile, generation)) {
        toast.show(msg(e, "Failed to refresh library"));
      }
    } finally {
      if (this.#isCurrent(profile, generation)) this.uploading = false;
    }
    if (!this.#isCurrent(profile, generation)) return;
    if (ok > 0) toast.show(`Added ${ok} ${ok === 1 ? "book" : "books"}`);
    if (failed > 0)
      toast.show(
        `${failed} ${failed === 1 ? "file" : "files"} failed to import`,
      );
  }

  /** Edits a book's title/author with an optimistic update + rollback. Resolves
   *  with the server's refreshed record; rejects (after rollback) so the caller
   *  dialog can surface the error inline instead of a toast. */
  async editMetadata(
    id: string,
    patch: { title?: string; author?: string },
  ): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const prev = this.books;
    this.books = this.books.map((b) => (b.id === id ? { ...b, ...patch } : b));
    try {
      const updated = await updateBookMeta(id, patch);
      if (!this.#isCurrent(profile, generation)) return;
      // Reconcile with the server's canonical record (e.g. trimmed values,
      // bumped updatedAt) and drop the stale search haystack for this book.
      this.books = this.books.map((b) => (b.id === id ? updated : b));
      this.hayCache.delete(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      this.books = prev;
      throw e;
    }
  }

  /** Replaces a book's cover from an image file. Resolves once the grid reflects
   *  the new cover; rejects so the caller dialog can surface the error. No
   *  optimistic preview — the server normalizes the image, and the returned
   *  updatedAt is what busts the cover cache. */
  async replaceCover(id: string, file: File): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const updated = await uploadCover(id, file);
    if (!this.#isCurrent(profile, generation)) return;
    this.books = this.books.map((b) => (b.id === id ? updated : b));
  }

  async remove(id: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    try {
      await deleteBook(id);
      if (!this.#isCurrent(profile, generation)) return;
      this.books = this.books.filter((b) => b.id !== id);
      this.hayCache.delete(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(msg(e, "Could not remove book"));
    }
  }

  /** Re-scans the ./Library folder for files added outside the app. */
  async rescan(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    if (this.rescanning) return;
    this.rescanning = true;
    try {
      const { imported } = await rescanLibrary();
      if (!this.#isCurrent(profile, generation)) return;
      if (imported > 0) await this.refresh();
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(
        imported === 0
          ? "No new books found"
          : `Added ${imported} ${imported === 1 ? "book" : "books"}`,
      );
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(msg(e, "Rescan failed"));
    } finally {
      if (this.#isCurrent(profile, generation)) this.rescanning = false;
    }
  }
}

export const library = new Library();
