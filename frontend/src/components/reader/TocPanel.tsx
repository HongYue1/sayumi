// TocPanel: virtualised, filterable table-of-contents panel — Solid 2.0 port.
//
// Solid 2.0 notes:
//   - Rendered state is signals (query/scrollTop/viewportH/focusedIndex, plus
//     a scrollEl signal so the mount/ResizeObserver effect re-runs if the nav
//     mounts late); padTop/lastQuery/composing/initialised stay plain `let`
//     because nothing rendered reads them.
//   - $derived -> createMemo; the two mount-time $effects become one
//     compute/apply createEffect keyed on scrollEl() with an `initialised`
//     guard for the open-time centering (the apply phase never tracks, so an
//     arriving TOC can't re-run it).
//   - The query-reset $effect becomes a compute/apply createEffect on
//     normalizedQuery().
//   - focusRow's `await tick()` -> flush(): setter results (and the shifted
//     virtual window) are only visible after a flush, so apply synchronously
//     before focusing the row's button.
//   - keyed #each -> <For> keyed by Row identity: the flattened row objects
//     are stable references, so DOM nodes follow their row across window
//     shifts (the Svelte string key encoded the same identity).
//   - style:/class: directives -> style objects / class={[...]}; bind:value
//     -> value + onInput; bind:this -> ref callbacks.
import {
  createEffect,
  createMemo,
  createSignal,
  flush,
  For,
  Show,
  untrack,
} from "solid-js";
import type { TocEntry } from "~/api/client";
import Icon from "~/lib/Icon";
import { X } from "~/lib/icons";
import {
  findFoldedCodePointRange,
  foldSearchText,
  splitFoldedCodePointMatch,
} from "~/lib/searchText";

interface Props {
  toc: TocEntry[];
  activeEntry: TocEntry | null;
  /** First chapter index each entry covers; drives the read-rail. */
  entryChapter?: Map<TocEntry, number> | null;
  /** Current chapter index; entries starting before it render as read. */
  currentChapter?: number;
  /** "Ch 3 of 12" — shown in the panel header. */
  positionLabel?: string;
  onnavigate: (href: string) => void;
  onclose: () => void;
}

// Fixed-height virtual list. Only the rows in (or near) the viewport are ever
// in the DOM, so a 6000-chapter TOC opens instantly instead of laying out and
// painting thousands of buttons. ROW_H must match the rendered row height; we
// pin .tocp-entry to var(--toc-row-h) (set from ROW_H below) so the two can't
// drift and the scroll math stays exact.
const ROW_H = 34; // px
const OVERSCAN = 8; // rows kept above/below the viewport to avoid blank edges

// The TOC is always fully expanded, so flatten the tree to a linear list in
// display order once per book; each row remembers its depth for indentation.
type Row = { entry: TocEntry; depth: number };

function rowId(index: number): string {
  return `toc-entry-${index}`;
}

export default function TocPanel(props: Props) {
  // scrollEl is a signal (not a plain let) so the mount effect below re-runs
  // if the <nav> mounts after first render — e.g. when the TOC arrives async
  // and the nav only appears once rows exist.
  const [scrollEl, setScrollEl] = createSignal<HTMLElement | null>(null);
  let queryEl: HTMLInputElement | undefined;
  const [query, setQuery] = createSignal("");
  const [scrollTop, setScrollTop] = createSignal(0);
  const [viewportH, setViewportH] = createSignal(0);
  const [focusedIndex, setFocusedIndex] = createSignal(-1);
  // .tocp-scroll's padding-top: row i's real offset is i * ROW_H + padTop, so
  // the scroll-into-view math must include it or rows land clipped by ~one
  // pad. (The virtualization window itself doesn't need it — OVERSCAN absorbs
  // the off-by-a-few-pixels in startIndex.) Measured, not hardcoded, so a
  // style tweak can't silently desync it. Not rendered: plain let.
  let padTop = 0;
  let lastQuery = "";
  let composing = false;
  let initialised = false;

  const rows = createMemo<Row[]>(() => {
    const out: Row[] = [];
    const walk = (entries: TocEntry[], depth: number): void => {
      for (const entry of entries) {
        out.push({ entry, depth });
        if (entry.children?.length) walk(entry.children, depth + 1);
      }
    };
    walk(props.toc, 0);
    return out;
  });

  // Client-side filter over the flattened list. Case-insensitive substring on
  // the title (mirrors the library's substring filter). The match is cheap
  // even for huge TOCs, so no debounce; an empty query passes the list
  // through untouched so the virtual-list math below is identical to the
  // unfiltered case. The filtered list (not the full list) is what the window
  // renders, so every memo below keys off it.
  const normalizedQuery = createMemo(() => query().trim());
  const foldedQuery = createMemo(() => foldSearchText(normalizedQuery()));
  const filteredRows = createMemo<Row[]>(() => {
    const q = foldedQuery();
    if (q.length === 0) return rows();
    return rows().filter(
      (r) => findFoldedCodePointRange(r.entry.title, q) !== null,
    );
  });

  /** True when the entry's own chapter lies behind the reading position. */
  function isRead(entry: TocEntry): boolean {
    const current = props.currentChapter ?? -1;
    const map = props.entryChapter ?? null;
    if (current < 0 || !map) return false;
    const start = map.get(entry);
    return start !== undefined && start < current;
  }

  // Rows covered by the progress rail's fill: the leading run of read rows
  // plus the current one (order is preserved by filtering, so they always
  // form a prefix). Drawn as ONE continuous element behind the list — a
  // per-row segment would bend around each row's rounded corners.
  const railFillRows = createMemo(() => {
    const idx = filteredRows().findIndex(
      (r) => !isRead(r.entry) && r.entry !== props.activeEntry,
    );
    return idx === -1 ? filteredRows().length : idx;
  });

  // Highlight the matched substring (first occurrence) so a filtered list is
  // scannable. Returns null when there's no active query so unfiltered rows
  // render as plain text. The highlight is a height-neutral inline <mark>
  // (background/colour only, no vertical padding or border) so it can't shift
  // the fixed row height the virtual-scroll math depends on.
  function highlight(
    title: string,
  ): { before: string; match: string; after: string } | null {
    const q = foldedQuery();
    if (q.length === 0) return null;

    return splitFoldedCodePointMatch(title, q);
  }

  // Match on reference identity (not href) so exactly one row is current even
  // when two entries point at the same file.
  const activeIndex = createMemo(() =>
    props.activeEntry
      ? filteredRows().findIndex((r) => r.entry === props.activeEntry)
      : -1,
  );

  const startIndex = createMemo(() =>
    Math.max(0, Math.floor(scrollTop() / ROW_H) - OVERSCAN),
  );
  const endIndex = createMemo(() =>
    Math.min(
      filteredRows().length,
      startIndex() + Math.ceil(viewportH() / ROW_H) + OVERSCAN * 2,
    ),
  );
  const windowRows = createMemo(() =>
    filteredRows().slice(startIndex(), endIndex()),
  );
  const totalHeight = createMemo(() => filteredRows().length * ROW_H);
  const offsetY = createMemo(() => startIndex() * ROW_H);

  // Roving tabindex under virtualization. focusedIndex tracks the row the user
  // last touched, and scrolling deliberately does not move it, so once that row
  // leaves the rendered window every mounted row sits at tabindex -1 and the
  // whole list drops out of the tab order -- a keyboard user who scrolls away
  // from the current chapter has no way back into the contents. focusTrap's
  // `button:not([disabled])` still matches those rows, so a trap-driven test
  // sees a focusable list and reports nothing. Clamp the single tab stop into
  // the window that is actually mounted.
  const tabStopIndex = createMemo(() => {
    const last = endIndex() - 1;
    if (last < startIndex()) return -1;
    return Math.min(Math.max(focusedIndex(), startIndex()), last);
  });

  function onScroll(e: Event): void {
    setScrollTop((e.currentTarget as HTMLElement).scrollTop);
  }

  function clearQuery(): void {
    setQuery("");
    queryEl?.focus();
  }

  function onQueryKey(e: KeyboardEvent): void {
    if (e.isComposing || composing) return;

    if (e.key === "ArrowDown") {
      e.preventDefault();
      focusRow(focusedIndex() >= 0 ? focusedIndex() : 0);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      focusRow(
        focusedIndex() >= 0 ? focusedIndex() : filteredRows().length - 1,
      );
    } else if (e.key === "Enter") {
      // Jump straight to the first match so a quick filter-then-Enter opens the
      // most likely chapter without touching the mouse.
      e.preventDefault();
      const first = filteredRows()[0];
      if (first) props.onnavigate(first.entry.href);
    } else if (e.key === "Escape") {
      // The reader's global Escape (which closes the panel) never fires while a
      // panel input is focused — handleWindowKey in Read bails on
      // INPUT/TEXTAREA so letter shortcuts aren't typed — and this field is
      // focused on open. So drive both steps locally: a non-empty query clears
      // first, an empty query closes the panel. Branch on the RAW query, not
      // the normalized one: the clear button renders on query() too, so a
      // whitespace-only filter puts a visible clear affordance on screen, and
      // Escape has to clear it rather than skip straight to closing the panel.
      e.preventDefault();
      e.stopPropagation();
      if (query()) setQuery("");
      else props.onclose();
    }
  }

  function onCompositionStart(): void {
    composing = true;
  }

  function onCompositionEnd(): void {
    composing = false;
  }

  function focusRow(index: number): void {
    const el = scrollEl();
    const count = filteredRows().length;
    if (!el || count === 0) return;

    const nextIndex = Math.max(0, Math.min(count - 1, index));
    const rowTop = nextIndex * ROW_H + padTop;
    const rowBottom = rowTop + ROW_H;
    const visibleTop = el.scrollTop;
    const visibleBottom = visibleTop + el.clientHeight;

    if (rowTop < visibleTop) {
      el.scrollTop = rowTop;
    } else if (rowBottom > visibleBottom) {
      el.scrollTop = Math.max(0, rowBottom - el.clientHeight);
    }

    setScrollTop(el.scrollTop);
    setFocusedIndex(nextIndex);
    // flush() applies the window shift synchronously so the row's button is in
    // the DOM before we focus it (the Svelte original awaited tick()).
    flush();
    document.getElementById(rowId(nextIndex))?.focus({ preventScroll: true });
  }

  function onEntryKey(e: KeyboardEvent, index: number): void {
    let nextIndex: number | null = null;

    switch (e.key) {
      case "ArrowDown":
        nextIndex = index + 1;
        break;
      case "ArrowUp":
        nextIndex = index - 1;
        break;
      case "Home":
        nextIndex = 0;
        break;
      case "End":
        nextIndex = filteredRows().length - 1;
        break;
      case "PageDown":
        nextIndex = index + Math.max(1, Math.floor(viewportH() / ROW_H));
        break;
      case "PageUp":
        nextIndex = index - Math.max(1, Math.floor(viewportH() / ROW_H));
        break;
      default:
        return;
    }

    e.preventDefault();
    focusRow(nextIndex);
  }

  // Mount effect: track the viewport height with a ResizeObserver (the panel
  // resizes with the window), and on first layout jump straight to the current
  // chapter (centered) instead of opening at the top of a long book, then put
  // focus in the filter field so the user can type immediately. Focusing our
  // own element here is the focus-trap opt-out (preventScroll keeps the
  // centered position) — without it the trap would grab the first row and
  // yank the scroll back to the top. The compute phase tracks scrollEl() so
  // this re-runs if the nav mounts late; the `initialised` guard keeps the
  // centering once-per-open. The RO teardown rides the effect cleanup.
  createEffect(
    () => scrollEl(),
    (el) => {
      if (!el) return undefined;
      const measure = (): void => {
        setViewportH(el.clientHeight);
        padTop = parseFloat(getComputedStyle(el).paddingTop) || 0;
      };
      measure();

      if (!initialised) {
        // activeIndex/filteredRows are read from an effect apply phase, which
        // is an untracked scope. The reads are deliberate one-shots -- the
        // centering is once-per-open, guarded by `initialised` -- so mark them
        // with untrack the way Read.tsx does, rather than trip
        // STRICT_READ_UNTRACKED four times on every open.
        untrack(() => {
          if (activeIndex() >= 0) {
            const target =
              activeIndex() * ROW_H + padTop - (el.clientHeight - ROW_H) / 2;
            el.scrollTop = Math.max(0, target);
          }
          setScrollTop(el.scrollTop);
          setFocusedIndex(
            activeIndex() >= 0
              ? activeIndex()
              : filteredRows().length > 0
                ? 0
                : -1,
          );
          queryEl?.focus({ preventScroll: true });
        });
        initialised = true;
      }

      const ro = new ResizeObserver(measure);
      ro.observe(el);
      return () => ro.disconnect();
    },
  );

  // Whenever the query changes, reset the scroll to the top of the results so
  // a new filter starts at its first match rather than wherever the previous
  // list was scrolled. lastQuery starts at "" so the initial empty-query pass
  // skips itself and can't clobber the open-time centering above.
  createEffect(
    () => normalizedQuery(),
    (q) => {
      if (q === lastQuery) return undefined;
      lastQuery = q;
      // Apply phase again: deliberate reads, so untrack them rather than trip
      // STRICT_READ_UNTRACKED on every keystroke that changes the filter.
      untrack(() => {
        const el = scrollEl();
        if (el) {
          el.scrollTop = 0;
          setScrollTop(0);
          setFocusedIndex(filteredRows().length > 0 ? 0 : -1);
        }
      });
      return undefined;
    },
  );

  // Row title with the first query match wrapped in a <mark>; plain text when
  // unfiltered. Called inline from JSX so it re-tracks on query changes (a
  // per-row memo in the For callback would freeze at row-creation time).
  function renderTitle(row: Row) {
    const hl = highlight(row.entry.title);
    if (!hl) return row.entry.title;
    return (
      <>
        {hl.before}
        <mark>{hl.match}</mark>
        {hl.after}
      </>
    );
  }

  return (
    <div class="tocp">
      <header class="tocp-head">
        <div class="tocp-head-text">
          <p class="eyebrow">
            Reader
            <Show when={props.positionLabel}>
              <span class="tocp-pos-label tnum"> · {props.positionLabel}</span>
            </Show>
          </p>
          <h2 id="toc-panel-title" class="display tocp-title">
            Contents
          </h2>
        </div>
        <button
          class="icon-btn press tocp-close"
          onClick={props.onclose}
          aria-label="Close table of contents"
        >
          <Icon icon={X} size={18} labelFromParent />
        </button>
      </header>
      <Show
        when={rows().length > 0}
        fallback={<p class="tocp-empty">No table of contents.</p>}
      >
        <div class="tocp-filter">
          <input
            ref={(el) => {
              queryEl = el;
            }}
            value={query()}
            class="field"
            type="text"
            placeholder="Filter chapters…"
            autocomplete="off"
            spellcheck="false"
            aria-label="Filter table of contents"
            onKeyDown={onQueryKey}
            onInput={(e) => setQuery(e.currentTarget.value)}
            onCompositionStart={onCompositionStart}
            onCompositionEnd={onCompositionEnd}
          />
          <Show when={query()}>
            <button
              class="tocp-clear"
              onClick={clearQuery}
              aria-label="Clear filter"
            >
              <Icon icon={X} size={16} labelFromParent />
            </button>
          </Show>
        </div>
        <Show
          when={filteredRows().length > 0}
          fallback={
            <p class="tocp-empty" role="status">
              No chapters match “{query()}”.
            </p>
          }
        >
          <nav
            class="tocp-scroll"
            aria-label="Table of contents"
            ref={(el) => {
              setScrollEl(el);
            }}
            onScroll={onScroll}
            style={{ "--toc-row-h": `${ROW_H}px` }}
          >
            <div class="tocp-sizer" style={{ height: `${totalHeight()}px` }}>
              {/* Progress rail: hairline track the full height of the contents,
                  accent fill down through the current chapter. */}
              <div class="tocp-rail" aria-hidden="true" />
              <div
                class="tocp-rail-fill"
                aria-hidden="true"
                style={{ height: `${railFillRows() * ROW_H}px` }}
              />
              {/* eslint-disable jsx-a11y/no-redundant-roles -- the role is redundant only on paper: Safari and VoiceOver drop list semantics from a ul styled list-style: none, which .tocp-window is, and the rows' aria-setsize/aria-posinset describe a list that would otherwise not be announced as one. */}
              <ul
                class="tocp-window"
                role="list"
                style={{ transform: `translateY(${offsetY()}px)` }}
              >
                <For each={windowRows()}>
                  {(row, i) => {
                    const globalIndex = () => startIndex() + i();
                    return (
                      <li
                        aria-level={row.depth + 1}
                        aria-setsize={filteredRows().length}
                        aria-posinset={globalIndex() + 1}
                      >
                        <button
                          id={rowId(globalIndex())}
                          class={[
                            "tocp-entry",
                            {
                              current: row.entry === props.activeEntry,
                              read: isRead(row.entry),
                              top: row.depth === 0,
                            },
                          ]}
                          aria-current={
                            row.entry === props.activeEntry
                              ? "location"
                              : undefined
                          }
                          tabindex={globalIndex() === tabStopIndex() ? 0 : -1}
                          style={{
                            "padding-left": `${row.depth * 0.75 + 0.75}rem`,
                          }}
                          title={row.entry.title}
                          onFocus={() => setFocusedIndex(globalIndex())}
                          onKeyDown={(e) => onEntryKey(e, globalIndex())}
                          onClick={() => props.onnavigate(row.entry.href)}
                        >
                          {renderTitle(row)}
                        </button>
                      </li>
                    );
                  }}
                </For>
              </ul>
            </div>
          </nav>
        </Show>
      </Show>
    </div>
  );
}
