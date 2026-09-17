/*
 * Shared icon wrapper for the whole UI. Pass any glyph from ~/lib/icons as the
 * `icon` prop and this enforces one consistent size/stroke and accessibility
 * contract.
 *
 * Icons inherit `currentColor`, so they automatically take the active theme's
 * text colour (set `color: var(--accent)` on the parent to tint an active
 * control). The defaults (20px / stroke 1.75) are pinned by Icon.test.tsx.
 *
 * The <svg> shell lives here rather than in icons.ts so it exists once in the
 * bundle rather than once per glyph, and so the geometry file stays pure data.
 * The shell is stroke-only; Tag is the one glyph whose dot overrides that with
 * its own fill. Icon.test.tsx pins both halves - do not tidy up the shell.
 *
 * innerHTML is deliberate: icons.ts is a fixed, local, developer-authored
 * allowlist, so serialising it is one string parse per glyph instead of a
 * component per node. markup() does NOT escape: one double quote in a value
 * breaks out of its attribute and injects another one. Two suites are the
 * whole guard: icons.test.ts rejects a breakout character in anything
 * ~/lib/icons exports, and Icon.test.tsx pins that every call site takes its
 * glyph from that module -- the first check only covers what the module
 * itself holds. Never pass anything user-controlled through the `icon` prop.
 */
import type { IconNode } from "~/lib/icons";

interface CommonProps {
  /** A glyph from ~/lib/icons, e.g. `ArrowLeft`. */
  icon: IconNode;
  /** Pixel size of the square glyph. 20 in chrome, 18 in dense rows. */
  size?: number;
  /** Stroke width. */
  stroke?: number;
  /** Extra class(es) to forward to the underlying <svg>. */
  class?: string;
}

type AccessibilityIntent =
  | {
      /** Accessible name for a meaningful standalone icon. */
      label: string;
      decorative?: never;
      labelFromParent?: never;
    }
  | {
      /** Hidden glyph beside text or inside non-interactive decoration. */
      decorative: true;
      label?: never;
      labelFromParent?: never;
    }
  | {
      /** Hidden glyph whose icon-only control owns the accessible name. */
      labelFromParent: true;
      label?: never;
      decorative?: never;
    };

type Props = CommonProps & AccessibilityIntent;

// Serialised once per glyph, not once per render: every call site passes the
// same module-level array, so the second <Icon icon={Search} /> is a map hit.
const serialised = new WeakMap<IconNode, string>();

type HiddenIconIntent = "decorative" | "label-from-parent";

const INTERACTIVE_ANCESTOR = [
  "button",
  "a[href]",
  '[role="button"]',
  '[role="link"]',
  '[role="menuitem"]',
  '[role="menuitemcheckbox"]',
  '[role="menuitemradio"]',
  '[role="tab"]',
].join(",");

const BUTTON_INPUT_TYPES = new Set(["submit", "button", "reset"]);

// Text nodes are not the only content that can name a control: an <img alt>,
// a button-style <input value> and a nested aria-label all feed the parent's
// accessible name. Counting text alone made a decorative icon next to a
// captioned cover look like the control's only content, so the audit warned
// about markup that was already correct.
function namesItself(node: Element): boolean {
  if ((node.getAttribute("aria-label") ?? "").trim() !== "") return true;
  const tag = node.tagName.toLowerCase();
  const type = (node.getAttribute("type") ?? "").toLowerCase();
  const imageLike =
    tag === "img" || tag === "area" || (tag === "input" && type === "image");
  if (imageLike) return (node.getAttribute("alt") ?? "").trim() !== "";
  if (tag === "input" && BUTTON_INPUT_TYPES.has(type)) {
    return (node.getAttribute("value") ?? "").trim() !== "";
  }
  return false;
}

function hasExposedText(
  node: Node,
  icon: SVGSVGElement,
  control = false,
): boolean {
  if (node === icon) return false;
  if (node.nodeType === 3) return (node.textContent ?? "").trim() !== "";
  if (node instanceof Element) {
    if (node.getAttribute("aria-hidden") === "true") return false;
    // The control's own aria-label is its explicit name, not content inside
    // it -- that distinction is what the labelFromParent check rests on.
    if (!control && namesItself(node)) return true;
  }
  return [...node.childNodes].some((child) => hasExposedText(child, icon));
}

function hasExplicitName(control: Element): boolean {
  if ((control.getAttribute("aria-label") ?? "").trim() !== "") return true;
  return (control.getAttribute("aria-labelledby") ?? "").trim() !== "";
}

/** Development-only audit for the parent half of a hidden icon contract. */
function auditHiddenIconControl(
  icon: SVGSVGElement,
  intent: HiddenIconIntent,
): void {
  queueMicrotask(() => {
    const control = icon.closest(INTERACTIVE_ANCESTOR);
    const hasText = control !== null && hasExposedText(control, icon, true);
    const valid =
      intent === "decorative"
        ? control === null || hasText
        : control !== null && !hasText && hasExplicitName(control);
    if (valid) return;

    console.warn(
      intent === "decorative"
        ? "[Icon] A decorative icon is the only accessible content of a control. Add an explicit control label and use labelFromParent."
        : "[Icon] labelFromParent requires an icon-only control with a non-empty aria-label or aria-labelledby.",
      control ?? icon,
    );
  });
}

function markup(node: IconNode): string {
  const hit = serialised.get(node);
  if (hit !== undefined) return hit;
  const out = node
    .map(
      ([tag, attrs]) =>
        "<" +
        tag +
        Object.entries(attrs)
          .map(([name, value]) => " " + name + '="' + value + '"')
          .join("") +
        "/>",
    )
    .join("");
  serialised.set(node, out);
  return out;
}

export default function Icon(props: Props) {
  // A blank label is not a label. Deciding role and aria-hidden by truthiness
  // turned `label=""` into a hidden glyph -- the opposite of what a caller
  // asking for a name wants -- and neither audit branch covered it.
  const named = () => (props.label ?? "").trim() !== "";
  return (
    <svg
      ref={
        import.meta.env.DEV
          ? (element) => {
              if (props.labelFromParent) {
                auditHiddenIconControl(element, "label-from-parent");
              } else if (props.decorative) {
                auditHiddenIconControl(element, "decorative");
              } else if (!named()) {
                console.warn(
                  "[Icon] label must be a non-empty string. Use decorative or labelFromParent for a glyph that should stay hidden.",
                  element,
                );
              }
            }
          : undefined
      }
      xmlns="http://www.w3.org/2000/svg"
      width={props.size ?? 20}
      height={props.size ?? 20}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width={props.stroke ?? 1.75}
      stroke-linecap="round"
      stroke-linejoin="round"
      class={props.class}
      role={named() ? "img" : undefined}
      aria-label={named() ? props.label : undefined}
      aria-hidden={named() ? undefined : "true"}
      innerHTML={markup(props.icon)}
    />
  );
}
