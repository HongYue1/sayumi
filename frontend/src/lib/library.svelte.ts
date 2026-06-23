import {
  getBooks,
  uploadBook,
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

class Library {
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

  async load(): Promise<void> {
    this.loading = true;
    this.error = "";
    try {
      this.books = await getBooks();
    } catch (e) {
      // When the server is unreachable the global offline banner already says
      // so; don't stack a redundant red error above the empty list. Genuine
      // errors (e.g. a 500) still surface inline.
      this.error = isReachable() ? msg(e, "Failed to load library") : "";
    } finally {
      this.loading = false;
    }
    // Custom flairs are non-blocking; built-in flairs always work.
    getFlairs()
      .then((f) => (this.customFlairs = f))
      .catch(() => {});
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
    const prev = this.books;
    this.books = this.books.map((b) =>
      b.id === bookId ? { ...b, flairId: flairId ?? undefined } : b,
    );
    try {
      await setBookFlair(bookId, flairId);
    } catch (e) {
      this.books = prev;
      toast.show(msg(e, "Could not update flair"));
    }
  }

  async addCustomFlair(label: string): Promise<void> {
    const trimmed = label.trim();
    if (!trimmed) return;
    const color = getNextPaletteColor(this.customFlairs.length);
    try {
      const flair = await createFlair({ label: trimmed, color });
      this.customFlairs = [...this.customFlairs, flair];
    } catch (e) {
      toast.show(msg(e, "Could not create flair"));
    }
  }

  async removeCustomFlair(id: string): Promise<void> {
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
    try {
      await this.load();
    } catch (e) {
      toast.show(msg(e, "Failed to refresh library"));
    } finally {
      this.uploading = false;
    }
    if (ok > 0) toast.show(`Added ${ok} ${ok === 1 ? "book" : "books"}`);
    if (failed > 0)
      toast.show(
        `${failed} ${failed === 1 ? "file" : "files"} failed to import`,
      );
  }

  async remove(id: string): Promise<void> {
    try {
      await deleteBook(id);
      this.books = this.books.filter((b) => b.id !== id);
      this.hayCache.delete(id);
    } catch (e) {
      toast.show(msg(e, "Could not remove book"));
    }
  }

  /** Re-scans the ./Library folder for files added outside the app. */
  async rescan(): Promise<void> {
    if (this.rescanning) return;
    this.rescanning = true;
    try {
      const { imported } = await rescanLibrary();
      if (imported > 0) await this.load();
      toast.show(
        imported === 0
          ? "No new books found"
          : `Added ${imported} ${imported === 1 ? "book" : "books"}`,
      );
    } catch (e) {
      toast.show(msg(e, "Rescan failed"));
    } finally {
      this.rescanning = false;
    }
  }
}

export const library = new Library();
