<script lang="ts">
  import { ui } from "~/lib/ui.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { X } from "@lucide/svelte";
  import { focusTrap } from "~/lib/focusTrap";

  const groups: { title: string; items: { keys: string[]; desc: string }[] }[] =
    [
      {
        title: "Global",
        items: [
          { keys: ["Ctrl", "K"], desc: "Open command palette" },
          { keys: ["?"], desc: "Show this help" },
          { keys: ["Esc"], desc: "Close overlay / panel" },
        ],
      },
      {
        title: "Reader",
        items: [
          { keys: ["←"], desc: "Previous page / chapter" },
          { keys: ["→"], desc: "Next page / chapter" },
          { keys: ["T"], desc: "Table of contents" },
          { keys: ["S"], desc: "Settings" },
          { keys: ["F"], desc: "Search in book" },
          { keys: ["B"], desc: "Toggle bookmark" },
        ],
      },
    ];

  function close(): void {
    ui.shortcuts = false;
  }
  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume the event so the reader's separate window key handler doesn't
      // also act on this Esc and navigate back to the library.
      e.stopImmediatePropagation();
      close();
    }
  }
</script>

<svelte:window onkeydown={ui.shortcuts ? onKeydown : undefined} />

{#if ui.shortcuts}
  <div class="overlay" role="presentation" onclick={close}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="sheet"
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-label="Keyboard shortcuts"
      onclick={(e) => e.stopPropagation()}
      {@attach focusTrap}
    >
      <header>
        <h2>Keyboard shortcuts</h2>
        <button
          class="close"
          aria-label="Close"
          onclick={close}
          {@attach (el) => (el as HTMLButtonElement).focus()}
          ><Icon icon={X} size={18} /></button
        >
      </header>
      <div class="groups">
        {#each groups as g (g.title)}
          <section>
            <p class="eyebrow">{g.title}</p>
            <dl>
              {#each g.items as it (it.desc)}
                <div class="row">
                  <dt>
                    {#each it.keys as k (k)}<kbd>{k}</kbd>{/each}
                  </dt>
                  <dd>{it.desc}</dd>
                </div>
              {/each}
            </dl>
          </section>
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: grid;
    place-items: center;
    padding: 1.5rem;
    background: color-mix(in srgb, #000 38%, transparent);
    animation: ov-in var(--dur-fast) var(--ease-out);
  }
  @keyframes ov-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  .sheet {
    width: min(34rem, 100%);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: 0.75rem;
    animation: sh-in var(--dur) var(--ease-out);
  }
  @keyframes sh-in {
    from {
      opacity: 0;
      transform: translateY(-8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1.1rem;
    border-bottom: 1px solid var(--hairline);
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 500;
  }
  .close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .close:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .close:active {
    transform: scale(0.94);
  }
  .groups {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1.25rem;
    padding: 1.1rem;
  }
  @media (max-width: 30rem) {
    .groups {
      grid-template-columns: 1fr;
    }
  }
  .eyebrow {
    margin: 0 0 0.5rem;
  }
  dl {
    margin: 0;
  }
  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.28rem 0;
  }
  dt {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  dd {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
    text-align: right;
  }
  kbd {
    min-width: 1.4rem;
    padding: 0.1rem 0.4rem;
    border: 1px solid var(--hairline-strong);
    border-bottom-width: 2px;
    border-radius: 0.3rem;
    background: var(--surface);
    font-family: ui-monospace, monospace;
    font-size: 0.75rem;
    text-align: center;
  }
</style>
