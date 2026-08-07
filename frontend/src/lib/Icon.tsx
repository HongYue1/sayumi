/*
 * Shared icon wrapper for the whole UI. Pass any glyph from ~/lib/icons as the
 * `icon` prop and this enforces one consistent size/stroke and accessibility
 * contract.
 *
 * Icons inherit `currentColor`, so they automatically take the active theme's
 * text colour (set `color: var(--accent)` on the parent to tint an active
 * control). Defaults match the redesign spec: 20px / stroke 1.75.
 *
 * The <svg> shell lives here rather than in icons.ts so it exists once in the
 * bundle rather than once per glyph, and so the geometry file stays pure data.
 * The shell is stroke-only; Tag is the one glyph whose dot overrides that with
 * its own fill. Icon.test.tsx pins both halves - do not tidy up the shell.
 *
 * innerHTML is deliberate: icons.ts is a fixed, local, developer-authored
 * allowlist, so serialising it is one string parse per glyph instead of a
 * component per node. markup() does NOT escape: one double quote in a value
 * breaks out of its attribute and injects another one. icons.test.ts is what
 * enforces that. Never pass anything user-controlled through the `icon` prop.
 */
import type { IconNode } from "~/lib/icons";

interface Props {
  /** A glyph from ~/lib/icons, e.g. `ArrowLeft`. */
  icon: IconNode;
  /** Pixel size of the square glyph. 20 in chrome, 18 in dense rows. */
  size?: number;
  /** Stroke width. */
  stroke?: number;
  /**
   * Accessible label. Provide for a meaningful standalone icon; omit for a
   * decorative icon (it is then hidden from assistive tech). Icon-only buttons
   * should keep their own aria-label and leave this unset.
   */
  label?: string;
  /** Extra class(es) to forward to the underlying <svg>. */
  class?: string;
}

// Serialised once per glyph, not once per render: every call site passes the
// same module-level array, so the second <Icon icon={Search} /> is a map hit.
const serialised = new WeakMap<IconNode, string>();

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
  return (
    <svg
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
      role={props.label ? "img" : undefined}
      aria-label={props.label}
      aria-hidden={props.label ? undefined : "true"}
      innerHTML={markup(props.icon)}
    />
  );
}
