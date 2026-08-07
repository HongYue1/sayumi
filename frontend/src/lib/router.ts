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

export interface Router {
  readonly route: Route;
  /** False when the current hash already equals the path: no hashchange
   *  fires, so the navigate is a no-op the caller may want to detect. */
  navigate(path: string): boolean;
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

// Value equality for the route signal: parseHash builds a fresh object per
// hashchange, and reference equality would publish identical routes to every
// consumer. The option is SignalOptions.equals (@solidjs/signals), grounded
// in the installed .d.ts, not memory. Shallow on params is exact today,
// where routes carry at most { id }.
function sameRoute(a: Route, b: Route): boolean {
  if (a.path !== b.path) return false;
  const aKeys = Object.keys(a.params);
  const bKeys = Object.keys(b.params);
  return (
    aKeys.length === bKeys.length &&
    aKeys.every((k) => a.params[k] === b.params[k])
  );
}

/**
 * Builds a router over window.location.hash.
 *
 * Exported for tests only. The singleton below is constructed at import time,
 * which leaves parseHash, the hashchange wiring and navigate unreachable from
 * any suite unless the constructor itself can be called. Application code must
 * use `router` -- every extra instance attaches its own permanent listener.
 */
export function createRouter(): Router {
  const [route, setRoute] = createSignal<Route>(parseHash(), {
    equals: sameRoute,
  });

  // App-lifetime singleton -- the listener lives as long as the document, so no
  // teardown is needed. The handler runs outside any reactive scope, so writing
  // the signal here is a normal external write, not an owned-scope write.
  window.addEventListener("hashchange", () => setRoute(parseHash()));

  return {
    get route(): Route {
      return route();
    },
    navigate(path: string): boolean {
      // Assigning the hash it already has fires no hashchange anywhere (the
      // suite pins this), so the call would be a silent no-op. Report it
      // instead of swallowing it: a caller whose intent is already satisfied
      // (the SettingsPanel specimen button) runs its own fallback on false.
      // The comparison is like-for-like — location.hash reads back raw, and
      // the /read/ call sites pass encodeURIComponent'd paths.
      if (window.location.hash.slice(1) === path) return false;
      window.location.hash = path;
      return true;
    },
  };
}

export const router = createRouter();
