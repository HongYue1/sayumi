// BookCard: library grid card — Solid 2.0 port.
// (Solid notes: the dismiss effect and the edge-flip measurement split into
// compute→apply pairs; ref vars are assigned at mount and read only while a
// menu is open, so plain `let` bindings stay intentionally non-reactive.)
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { getCoverUrl, type BookMeta, type FlairDef } from "~/api/client";
import { findFlair, flairTextColor } from "~/lib/flairs";
import Icon from "~/lib/Icon";
import { Check, Pencil, Settings, Share2, Tag, Trash2 } from "~/lib/icons";

interface Props {
  book: BookMeta;
  flairs: FlairDef[];
  index?: number;
  onopen: (id: string) => void;
  onremove: (id: string) => void;
  onedit: (id: string) => void;
  onshare: (id: string) => void;
  onsetflair: (bookId: string, flairId: string | null) => void;
}

export default function BookCard(props: Props) {
  // One-shot entrance stagger: index is read once on mount by design (cards are
  // keyed by book.id, so each instance keeps its original position), and the
  // delay only drives the mount animation. Capped so large libraries don't
  // accumulate long delays.
  const enterDelay = Math.min(props.index ?? 0, 16) * 32;

  // Remember the exact URL that failed rather than permanently suppressing the
  // cover for this book id. Metadata/cover edits advance updatedAt while the
  // keyed card instance stays mounted, so a new URL must get a fresh attempt.
  const [failedCoverUrl, setFailedCoverUrl] = createSignal<string | null>(null);
  const coverUrl = createMemo(() =>
    getCoverUrl(props.book.id, props.book.updatedAt),
  );
  const [openMenu, setOpenMenu] = createSignal<"flair" | "actions" | null>(
    null,
  );
  const showCover = createMemo(
    () => props.book.hasCover && failedCoverUrl() !== coverUrl(),
  );
  const pct = createMemo(() =>
    Math.round(Math.max(0, Math.min(1, props.book.progress)) * 100),
  );
  const flair = createMemo(() => findFlair(props.book.flairId, props.flairs));
  // Whether the book's current flair is one of the selectable options, so the
  // menu can open with focus on the checked item (menuitemradio model, matching
  // ThemeDropdown) and fall back to the first item only when nothing is set.
  const hasActiveFlair = createMemo(() =>
    props.flairs.some((f) => f.id === props.book.flairId),
  );

  let flairBtn: HTMLButtonElement | undefined;
  let actionsBtn: HTMLButtonElement | undefined;
  let menuEl: HTMLDivElement | undefined;
  // The trigger that owns the currently-open popover, so dismiss/Escape can
  // restore focus to the right chip (gear or flair) regardless of which menu
  // is open.
  const activeTrigger = createMemo(() =>
    openMenu() === "flair"
      ? flairBtn
      : openMenu() === "actions"
        ? actionsBtn
        : null,
  );
  // Flip the open popover inward when a card near the right/bottom viewport
  // edge would otherwise open it off-screen.
  const [flipX, setFlipX] = createSignal(false);
  const [flipY, setFlipY] = createSignal(false);

  function toggleFlair(e: MouseEvent): void {
    e.stopPropagation();
    setOpenMenu(openMenu() === "flair" ? null : "flair");
  }

  function toggleActions(e: MouseEvent): void {
    e.stopPropagation();
    setOpenMenu(openMenu() === "actions" ? null : "actions");
  }

  function closeMenu(restoreFocus = true): void {
    // Capture the trigger before clearing openMenu, since activeTrigger derives
    // from it and would otherwise read null.
    const trigger = activeTrigger();
    setOpenMenu(null);
    if (restoreFocus) trigger?.focus();
  }

  function chooseEdit(e: MouseEvent): void {
    e.stopPropagation();
    // The dialog focus trap snapshots the active element on mount. Restore the
    // trigger before removing this focused menu item so focus can round-trip.
    closeMenu();
    props.onedit(props.book.id);
  }

  function chooseShare(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu();
    props.onshare(props.book.id);
  }

  function chooseDelete(e: MouseEvent): void {
    e.stopPropagation();
    closeMenu();
    if (confirm(`Remove “${props.book.title}” from your library?`))
      props.onremove(props.book.id);
  }

  function pick(e: MouseEvent, id: string): void {
    e.stopPropagation();
    props.onsetflair(props.book.id, props.book.flairId === id ? null : id);
    closeMenu();
  }

  // Dismiss on outside-click / Escape via window listeners instead of a fixed
  // scrim. `.bc-card` keeps a `transform` after its entrance animation (fill
  // mode `both` leaves translateY(0) applied), so the card is the containing
  // block for `position: fixed` descendants — a "fixed inset:0" scrim was
  // clipped to the card box, so only clicks ON the card closed the menu, and
  // Escape was missed whenever focus sat on the trigger (a sibling of the
  // menu, so its keydown never bubbled to the menu's handler). The
  // capture-phase click also swallows the dismissing click (like the old
  // scrim) so it doesn't fall through and open a book.
  createEffect(() => {
    const menu = openMenu();
    if (!menu) return undefined;
    const trigger = activeTrigger();
    const onWindowClick = (e: MouseEvent): void => {
      const t = e.target;
      if (
        menuEl?.contains(t as Node | null) ||
        trigger?.contains(t as Node | null)
      )
        return;
      e.preventDefault();
      e.stopPropagation();
      closeMenu(false);
    };
    const onWindowKeyDown = (e: KeyboardEvent): void => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeMenu();
      }
    };
    window.addEventListener("click", onWindowClick, true);
    window.addEventListener("keydown", onWindowKeyDown);
    return () => {
      window.removeEventListener("click", onWindowClick, true);
      window.removeEventListener("keydown", onWindowKeyDown);
    };
  });

  // Measure the open menu once and flip it inward if it spills past the
  // viewport edge; reset on close so the next open re-measures from the
  // default top-left position rather than inheriting a stale flip. Reads
  // openMenu/menuEl only (never flipX/flipY), so applying a flip doesn't
  // re-trigger this effect.
  createEffect(() => {
    const menu = openMenu();
    const el = menuEl;
    if (!menu || !el) {
      setFlipX(false);
      setFlipY(false);
      return undefined;
    }
    const r = el.getBoundingClientRect();
    const margin = 8;
    if (r.right > window.innerWidth - margin) setFlipX(true);
    if (r.bottom > window.innerHeight - margin) setFlipY(true);
    return undefined;
  });

  // Escape closes the popover and returns focus to its trigger.
  function onMenuKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMenu();
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
    // model so arrow keys move between flairs.
    const menu = e.currentTarget;
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>(".bc-menu-item"),
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
        // Contain focus: Tab wraps forward, Shift+Tab backward, so keyboard
        // focus can't escape into the grid behind the open popover.
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

  return (
    <div
      class="bc-card"
      role="listitem"
      style={{ "--enter-delay": `${enterDelay}ms` }}
    >
      {/* A real button carries native Enter/Space + focus semantics for "open",
			     so the card no longer nests action buttons inside a role="button". */}
      <button
        type="button"
        class="bc-open-overlay"
        aria-label={`Open ${props.book.title}`}
        onClick={() => props.onopen(props.book.id)}
      />

      {/* The volume: cover art treated as a physical book — spine shading on the
			     left, a soft ink shadow beneath, and a lift on hover. */}
      <div class="bc-volume">
        <div class="bc-cover">
          <Show
            when={showCover()}
            fallback={
              <div class="bc-placeholder">
                <span class="bc-ph-frame" aria-hidden="true" />
                <span class="bc-ph-fleuron" aria-hidden="true">
                  ❦
                </span>
                <span class="bc-ph-title display">{props.book.title}</span>
              </div>
            }
          >
            <img
              src={coverUrl()}
              alt=""
              loading={(props.index ?? 0) < 8 ? "eager" : "lazy"}
              fetchpriority={(props.index ?? 0) === 0 ? "high" : undefined}
              decoding="async"
              onError={() => setFailedCoverUrl(coverUrl())}
            />
          </Show>

          <Show when={pct() > 0}>
            <div
              class="bc-progress"
              role="progressbar"
              aria-label={`Reading progress for ${props.book.title}`}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={pct()}
              aria-valuetext={`${pct()}% read`}
              title={`${pct()}% read`}
            >
              <div class="bc-bar" style={{ width: `${pct()}%` }} />
            </div>
          </Show>

          <Show when={flair()}>
            {(f) => (
              <span
                class="bc-flair-badge"
                style={{
                  background: f().color,
                  color: flairTextColor(f().color),
                }}
              >
                {f().label}
              </span>
            )}
          </Show>
        </div>
      </div>

      {/* Corner actions live at card level (NOT inside .bc-volume): the volume's
			     hover-lift transform establishes a stacking context, which would trap
			     these buttons beneath the z-index:1 open-overlay and swallow their
			     clicks + :hover. At card level they stay above the overlay (z-index:3). */}
      <button
        type="button"
        ref={actionsBtn}
        class="bc-chip-btn bc-actions-btn"
        title="Actions"
        aria-label={`Book actions for ${props.book.title}`}
        aria-haspopup="menu"
        aria-expanded={openMenu() === "actions" ? "true" : "false"}
        onClick={toggleActions}
      >
        <Icon icon={Settings} size={15} />
      </button>
      <button
        type="button"
        ref={flairBtn}
        class="bc-chip-btn bc-flair-btn"
        title="Set flair"
        aria-label={`Set flair for ${props.book.title}`}
        aria-haspopup="menu"
        aria-expanded={openMenu() === "flair" ? "true" : "false"}
        onClick={toggleFlair}
      >
        <Icon icon={Tag} size={15} />
      </button>

      {/* Catalog caption beneath the volume. */}
      <div class="bc-meta">
        <div class="bc-title display" title={props.book.title}>
          {props.book.title}
        </div>
        <div class="bc-byline">
          <Show when={props.book.author}>
            <span class="bc-author" title={props.book.author}>
              {props.book.author}
            </span>
          </Show>
          <Show when={pct() > 0}>
            <span class="bc-pct tnum">{pct()}%</span>
          </Show>
        </div>
      </div>

      <Show when={openMenu() === "flair"}>
        <div
          ref={menuEl}
          class={[
            "bc-flair-menu paper",
            flipX() ? "flip-x" : "",
            flipY() ? "flip-y" : "",
          ]}
          role="menu"
          tabindex="-1"
          aria-label={`Set flair for ${props.book.title}`}
          onKeyDown={onMenuKeydown}
        >
          <p class="bc-menu-heading eyebrow" aria-hidden="true">
            Set flair
          </p>
          <For each={props.flairs}>
            {(f, i) => {
              const isActive = () => props.book.flairId === f.id;
              return (
                <button
                  type="button"
                  class={["bc-menu-item", isActive() ? "active" : ""]}
                  role="menuitemradio"
                  aria-checked={isActive() ? "true" : "false"}
                  tabindex={
                    isActive() || (i() === 0 && !hasActiveFlair()) ? "0" : "-1"
                  }
                  ref={(el) => {
                    // Open with focus on the checked flair (menuitemradio model); fall
                    // back to the first item only when the book has no flair set.
                    if (isActive() || (i() === 0 && !hasActiveFlair()))
                      el.focus();
                  }}
                  onClick={(e) => pick(e, f.id)}
                >
                  <span
                    class="bc-dot"
                    style={{ background: f.color }}
                    aria-hidden="true"
                  />
                  <span class="bc-menu-label">{f.label}</span>
                  <Show when={isActive()}>
                    <span class="bc-check" aria-hidden="true">
                      <Icon icon={Check} size={15} />
                    </span>
                  </Show>
                </button>
              );
            }}
          </For>
        </div>
      </Show>

      <Show when={openMenu() === "actions"}>
        <div
          ref={menuEl}
          class={[
            "bc-actions-menu paper",
            flipX() ? "flip-x" : "",
            flipY() ? "flip-y" : "",
          ]}
          role="menu"
          tabindex="-1"
          aria-label={`Book actions for ${props.book.title}`}
          onKeyDown={onMenuKeydown}
        >
          <button
            type="button"
            class="bc-menu-item"
            role="menuitem"
            tabindex="0"
            ref={(el) => el.focus()}
            onClick={chooseEdit}
          >
            <span class="bc-menu-ico" aria-hidden="true">
              <Icon icon={Pencil} size={15} />
            </span>
            <span class="bc-menu-label">Edit</span>
          </button>
          <button
            type="button"
            class="bc-menu-item"
            role="menuitem"
            tabindex="-1"
            onClick={chooseShare}
          >
            <span class="bc-menu-ico" aria-hidden="true">
              <Icon icon={Share2} size={15} />
            </span>
            <span class="bc-menu-label">Share</span>
          </button>
          <button
            type="button"
            class="bc-menu-item danger"
            role="menuitem"
            tabindex="-1"
            onClick={chooseDelete}
          >
            <span class="bc-menu-ico" aria-hidden="true">
              <Icon icon={Trash2} size={15} />
            </span>
            <span class="bc-menu-label">Delete</span>
          </button>
        </div>
      </Show>
    </div>
  );
}
