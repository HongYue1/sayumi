// SPA entry point. Ported from main.ts: Svelte's mount() becomes Solid's
// render(), which takes a thunk returning JSX rather than a component plus a
// target option bag. The returned value is a dispose function.
import { render } from "@solidjs/web";
import "~/app.css";
import App from "~/App";

const target = document.getElementById("app");
if (!target) throw new Error("#app root element not found");

export default render(() => <App />, target);
