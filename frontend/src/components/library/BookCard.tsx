// BookCard: library grid card.
// (Solid notes: the dismiss effect and the edge-flip measurement split into
// compute→apply pairs; ref vars are assigned at mount and read only while a
// menu is open, so plain `let` bindings stay intentionally non-reactive.
// Apply phases are untracked scopes, so the chips are resolved with a plain
// lookup rather than a memo — reading a memo there logs STRICT_READ_UNTRACKED.
// Menu items do not self-focus from a ref either — refs run while the node is
// still detached, so focus moves in from a post-flush apply phase instead.)
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { getCoverUrl, type BookMeta, type FlairDef } from "~/api/client";
import { findFlair, flairTextColor } from "~/lib/flairs";
import Fleuron from "~/components/Fleuron";
import Icon from "~/lib/Icon";
import { Check, Pencil, Settings, Share2, Tag, Trash2 } from "~/lib/icons";

type MenuKind = "flair" | "actions";

interface Props {
  book: BookMeta;
  flairs: FlairDef[];
  index?: number;
  onopen: (id: string) => void;
  /** Requests removal. The host confirms; the card never deletes on its own. */
  onremove: (id: string) => void;
  onedit: (id: string) => void;
  onshare: (id: string) => void;
  onsetflair: (bookId: string, flairId: string | null) => void;
}

export default function BookCard(props: Props) {
  // One-shot entrance stagger: index is read once on mount by design (<For>
  // keys rows by item identity and library.load patches the shelf by id, so an
  // instance survives refreshes and keeps its original position), and the
  // delay only drives the mount animation. Capped so large libraries don't
  // accumulate long delays.
  const enterDelay = Math.min(props.index ?? 0, 16) * 32;

  // Same one-shot treatment, and for the same reason. `index` is a live getter
  // on the props proxy, so reading it inside JSX made loading/fetchpriority
  // reactive: every reorder (sort change, filter, upload) rewrote both
  // attributes on an already-live <img>, where the browser ignores them once
  // the request is in flight. Pure churn, plus a misleading read of which
  // covers are actually in the LCP window. Decide once, at creation.
  const eager = (props.index ?? 0) < 8;

  // Remember the exact URL that failed rather than permanently suppressing the
  // cover for this book id. Metadata/cover edits advance updatedAt while the
  // keyed card instance stays mounted, so a new URL must get a fresh attempt.
  const [failedCoverUrl, setFailedCoverUrl] = createSignal<string | null>(null);
  const coverUrl = createMemo(() =>
    getCoverUrl(props.book.id, props.book.updatedAt),
  );
  const [openMenu, setOpenMenu] = createSignal<MenuKind | null>(null);
  const showCover = createMemo(
    () => props.book.hasCover && failedCoverUrl() !== coverUrl(),
  );
  const pct = createMemo(() =>
    Math.round(Math.max(0, Math.min(1, props.book.progress)) * 100),
  );
  const flair = createMemo(() => findFlair(props.book.flairId, props.flairs));
  // One spelling of "no flair", and the same falsy test findFlair uses rather
  // than a second, stricter one: the API omits flairId when a book has none,
  // so the two can only diverge if that ever changes -- at which point the
  // chip and the checkmark would start contradicting each other.
  const noFlair = createMemo(() => !props.book.flairId);
  // Whether the book's current flair is one of the selectable options, so the
  // menu can open with focus on the checked item. When none is, the "No
  // flair" entry carries the nomination instead.
  const hasActiveFlair = createMemo(() =>
    props.flairs.some((f) => f.id === props.book.flairId),
  );

  let cardEl: HTMLDivElement | undefined;
  let flairBtn: HTMLButtonElement | undefined;
  let actionsBtn: HTMLButtonElement | undefined;
  let flairMenuEl: HTMLDivElement | undefined;
  let actionsMenuEl: HTMLDivElement | undefined;
  let overlayEl: HTMLButtonElement | undefined;
  // The trigger that owns a popover. A plain lookup, not a memo: it is
  // read from effect apply phases, which are untracked scopes where a memo
  // read logs STRICT_READ_UNTRACKED (see Read.tsx).
  const triggerFor = (menu: MenuKind | null): HTMLButtonElement | undefined =>
    menu === "flair" ? flairBtn : menu === "actions" ? actionsBtn : undefined;
  // The popover a menu kind owns, in the same shape as triggerFor. A single
  // shared ref would be correct only while the two menus stay mutually
  // exclusive and every close nulls it — a pair of rules nothing in the types
  // enforces, and one a direct chip-to-chip switch already sidesteps, since
  // that path never passes through null.
  const menuFor = (menu: MenuKind | null): HTMLDivElement | undefined =>
    menu === "flair"
      ? flairMenuEl
      : menu === "actions"
        ? actionsMenuEl
        : undefined;
  // Stable popover ids so each chip can point at the menu it owns.
  const flairMenuId = (): string => `bc-flair-menu-${props.book.id}`;
  const actionsMenuId = (): string => `bc-actions-menu-${props.book.id}`;
  // Flip the open popover inward when a card near the right/bottom viewport
  // edge would otherwise open it off-screen. The signals drive the class list;
  // the plain mirrors carry the same state into untracked scopes (the effect
  // apply phase, the reflow listeners), where the measurement has to know
  // which way the menu is currently flipped.
  const [flipX, setFlipX] = createSignal(false);
  const [flipY, setFlipY] = createSignal(false);
  let flippedX = false;
  let flippedY = false;

  function applyFlip(x: boolean, y: boolean): void {
    flippedX = x;
    flippedY = y;
    setFlipX(x);
    setFlipY(y);
  }

  // Decide both flips from the popover's geometry, always as if it were
  // unflipped. A flipped menu measures as fitting precisely *because* it is
  // flipped, so taking its rect at face value would latch the first flip for
  // as long as the card lives. The CSS pairs each flip with the edge it
  // replaces — `left: var(--sp-2)` against `right: var(--sp-2)`, `top: 2.4rem`
  // against `bottom: calc(100% - 2.4rem)` — so the default position is
  // recoverable from the card box without restating those offsets here.
  function measureFlip(menu: MenuKind): void {
    const el = menuFor(menu);
    const card = cardEl;
    if (!el || !card) {
      applyFlip(false, false);
      return;
    }
    const r = el.getBoundingClientRect();
    const c = card.getBoundingClientRect();
    const margin = 8;
    const right = flippedX ? c.left + (c.right - r.right) + r.width : r.right;
    const bottom = flippedY ? r.bottom + r.height : r.bottom;
    applyFlip(
      right > window.innerWidth - margin,
      bottom > window.innerHeight - margin,
    );
  }

  function toggleFlair(e: MouseEvent): void {
    e.stopPropagation();
    setOpenMenu(openMenu() === "flair" ? null : "flair");
  }

  function toggleActions(e: MouseEvent): void {
    e.stopPropagation();
    setOpenMenu(openMenu() === "actions" ? null : "actions");
  }

  function closeMenu(restoreFocus = true): void {
    // Resolve the trigger before clearing openMenu, which is what selects it.
    // Resolved before the write, which also self-guards a repeat: with no
    // menu open triggerFor(null) is undefined, so a dismissal that lands
    // after the popover closed cannot pull focus off whatever legitimately
    // took it. That is this component's equivalent of the openNow mirror the
    // single-popover components carry.
    const trigger = triggerFor(openMenu());
    setOpenMenu(null);
    // Drop the stale nodes so a queued focus can't land in a closed popover.
    flairMenuEl = undefined;
    actionsMenuEl = undefined;
    if (restoreFocus) trigger?.focus();
  }

  // Focus-out dismissal, the ProfileMenu/ThemeDropdown shape: focus leaving
  // the card for good closes the menu without a focus restore. A null
  // relatedTarget is the window itself losing focus (a blur or devtools), not
  // a leave — the menu stays.
  function onCardFocusOut(
    e: FocusEvent & { currentTarget: HTMLDivElement },
  ): void {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget.contains(next)) return;
    closeMenu(false);
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
    // Restore the trigger before this focused item is removed, exactly as
    // chooseEdit does: the confirmation's focus trap snapshots the active
    // element on mount and hands focus back to it on unmount.
    closeMenu();
    // The asking happens in the host, which owns the confirm dialog, and it
    // cannot happen here: `.bc-card` keeps a transform after its entrance
    // animation, so the card is the containing block for position:fixed
    // descendants and an overlay mounted inside it gets clipped to the card
    // box (see the menu dismissal note below). The native confirm() this
    // replaces named the real consequence -- the server runs
    // removeManagedLibraryFile on the .epub and its cover sidecar, a plain
    // os.Remove with no trash and no undo -- and that wording moved into the
    // dialog copy rather than being dropped.
    props.onremove(props.book.id);
  }

  function pick(e: MouseEvent, id: string): void {
    e.stopPropagation();
    // Re-picking the current flair clears it — a toggle, which is why the items
    // are menuitemcheckbox. A menuitemradio cannot be unchecked by activating
    // it again, so the role has to match the behaviour. The "No flair" entry
    // at the top of the menu is the discoverable form of that same clear.
    props.onsetflair(props.book.id, props.book.flairId === id ? null : id);
    closeMenu();
  }

  // The explicit clear affordance: checked exactly when the book carries no
  // flair, and a close-only no-op in that state — the state of record, not a
  // second toggle.
  function clearFlair(e: MouseEvent): void {
    e.stopPropagation();
    if (!noFlair()) {
      props.onsetflair(props.book.id, null);
    }
    closeMenu();
  }

  // Dismiss on outside-click / Escape via window listeners instead of a fixed
  // scrim. `.bc-card` keeps a `transform` after its entrance animation (fill
  // mode `both` leaves translateY(0) applied), so the card is the containing
  // block for `position: fixed` descendants — a "fixed inset:0" scrim was
  // clipped to the card box, so only clicks ON the card closed the menu, and
  // Escape was missed whenever focus sat on the trigger (a sibling of the
  // menu, so its keydown never bubbled to the menu's handler).
  //
  // The doctrine, shared with every other menu in the app (Library's sort
  // menu, ProfileMenu, ThemeDropdown, the reader's more menu): an outside
  // click closes the menu AND lands on its target — one activation, never a
  // dead first click. One principled exception: the card's own open-book
  // overlay sits directly beneath the open menu and is one giant activation
  // target, so a click there keeps the swallow — dismissing the menu must not
  // also open the book. The listener stays a capture-phase click because that
  // is the only phase that can preempt the overlay's own onClick; the other
  // menus sit on inert backgrounds, so their pointerdown pass-through suffices.
  // Compute/apply pair: single-argument createEffect is a one-shot in
  // Solid 2.0 and silently drops the returned cleanup (MISSING_EFFECT_FN),
  // so these window listeners would never attach on open.
  createEffect(
    () => openMenu(),
    (menu) => {
      if (!menu) return undefined;
      const trigger = triggerFor(menu);
      const onWindowClick = (e: MouseEvent): void => {
        const t = e.target;
        if (!(t instanceof Node)) return;
        if (menuFor(menu)?.contains(t) || trigger?.contains(t)) return;
        // The one swallow: the open-book overlay beneath the menu. Everything
        // else — the peer chip included — closes and lets the click land.
        if (overlayEl?.contains(t)) {
          e.preventDefault();
          e.stopPropagation();
        }
        // Read the focus holder before closing: the item holding it is still
        // mounted here, and the removal that strands focus lands on the next
        // flush. A click that took a real control keeps it — a dismissal
        // never steals focus back. Focus is orphaned in two shapes: the click
        // blurred to <body> because its target was unfocusable, or it never
        // moved and still sits in the menu about to be removed. Both end on
        // <body>, which is nobody's destination, so the chip takes it back.
        const held = document.activeElement;
        const orphaned =
          !held || held === document.body || !!menuFor(menu)?.contains(held);
        closeMenu(false);
        if (orphaned) trigger?.focus();
      };
      const onWindowKeyDown = (e: KeyboardEvent): void => {
        // This listener spans the window, so it would also consume the
        // Escape that abandons an IME composition elsewhere in the document.
        // Same guard as ProfileMenu's and ThemeDropdown's window handlers.
        if (e.key !== "Escape" || e.isComposing) return;
        e.preventDefault();
        closeMenu();
      };
      window.addEventListener("click", onWindowClick, true);
      window.addEventListener("keydown", onWindowKeyDown);
      return () => {
        window.removeEventListener("click", onWindowClick, true);
        window.removeEventListener("keydown", onWindowKeyDown);
      };
    },
  );

  // Flip the open menu inward when it spills past a viewport edge, and reset
  // on close. Compute/apply pair (same one-shot hazard as above): the DOM read
  // lives in the apply phase, which runs post-flush — after the menu's ref has
  // been assigned on open. Compute reads openMenu() only (never flipX/flipY,
  // which the mirrors carry instead), so applying a flip cannot re-trigger it.
  //
  // The decision then has to survive the card moving underneath it. A viewport
  // resize or a shelf scroll changes the geometry without touching a single
  // signal, so these listeners re-measure for as long as the popover is open.
  // Re-measuring beats dismissing, which would punish a user who scrolled by
  // one line. Scoped to the open menu, exactly like the dismiss listeners; the
  // scroll listener captures because scroll events don't bubble.
  createEffect(
    () => openMenu(),
    (menu) => {
      if (!menu) {
        applyFlip(false, false);
        return undefined;
      }
      measureFlip(menu);
      const onReflow = (): void => measureFlip(menu);
      window.addEventListener("resize", onReflow);
      window.addEventListener("scroll", onReflow, {
        capture: true,
        passive: true,
      });
      return () => {
        window.removeEventListener("resize", onReflow);
        window.removeEventListener("scroll", onReflow, { capture: true });
      };
    },
  );

  // The entry an opening focuses: the checked item, else the first entry,
  // else the menu container so focus is at least inside the popover for
  // Escape and arrow keys.
  function focusMenuEntry(menu: MenuKind): void {
    const el = menuFor(menu);
    if (!el) return;
    const items = Array.from(
      el.querySelectorAll<HTMLButtonElement>(".bc-menu-item"),
    );
    const preferred = items.find((it) => it.getAttribute("tabindex") === "0");
    (preferred ?? items[0] ?? el).focus();
  }

  // Move focus into the popover when it opens. This cannot be done from a ref
  // on the item: refs fire while their node is still detached, so focus() there
  // is a silent no-op — which also left the roving arrow-key handler below
  // unreachable, since focus never entered the menu. The apply phase runs
  // post-flush (the menu is mounted), and one more microtask lets the rest of
  // this flush's DOM work land first.
  let menuGen = 0;
  createEffect(
    () => openMenu(),
    (menu) => {
      // Bump on every open AND close, so a queued focus for a menu that has
      // since closed (or swapped) is dropped instead of stealing focus back.
      const gen = ++menuGen;
      if (!menu) return undefined;
      queueMicrotask(() => {
        if (gen !== menuGen) return;
        focusMenuEntry(menu);
      });
      return undefined;
    },
  );

  // The collapsed triggers own their navigation keys. A closed trigger is
  // still a plain button, so the arrows would otherwise do whatever the page
  // does with them instead of opening the menu -- unlike every other menu in
  // the app (DropSelect's onTriggerKeydown). Open and consume; opening moves
  // focus into the popover through the effect above.
  function onTriggerKeydown(menu: MenuKind) {
    return (e: KeyboardEvent): void => {
      if (e.isComposing) return;
      switch (e.key) {
        case "ArrowDown":
        case "ArrowUp":
        case "Home":
        case "End":
          e.preventDefault();
          e.stopPropagation();
          if (openMenu() !== menu) setOpenMenu(menu);
          else focusMenuEntry(menu);
          break;
        default:
          break;
      }
    };
  }

  // Escape closes the popover and returns focus to its trigger.
  function onMenuKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    // Composition owns Escape and the arrows while it is active, exactly as
    // in the peer menus, so nothing here acts on a composing keystroke.
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMenu();
      return;
    }
    if (e.key === "Tab") {
      // Tab leaves the menu (WCAG 2.1.2 / APG Menu Button). See Library.tsx.
      closeMenu();
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
    // Roving focus across the items, matching the menu role's keyboard model so
    // arrow keys move between entries. This was dead code until focus actually
    // entered the popover: the items' refs no-oped on detached nodes, so the
    // active element stayed on the chip and these keydowns never arrived.
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
      ref={(el) => (cardEl = el)}
      class="bc-card"
      role="listitem"
      onFocusOut={onCardFocusOut}
      style={{ "--enter-delay": `${enterDelay}ms` }}
    >
      {/* A real button carries native Enter/Space + focus semantics for "open",
			     so the card no longer nests action buttons inside a role="button". */}
      <button
        type="button"
        ref={(el) => (overlayEl = el)}
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
                  <Fleuron />
                </span>
                <span class="bc-ph-title display">{props.book.title}</span>
              </div>
            }
          >
            {/* Eager window = LCP candidates: covers tie at equal display
                sizes and the tie goes to whichever paints first, which is not
                reliably index 0 — mark the whole first screenful. */}
            <img
              src={coverUrl()}
              alt=""
              loading={eager ? "eager" : "lazy"}
              fetchpriority={eager ? "high" : undefined}
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
        ref={(el) => (actionsBtn = el)}
        class="bc-chip-btn bc-actions-btn"
        title="Actions"
        aria-label={`Book actions for ${props.book.title}`}
        aria-haspopup="menu"
        aria-expanded={openMenu() === "actions" ? "true" : "false"}
        aria-controls={openMenu() === "actions" ? actionsMenuId() : undefined}
        onClick={toggleActions}
        onKeyDown={onTriggerKeydown("actions")}
      >
        <Icon icon={Settings} size={15} labelFromParent />
      </button>
      <button
        type="button"
        ref={(el) => (flairBtn = el)}
        class="bc-chip-btn bc-flair-btn"
        title="Set flair"
        aria-label={`Set flair for ${props.book.title}`}
        aria-haspopup="menu"
        aria-expanded={openMenu() === "flair" ? "true" : "false"}
        aria-controls={openMenu() === "flair" ? flairMenuId() : undefined}
        onClick={toggleFlair}
        onKeyDown={onTriggerKeydown("flair")}
      >
        <Icon icon={Tag} size={15} labelFromParent />
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
          ref={(el) => (flairMenuEl = el)}
          id={flairMenuId()}
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
          <button
            type="button"
            class={["bc-menu-item bc-flair-none", noFlair() ? "active" : ""]}
            role="menuitemcheckbox"
            aria-checked={noFlair() ? "true" : "false"}
            tabindex={hasActiveFlair() ? "-1" : "0"}
            onClick={clearFlair}
          >
            <span class="bc-dot bc-dot-none" aria-hidden="true" />
            <span class="bc-menu-label">No flair</span>
            <Show when={noFlair()}>
              <span class="bc-check" aria-hidden="true">
                <Icon icon={Check} size={15} decorative />
              </span>
            </Show>
          </button>
          <For each={props.flairs}>
            {(f) => {
              const isActive = () => props.book.flairId === f.id;
              return (
                <button
                  type="button"
                  class={["bc-menu-item", isActive() ? "active" : ""]}
                  role="menuitemcheckbox"
                  aria-checked={isActive() ? "true" : "false"}
                  tabindex={isActive() ? "0" : "-1"}
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
                      <Icon icon={Check} size={15} decorative />
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
          ref={(el) => (actionsMenuEl = el)}
          id={actionsMenuId()}
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
            onClick={chooseEdit}
          >
            <span class="bc-menu-ico" aria-hidden="true">
              <Icon icon={Pencil} size={15} decorative />
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
              <Icon icon={Share2} size={15} decorative />
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
              <Icon icon={Trash2} size={15} decorative />
            </span>
            <span class="bc-menu-label">Delete</span>
          </button>
        </div>
      </Show>
    </div>
  );
}
