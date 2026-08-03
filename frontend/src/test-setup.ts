import { Window } from "happy-dom";

// Node 22+ ships an experimental Web Storage implementation that defines a
// `localStorage` accessor on the global object. It stays inert unless the
// process was started with --localstorage-file, and because vitest's happy-dom
// environment uses a single object for both `window` and `globalThis`, that
// dead accessor shadows the Storage instance happy-dom installs. sessionStorage
// is unaffected, which is why only localStorage-reading suites ever noticed.
// Reinstall a real Storage so DOM suites behave identically on every Node build.
if (typeof globalThis.localStorage === "undefined") {
  const donor = new Window({ url: "http://localhost/" });
  Object.defineProperty(globalThis, "localStorage", {
    value: donor.localStorage as unknown as Storage,
    configurable: true,
    writable: true,
  });
}
