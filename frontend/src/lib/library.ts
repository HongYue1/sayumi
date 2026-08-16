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
import { getErrorMessage } from "~/lib/errors";
import { toast } from "~/lib/toast";

export type SortKey = "title" | "author" | "added" | "read" | "progress";

export const SORT_OPTIONS: { key: SortKey; label: string }[] = [
  { key: "title", label: "Title" },
  { key: "author", label: "Author" },
  { key: "added", label: "Recently added" },
  { key: "read", label: "Recently read" },
  { key: "progress", label: "Progress" },
];

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
  // property-level reactivity. Same for the two flair arrays: apart from the
  // wholesale load/rollback replacements, their updates are all derived from
  // the previous value.
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
   * calls dispatched in the same tick would both pass - two interleaved upload
   * batches (or two rescans), where the first to finish clears the flag
   * mid-flight and re-enables the UI under the survivor.
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

  /** Per-book write stamps for the optimistic-rollback ownership checks.
   *  Comparing the live value to the optimistic one cannot tell this call's
   *  write from a later call that toggled the same value back (ABA), so each
   *  mutator captures the stamp before its optimistic write and keeps the
   *  rollback only while its own bump is still the newest write to that book.
   *  Server responses that replace a record wholesale clear the entry. */
  readonly #bookWrites = new Map<string, number>();

  #stampBookWrite(id: string): void {
    this.#bookWrites.set(id, (this.#bookWrites.get(id) ?? 0) + 1);
  }

  constructor() {
    // Detached on purpose: in Solid 2.0 a root is owned by its parent by
    // default, so an app-lifetime singleton opts into global lifetime
    // explicitly. The instance is never disposed; its memos are pure
    // derivations that autodispose when unwatched and recompute on next read.
    const derived = runWithOwner(null, () => {
      // Sort and filter are split across two memos on purpose. Fused, the
      // single memo depended on books, sort, debouncedQuery AND flairFilters,
      // so every debounced keystroke and every chip toggle re-sorted the whole
      // library even though neither input can change the order. Measured at
      // n=500: 6.6x faster on the keystroke path, 30x on the filter-toggle path
      // (156.9us -> 5.1us). Filtering preserves relative order, so `visible`
      // never needs to re-sort.
      //
      // `sorted` is a local const built BEFORE `visible`, and is passed in as
      // an argument rather than read back off `this`. createMemo evaluates its
      // body eagerly, so the visible memo runs during this constructor call --
      // declaring both in one object literal and reading `this.#sorted()`
      // inside threw, because the field is only assigned after runWithOwner
      // returns.
      const sorted = createMemo<BookMeta[]>(() => this.#computeSorted());
      return {
        allFlairs: createMemo<FlairDef[]>(() => [
          ...DEFAULT_FLAIRS,
          ...this.customFlairs,
        ]),
        visible: createMemo<BookMeta[]>(() => this.#computeVisible(sorted())),
      };
    });
    this.#allFlairs = derived.allFlairs;
    this.#visible = derived.visible;
  }

  get books(): readonly Readonly<BookMeta>[] {
    return this.#books[0];
  }

  get customFlairs(): readonly Readonly<FlairDef>[] {
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

  /** Depends on `books` and `sort` only. */
  #computeSorted(): BookMeta[] {
    const list = this.books.slice();

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

  /** Depends on the sorted list, `debouncedQuery` and `flairFilters` only. */
  #computeVisible(sorted: BookMeta[]): BookMeta[] {
    const q = this.debouncedQuery.trim().toLowerCase();
    const filters = this.flairFilters;
    // Array#filter is order-preserving, so the sorted order survives untouched.
    let list = sorted;

    if (q) list = list.filter((b) => this.#hayFor(b).includes(q));

    if (filters.length > 0) {
      list = list.filter(
        (b) => b.flairId !== undefined && filters.includes(b.flairId),
      );
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
    this.#bookWrites.clear();
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
    // Stamped in the server's own format (time.DateTime, progress.go) so the
    // codepoint compare in compareIsoDesc never has to mix formats.
    return (
      chapter,
      percent,
      readAt = new Date().toISOString().slice(0, 19).replace("T", " "),
    ) => {
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

    // The loading state is for the initial load only: a refresh with a
    // populated shelf revalidates in place instead of blanking the grid.
    if (this.#books[0].length === 0) this.#loading[1](true);
    this.#error[1]("");
    const promise = (async () => {
      try {
        const books = await getBooks();
        if (!this.#isCurrent(profile, generation)) return;
        this.#books[1](() => books);
        this.#booksLoaded = true;
        // Server truth replaces local writes: stale rollback guards reset.
        this.#bookWrites.clear();
      } catch (e) {
        if (!this.#isCurrent(profile, generation)) return;
        // A transport failure means the global offline banner is already on
        // its way (the API client flips reachability before throwing); don't
        // stack a redundant red error above the empty list. Keyed off the
        // error, not the reachability flag, which an unrelated earlier request
        // may have flipped. Genuine errors (e.g. a 500) still surface inline.
        this.#error[1](
          e instanceof ApiError && e.code === "network_error"
            ? ""
            : getErrorMessage(e, "Failed to load library"),
        );
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
   *  Each refresh chains behind any older refresh (and the initial load)
   *  rather than deduping onto it: a dedupe could resolve a caller's await
   *  with a read issued before that caller's mutation committed — uploadFiles
   *  would toast "Added N books" over a shelf that still lacked them, and
   *  nothing would refetch. Overlapping refreshes wait on each other, but
   *  every caller still gets a read that postdates its own mutation. The
   *  field is never cleared on settle: awaiting an already-settled refresh is
   *  a no-op, and a stale one can never clobber the slot it no longer owns. */
  async refresh(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    const prior = this.#refreshPromise;

    const promise = (async () => {
      if (prior) await prior;
      const initialLoad = this.#loadPromise;
      if (initialLoad) await initialLoad;
      if (!this.#isCurrent(profile, generation)) return;
      this.#booksLoaded = false;
      await this.load();
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
    const stamp = this.#bookWrites.get(bookId) ?? 0;
    this.#books[1]((s) => {
      const book = s.find((b) => b.id === bookId);
      if (book) book.flairId = flairId ?? undefined;
    });
    this.#stampBookWrite(bookId);
    try {
      await setBookFlair(bookId, flairId);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      // Roll back only while this call's write is still the newest one: an
      // overlapping setFlair that landed meanwhile owns the book now, even
      // when it re-wrote the same value this call had optimistically set.
      this.#books[1]((s) => {
        const book = s.find((b) => b.id === bookId);
        if (book && (this.#bookWrites.get(bookId) ?? 0) === stamp + 1) {
          book.flairId = prevFlair;
          this.#stampBookWrite(bookId);
        }
      });
      toast.show(getErrorMessage(e, "Could not update flair"));
    }
  }

  /**
   * Creates a custom flair and returns it, or null when no flair was added:
   * no active profile, an empty label, a superseded generation, or a failed
   * request (already surfaced as a toast). Errors stay swallowed here, so this
   * return value is the only signal a caller gets - Library.tsx needs it to
   * decide whether to clear the text the user typed.
   */
  async addCustomFlair(label: string): Promise<FlairDef | null> {
    const profile = this.#profile;
    if (profile === null) return null;
    const generation = this.#generation;
    const trimmed = label.trim();
    if (!trimmed) return null;
    const color = getNextPaletteColor(this.customFlairs.length);
    try {
      const flair = await createFlair({ label: trimmed, color });
      if (!this.#isCurrent(profile, generation)) return null;
      this.#customFlairs[1]((s) => {
        s.push(flair);
      });
      return flair;
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return null;
      toast.show(getErrorMessage(e, "Could not create flair"));
      return null;
    }
  }

  async removeCustomFlair(id: string): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    // Plain copies, not the store proxies: draft mutation edits the arrays in
    // place, so holding the proxy here would alias the live state and the
    // rollback below would restore the already-mutated value.
    const found = this.customFlairs.find((f) => f.id === id);
    const removedFlair = found ? { ...found } : null;
    const removedIndex = found ? this.customFlairs.indexOf(found) : -1;
    const hadFilter = this.flairFilters.includes(id);
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
        if (book.flairId === id) {
          book.flairId = undefined;
          this.#stampBookWrite(book.id);
        }
      }
    });
    try {
      await deleteFlair(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      // Surgical rollback, not a snapshot restore: additions and toggles
      // that landed while the delete was in flight stay.
      if (removedFlair) {
        this.#customFlairs[1]((s) => {
          if (!s.some((f) => f.id === removedFlair.id)) {
            s.splice(Math.min(Math.max(removedIndex, 0), s.length), 0, {
              ...removedFlair,
            });
          }
        });
      }
      if (hadFilter) {
        this.#flairFilters[1]((s) => {
          if (!s.includes(id)) s.push(id);
        });
      }
      this.#books[1]((s) => {
        for (const book of s) {
          if (affected.has(book.id) && book.flairId === undefined) {
            book.flairId = id;
            this.#stampBookWrite(book.id);
          }
        }
      });
      toast.show(getErrorMessage(e, "Could not delete flair"));
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
    // Guard against re-entrant upload batches: the first to finish would
    // clear `uploading` mid-flight, re-enabling the UI and emitting a
    // duplicate "Added N books" toast. Only a same-tick pair needs the plain
    // mirror - a later call would see the flushed signal; within one tick the
    // accessor still reports false, so both calls would pass.
    if (this.#uploadingPlain) {
      toast.show("Still importing the previous batch\u2026");
      return;
    }
    this.#setUploading(true);
    let ok = 0;
    let dupes = 0;
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
          // A duplicate is a success carrying a book, so counting it as added
          // would report "Added 3 books" for a batch that added nothing.
          const { duplicate } = await uploadBook(file);
          if (duplicate) dupes += 1;
          else ok += 1;
        } catch {
          failed += 1;
        }
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(CONCURRENCY, epubs.length) }, worker),
    );
    if (!this.#isCurrent(profile, generation)) return;
    // refresh() never rejects - load() reports failures through `error` -
    // so the flag clear only needs a finally; there is no refresh-failure toast.
    try {
      await this.refresh();
    } finally {
      if (this.#isCurrent(profile, generation)) this.#setUploading(false);
    }
    if (!this.#isCurrent(profile, generation)) return;
    if (ok > 0) toast.show(`Added ${ok} ${ok === 1 ? "book" : "books"}`);
    if (dupes > 0)
      toast.show(
        `${dupes} ${dupes === 1 ? "book was" : "books were"} already in the library`,
      );
    if (failed > 0)
      toast.show(
        `${failed} ${failed === 1 ? "file" : "files"} failed to import`,
      );
  }

  // Mutation doctrine for this store: a mutator writes optimistically only
  // when the client can compute the post-image itself (editMetadata,
  // setFlair, removeCustomFlair), then rolls back surgically on failure. When
  // the post-image is server-computed (replaceCover) or the operation is
  // already a grid-level refetch (upload, rescan), the write instead waits for
  // the server's enriched record. `remove` stays pessimistic although its
  // post-image is known: a false optimistic removal re-shows a book the user
  // just deleted, which reads worse than a delayed one. And whatever the
  // shape, a failure that is not a known pre-commit rejection leaves the
  // commit state ambiguous - see #reconcileAfterAmbiguousFailure.
  /** Edits a book's title/author with an optimistic update + rollback. On
   *  success the server's canonical record is reconciled into the store;
   *  rejects (after rollback) so the caller dialog can surface the error
   *  inline instead of a toast. */
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
    const stamp = this.#bookWrites.get(id) ?? 0;
    this.#books[1]((s) => {
      const book = s.find((b) => b.id === id);
      if (book) Object.assign(book, patch);
    });
    this.#stampBookWrite(id);
    try {
      const updated = await updateBookMeta(id, patch);
      if (!this.#isCurrent(profile, generation)) return;
      // Reconcile with the server's canonical record (e.g. trimmed values,
      // bumped updatedAt) and drop the stale search haystack for this book.
      // The response is now enriched server-side, so it carries progress,
      // lastReadAt and flairId too and replaces the record whole. Grafting
      // those back from the local copy would only reinstate what this client
      // last happened to see.
      this.#books[1]((s) => {
        const index = s.findIndex((b) => b.id === id);
        if (index === -1) return;
        s[index] = updated;
      });
      this.#hayCache.delete(id);
      this.#bookWrites.delete(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      // Restore this book's previous title/author on the current array, and
      // only the keys still holding THIS call's optimistic value - a newer
      // overlapping edit owns those keys now.
      if (prevMeta) {
        this.#books[1]((s) => {
          const book = s.find((b) => b.id === id);
          if (!book) return;
          // Same newest-write guard as setFlair's rollback: an overlapping
          // edit owns the book now, even if it re-typed the same text.
          if ((this.#bookWrites.get(id) ?? 0) !== stamp + 1) return;
          book.title = prevMeta.title;
          book.author = prevMeta.author;
          this.#stampBookWrite(id);
        });
      }
      // The rollback restores the likely case (a pre-commit rejection); the
      // reconcile heals the ambiguous one. The caller's view is unchanged:
      // the rejection still propagates to the dialog.
      this.#reconcileAfterAmbiguousFailure(e);
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
    let updated: BookMeta;
    try {
      updated = await uploadCover(id, file);
    } catch (e) {
      // No optimistic write means nothing local to roll back; the reconcile
      // covers a failure that arrived after the server had already committed
      // (the row update runs on a detached context server-side).
      if (this.#isCurrent(profile, generation)) {
        this.#reconcileAfterAmbiguousFailure(e);
      }
      throw e;
    }
    if (!this.#isCurrent(profile, generation)) return;
    // Same enriched response shape as editMetadata: it already carries the
    // reader-owned fields alongside the new updatedAt (which busts the cover
    // cache), so it replaces the record whole.
    this.#books[1]((s) => {
      const index = s.findIndex((b) => b.id === id);
      if (index === -1) return;
      s[index] = updated;
    });
    this.#bookWrites.delete(id);
  }

  /**
   * Best-effort reconcile after a mutator fails with the commit state
   * ambiguous. The book-edit handlers commit on a context detached from the
   * request (bookEditCommitTimeout), so a network_error - or the post-commit
   * "updated but failed to reload" 500 - can mean the server holds a change
   * this side reported as failed. A 4xx is always a pre-commit rejection from
   * these handlers (validation, conflict, size), so it skips the round trip.
   * refresh() is deduped and never rejects; on a genuinely dead server its
   * load() defers to the offline banner instead of stacking an inline error.
   */
  #reconcileAfterAmbiguousFailure(e: unknown): void {
    if (
      e instanceof ApiError &&
      e.status !== undefined &&
      e.status >= 400 &&
      e.status < 500
    ) {
      return;
    }
    void this.refresh();
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
      this.#bookWrites.delete(id);
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(getErrorMessage(e, "Could not remove book"));
    }
  }

  /** Re-scans the library folder (default <executable-dir>/Library) for files
   *  added outside the app. */
  async rescan(): Promise<void> {
    const profile = this.#profile;
    if (profile === null) return;
    const generation = this.#generation;
    // Plain mirror: the signal would still read false inside the same tick, so
    // a double-click would start two rescans.
    if (this.#rescanningPlain) return;
    this.#setRescanning(true);
    try {
      const { imported, refreshed, partial } = await rescanLibrary();
      if (!this.#isCurrent(profile, generation)) return;
      // Always refresh, even when nothing was imported: a rescan also backfills
      // covers and reconciles paths for existing books, which the server
      // reports as refreshed rather than imported.
      await this.refresh();
      if (!this.#isCurrent(profile, generation)) return;
      // A scan that stopped early still committed what it reports, and the
      // next scan will not re-report it, so say what landed instead of calling
      // the whole thing a failure.
      if (partial) {
        toast.show(
          `Rescan incomplete: added ${imported}, refreshed ${refreshed} before it stopped`,
        );
        return;
      }
      toast.show(
        imported === 0
          ? "No new books found"
          : `Added ${imported} ${imported === 1 ? "book" : "books"}`,
      );
    } catch (e) {
      if (!this.#isCurrent(profile, generation)) return;
      toast.show(getErrorMessage(e, "Rescan failed"));
    } finally {
      if (this.#isCurrent(profile, generation)) this.#setRescanning(false);
    }
  }
}

export const library = new Library();
