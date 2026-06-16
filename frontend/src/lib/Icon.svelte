<script lang="ts">
  /*
   * Shared icon wrapper for the whole UI. Pass any Lucide icon component
   * (e.g. `import { ArrowLeft } from "@lucide/svelte"`) as the `icon` prop and
   * this enforces one consistent size/stroke and accessibility contract.
   *
   * Icons inherit `currentColor`, so they automatically take the active
   * theme's text colour (set `color: var(--accent)` on the parent to tint an
   * active control). Defaults match the redesign spec: 20px / stroke 1.75.
   *
   * This file intentionally has no import from @lucide/svelte — the icon is
   * injected by the caller — so it stays dependency-free and the build never
   * breaks if the icon package isn't installed yet.
   */
  import type { Component } from "svelte";

  interface Props {
    /** A Lucide (or compatible) icon component, e.g. `ArrowLeft`. */
    icon: Component<any>;
    /** Pixel size of the square glyph. 20 in chrome, 18 in dense rows. */
    size?: number;
    /** Stroke width. */
    stroke?: number;
    /**
     * Accessible label. Provide for a meaningful standalone icon; omit for a
     * decorative icon (it is then hidden from assistive tech). Icon-only
     * buttons should keep their own aria-label and leave this unset.
     */
    label?: string;
    /** Extra class(es) to forward to the underlying <svg>. */
    class?: string;
  }

  let {
    icon: Glyph,
    size = 20,
    stroke = 1.75,
    label,
    class: klass = "",
  }: Props = $props();
</script>

<Glyph
  {size}
  strokeWidth={stroke}
  class={klass}
  role={label ? "img" : undefined}
  aria-label={label}
  aria-hidden={label ? undefined : "true"}
/>
