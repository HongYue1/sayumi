// Tiny hash-based router. Reactivity uses a Solid signal; the route-matching
// logic is unchanged from the Svelte revision it replaces.
//
// Behaviour contract worth keeping: hash parsing treats malformed
// percent-encoded book IDs as invalid routes and falls back to the library,
// rather than throwing during module initialization or a hashchange event.
import { createSignal } from "solid-js";

export interface Route {
  path: string;
  params: Record<string, string>;
}

export function matchRoute(path: string): Route {
  const m = path.match(/^\/read\/([^/]+)$/);
  if (m) {
    try {
      return {
        path: "/read/:id",
        params: { id: decodeURIComponent(m[1]) },
      };
    } catch {
      // A malformed percent escape in a hand-edited or external hash must not
      // throw during module initialization or a hashchange event.
    }
  }
  return { path: "/", params: {} };
}

function parseHash(): Route {
  return matchRoute(window.location.hash.slice(1) || "/");
}

function createRouter() {
  const [route, setRoute] = createSignal<Route>(parseHash());

  // App-lifetime singleton -- the listener lives as long as the document, so no
  // teardown is needed. The handler runs outside any reactive scope, so writing
  // the signal here is a normal external write, not an owned-scope write.
  window.addEventListener("hashchange", () => setRoute(parseHash()));

  return {
    get route(): Route {
      return route();
    },
    navigate(path: string): void {
      window.location.hash = path;
    },
  };
}

export const router = createRouter();
