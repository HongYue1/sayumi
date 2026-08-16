// Global transient-feedback toasts, mounted once by App.tsx. State, timing,
// and the exit handshake live in ~/lib/toast; this component is only the
// renderer.
//
// All motion stays in CSS (app.css): enter via the mount animation each
// freshly keyed element runs, exit via the `exiting` class the store sets
// EXIT_MS before removal. The global prefers-reduced-motion switch therefore
// covers everything -- no JS motion check is needed here.
import { For } from "solid-js";
import { toast } from "~/lib/toast";

export default function Toaster() {
  return (
    <div
      class="toaster"
      role="log"
      aria-label="Notifications"
      aria-live="polite"
      aria-relevant="additions"
    >
      <For each={toast.items}>
        {(item) => (
          <div class={["toast", { exiting: item.exiting }]}>{item.message}</div>
        )}
      </For>
    </div>
  );
}
