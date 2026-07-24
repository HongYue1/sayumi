// Tiny hash-based router. Ported from the legacy Solid router (lib/router.ts).
// The route-matching logic is unchanged; reactivity uses a Svelte 5 `$state`
// rune instead of Solid signals.

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

class Router {
  route = $state<Route>(parseHash());

  constructor() {
    window.addEventListener("hashchange", this.#onHashChange);
  }

  #onHashChange = (): void => {
    this.route = parseHash();
  };

  navigate(path: string): void {
    window.location.hash = path;
  }
}

// App-lifetime singleton — the listener lives as long as the document, so no
// teardown is needed.
export const router = new Router();
