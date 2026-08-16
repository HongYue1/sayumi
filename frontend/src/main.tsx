// SPA entry point. render() takes a thunk returning JSX (not a component),
// mounts it into the target, and returns the app's dispose handle.
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

// The default export is the dispose function render() returns. Nothing
// imports this module -- importing it IS mounting the app -- so the export
// exists to keep that handle reachable: it is what a test drives the entry
// point through, and the only way a future teardown or HMR path could unmount
// cleanly instead of leaking the root.
export default render(() => <App />, target);
