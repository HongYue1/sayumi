import { createMemo, createSignal, createStore, runWithOwner } from "solid-js";
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
import { toast } from "~/lib/toast";
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

// Descending compare for ISO-8601 timestamps (missing sorts last). Plain
// codepoint comparison is correct for ISO strings and much cheaper than
// localeCompare, which applies locale collation rules to every call.
function compareIsoDesc(a?: string, b?: string): number {
  const av = a ?? "";
  const bv = b ?? "";
  return av === bv ? 0 : av < bv ? 1 : -1;
}

export class Library {
  // `books` is a store, not a signal: createReadingProgressPublisher mutates a
  // single book's progress/lastReadAt in place to avoid copying the array on
  // every progress report, and stores are the only primitive here with
  // property-level reactivity. Same for the two flair arrays, whose updates are
  // all derived from the previous value.
  readonly #books = createStore<BookMeta[]>([]);
  readonly #customFlairs = createStore<FlairDef[]>([]);
  readonly #flairFilters = createStore<string[]>([]);

  readonly #loading = createSignal(false);
  readonly #uploading = createSignal(false);
  readonly #rescanning = createSignal(false);
  readonly #error = createSignal("");
  readonly #query = createSignal("");
  readonly #debouncedQuery = createSignal("");
  readonly #sort = createSignal<SortKey>("title");

  readonly #allFlairs: () => FlairDef[];
  readonly #visible: () => BookMeta[];

  /**
   * Non-reactive mirrors of the two re-entrancy guards.
   *
   * Solid batches writes, so a signal read immediately after a write still
   * returns the pre-write value. `uploading` and `rescanning` exist purely to
   * reject a second concurrent call; if their guards read the accessors, two
   * drops (or rescan clicks) dispatched in the same tick would both pass, start
   * two worker pools, and the first to finish would clear the flag mid-flight.
   * Control flow reads these fields; the signals only drive the UI.
   */
  #uploadingPlain = false;
  #rescanningPlain = false;

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
  #queryTimer: ReturnType<typeof setTimeout> | null = null;

  /** id -> lowercased "title author" haystack, computed once per book and reused
   *  across books-array changes. Optimistic flair/progress updates leave
   *  title/author untouched, so this avoids re-lowercasing every title on each
   *  such change; an entry is refreshed only when its title or author actually
   *  changes. (Plain Map, not reactive state: it's a memo cache read inside
   *  `visible`, never a render dependency.) */
  readonly #hayCache = new Map<
    string,
    { title: string; author: string; hay: string }
  >();

  constructor() {
    // Detached on purpose: in Solid 2.0 a root is owned by its parent by
    // default and an unowned memo warns, so an app-lifetime singleton has to
    // opt into global lifetime explicitly. This instance is never disposed.
    const derived = runWithOwner(null, () => ({
      allFlairs: createMemo<FlairDef[]>(() => [
        ...DEFAULT_FLAIRS,
        ...this.customFlairs,
      ]),
      visible: createMemo<BookMeta[]>(() => this.#computeVisible()),
    }));
    this.#allFlairs = derived.allFlairs;
    this.#visible = derived.visible;
  }

  get books(): readonly BookMeta[] {
    return this.#books[0];
  }

  get customFlairs(): readonly FlairDef[] {
    return this.#customFlairs[0];
  }

  /** Active flair filters (OR semantics); empty = no flair filtering. */
  get flairFilters(): readonly string[] {
    return this.#flairFilters[0];
  }

  get loading(): boolean {
    return this.#loading[0]();
  }

  get uploading(): boolean {
    return this.#uploading[0]();
  }

  get rescanning(): boolean {
    return this.#rescanning[0]();
  }

  get error(): string {
    return this.#error[0]();
  }

  get query(): string {
    return this.#query[0]();
  }

  /** Debounced mirror of `query`; the heavy `visible` filter reads this so
   *  typing doesn't re-filter+sort the whole library on every keystroke. */
  get debouncedQuery(): string {
    return this.#debouncedQuery[0]();
  }

  get sort(): SortKey {
    return this.#sort[0]();
  }

  set sort(value: SortKey) {
    this.#sort[1](value);
  }

  /** Built-in plus custom flairs, for pickers and filter chips. */
  get allFlairs(): FlairDef[] {
    return this.#allFlairs();
  }

  /** Books after search + flair filtering, then sorting (memoised). */
  get visible(): BookMeta[] {
    return this.#visible();
  }

  #setUploading(value: boolean): void {
    this.#uploadingPlain = value;
    this.#uploading[1](value);
  }

  #setRescanning(value: boolean): void {
    this.#rescanningPlain = value;
    this.#rescanning[1](value);
  }

  #hayFor(b: BookMeta): string {
    const hit = this.#hayCache.get(b.id);
    if (hit && hit.title === b.title && hit.author === b.author) return hit.hay;
    const hay = `${b.title} ${b.author}`.toLowerCase();
    this.#hayCache.set(b.id, { title: b.title, author: b.author, hay });
    return hay;
  }

  #computeVisible(): BookMeta[] {
    const q = this.debouncedQuery.trim().toLowerCase();
    const filters = this.flairFilters;
    let list = q
      ? this.books.filter((b) => this.#hayFor(b).includes(q))
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
        list.sort((a, b) => compareIsoDesc(a.addedAt, b.addedAt));
        break;
      case "read":
        list.sort(
          (a, b) => compareIsoDesc(a.lastReadAt, b.lastReadAt) || byTitle(a, b),
        );
        break;
      case "progress":
        list.sort((a, b) => b.progress - a.progress || byTitle(a, b));
        break;
    }
    return list;
  }

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

    if (this.#queryTimer) clearTimeout(this.#queryTimer);
    this.#queryTimer = null;
    this.#books[1](() => []);
    this.#customFlairs[1](() => []);
    this.#flairFilters[1](() => []);
    this.#query[1]("");
    this.#debouncedQuery[1]("");
    this.#loading[1](false);
    this.#setUploading(false);
    this.#setRescanning(false);
    this.#error[1]("");
    this.#hayCache.clear();
  }

  /** Activates a profile and performs its lazy initial load as one ordered
   *  operation. Route/component mount hooks use this instead of assuming the
   *  parent App effect has already run. */
  loadForProfile(profile: string | null): Promise<void> {
    this.activate(profile);
    return this.load();
  }

  #isCurrent(profile: string, generation: number): boolean {
    return this.#profile === profile && this.#generation === generation;
  }

  /**
   * Returns a profile-bound publisher for the reader route, so a late save
   * from an old reader instance can never mutate a different profile's list.
   * Deliberately bound to the profile NAME, not the generation: on a hard
   * refresh straight into /read, the Read component initializes (capturing
   * this publisher) before App's effect runs activate(), which bumps the
   * generation - a generation captured here would be stale on arrival and the
   * publisher permanently dead for that whole reading session.
   */
  createReadingProgressPublisher(
    profile: string | null,
    bookId: string,
  ): (chapter: number, percent: number, readAt?: string) => void {
    return (chapter, percent, readAt = new Date().toISOString()) => {
      if (
        profile === null ||
        this.#profile !== profile ||
        !Number.isSafeInteger(chapter) ||
        chapter < 0 ||
        !Number.isFinite(percent)
      ) {
        return;
      }

      // Draft mutation, so the per-book guard reads the live value rather than a
      // pre-write snapshot, and only progress/lastReadAt invalidate. Concurrent
      // optimistic updates to OTHER books are untouched.
      this.#books[1]((s) => {
        const book = s.find((b) => b.id === bookId);
        if (!book || book.chapterCount <= 0 || chapter >= book.chapterCount) {
          return;
        }
        const chapterPercent = Math.max(0, Math.min(1, percent));
        book.progress = Math.max(
          0,
          Math.min(1, (chapter + chapterPercent) / book.chapterCount),
        );
        book.lastReadAt = readAt;
      });
    };
  }

  #loadFlairs(profile: string, generation: number): void {
    if (this.#flairsLoaded || this.#flairsPromise) return;

    const promise = getFlairs()
      .then((flairs) => {
        if (!this.#isCurrent(profile, generation)) return;
        this.#customFlairs[1](() => flairs);
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

    this.#loading[1](true);
    this.#error[1]("");
    const promise = (async () => {
      try {
        const books = await getBooks();
        if (!this.#isCurrent(profile, generation)) return;
        this.#books[1](() => books);
        this.#booksLoaded = true;
      } catch (e) {
        if (!this.#isCurrent(profile, generation)) return;
        // When the server is unreachable the global offline banner already says
        // so; don't stack a redundant red error above the empty list. Genuine
        // errors (e.g. a 500) still surface inline.
        this.#error[1](isReachable() ? msg(e, "Failed to load library") : "");
      } finally {
        // A profile switch increments the generation before a new load starts,
        // so an old request cannot clear the new profile's loading state/promise.
        if (this.#isCurrent(profile, generation)) {
          this.#loading[1](false);
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
    this.#query[1](value);
    if (this.#queryTimer) clearTimeout(this.#queryTimer);
    this.#queryTimer = setTimeout(() => {
      this.#debouncedQuery[1](value);
      this.#queryTimer = null;
    }, 140);
  }

  toggleFlairFilter(id: string): void {
    // Draft mutation rather than read-then-replace: two toggles dispatched in
    // the same tick would otherwise both read the pre-write array and the second
    // would silently discard the first.
    this.#flairFilters[1]((s) => {
      const index = s.indexOf(id);
      if (index === -1) {
        s.push(id);
      } else {
        s.splice(index, 1);
      }
    });
  }

  clearFlairFilters(): void {
    this.#flairFilters[1]((s) => {
      s.splice(0, s.length);
    });
  }

  /** Assigns/clears a book's flair with an optimistic update + rollback. The
   *  rollback restores only this book's previous flair onto the CURRENT array
   *  - snapshotting the whole array would also revert unrelated updates that
   *  landed while the request was in flight. */
  async setFlair(bookId: string, flairId: string | null): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const prevFlair = this.books.find((b) => b.id === bookId)?.flairId;
    this.#books[1]((s) => {
      const book = s.find((b) => b.id === bookId);
      if (book) book.flairId = flairId ?? undefined;
    });
    try {
      await setBookFlair(bookId, flairId);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      this.#books[1]((s) => {
        const book = s.find((b) => b.id === bookId);
        if (book) book.flairId = prevFlair;
      });
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
      this.#customFlairs[1]((s) => {
        s.push(flair);
      });
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(msg(e, "Could not create flair"));
    }
  }

  async removeCustomFlair(id: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    // Plain copies, not the store proxies. The Svelte revision could hold the
    // old arrays because every update REPLACED them; draft mutation edits them
    // in place, so keeping the proxy here would alias the live state and the
    // rollback below would restore the already-mutated value.
    const prevFlairs = this.customFlairs.slice();
    const prevFilters = this.flairFilters.slice();
    // Remember which books carried this flair; rollback re-applies it to those
    // books on the CURRENT array instead of restoring a stale array snapshot
    // (which would revert unrelated in-flight updates too).
    const affected = new Set(
      this.books.filter((b) => b.flairId === id).map((b) => b.id),
    );
    // Optimistic: drop the flair, its filter, and any local assignment.
    this.#customFlairs[1]((s) => {
      const index = s.findIndex((f) => f.id === id);
      if (index !== -1) s.splice(index, 1);
    });
    this.#flairFilters[1]((s) => {
      const index = s.indexOf(id);
      if (index !== -1) s.splice(index, 1);
    });
    this.#books[1]((s) => {
      for (const book of s) {
        if (book.flairId === id) book.flairId = undefined;
      }
    });
    try {
      await deleteFlair(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      this.#customFlairs[1](() => prevFlairs);
      this.#flairFilters[1](() => prevFilters);
      this.#books[1]((s) => {
        for (const book of s) {
          if (affected.has(book.id)) book.flairId = id;
        }
      });
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
    // and emitting a duplicate "Added N books" toast. Reads the plain mirror
    // because the signal would still report false inside the same tick.
    if (this.#uploadingPlain) {
      toast.show("Still importing the previous batch\u2026");
      return;
    }
    this.#setUploading(true);
    let ok = 0;
    let failed = 0;
    // Import with a small concurrency cap rather than strictly one-at-a-time:
    // faster for multi-file drops without hammering the backend. The shared
    // cursor is safe to increment - JS is single-threaded and there is no await
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
      if (this.#isCurrent(profile, generation)) this.#setUploading(false);
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
    const target = this.books.find((b) => b.id === id);
    const prevMeta = target
      ? { title: target.title, author: target.author }
      : null;
    this.#books[1]((s) => {
      const book = s.find((b) => b.id === id);
      if (book) Object.assign(book, patch);
    });
    try {
      const updated = await updateBookMeta(id, patch);
      if (!this.#isCurrent(profile, generation)) return;
      // Reconcile with the server's canonical record (e.g. trimmed values,
      // bumped updatedAt) and drop the stale search haystack for this book.
      this.#books[1]((s) => {
        const index = s.findIndex((b) => b.id === id);
        if (index !== -1) s[index] = updated;
      });
      this.#hayCache.delete(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      // Restore only this book's previous title/author onto the current array;
      // a whole-array snapshot would also revert unrelated in-flight updates.
      if (prevMeta) {
        this.#books[1]((s) => {
          const book = s.find((b) => b.id === id);
          if (book) Object.assign(book, prevMeta);
        });
      }
      throw e;
    }
  }

  /** Replaces a book's cover from an image file. Resolves once the grid reflects
   *  the new cover; rejects so the caller dialog can surface the error. No
   *  optimistic preview - the server normalizes the image, and the returned
   *  updatedAt is what busts the cover cache. */
  async replaceCover(id: string, file: File): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const updated = await uploadCover(id, file);
    if (!this.#isCurrent(profile, generation)) return;
    this.#books[1]((s) => {
      const index = s.findIndex((b) => b.id === id);
      if (index !== -1) s[index] = updated;
    });
  }

  async remove(id: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    try {
      await deleteBook(id);
      if (!this.#isCurrent(profile, generation)) return;
      this.#books[1]((s) => {
        const index = s.findIndex((b) => b.id === id);
        if (index !== -1) s.splice(index, 1);
      });
      this.#hayCache.delete(id);
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
    // Plain mirror: the signal would still read false inside the same tick, so
    // a double-click would start two rescans.
    if (this.#rescanningPlain) return;
    this.#setRescanning(true);
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
      if (this.#isCurrent(profile, generation)) this.#setRescanning(false);
    }
  }
}

export const library = new Library();
