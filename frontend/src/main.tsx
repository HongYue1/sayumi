// SPA entry point. Ported from main.ts: Svelte's mount() becomes Solid's
// render(), which takes a thunk returning JSX rather than a component plus a
// target option bag. The returned value is a dispose function.
import { render } from "@solidjs/web";
import "~/app.css";
import App from "~/App";
import { fontRegistry } from "~/lib/fontRegistry";

const target = document.getElementById("app");
if (!target) throw new Error("#app root element not found");

// A server restart re-mints the user-font token behind a surviving session,
// leaving every cached /fonts/user/ URL 404ing with no other heal short of a
// page reload — so the registry refetches on the recovery edge. Wired here,
// once, at the entry point. Known and accepted: a restart that completes
// inside one OfflineBanner heartbeat, or entirely while the tab is hidden,
// produces no edge and stays stale; closing that window needs a server-stable
// token, not a client fix.
fontRegistry.watchReachability();

export default render(() => <App />, target);
