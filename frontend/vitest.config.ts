import { defineConfig } from "vitest/config";
import solid from "@solidjs/vite-plugin";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Standalone test config, kept separate from vite.config.ts so the build's
// frameScriptPlugin (Bun.build of the frame engine) stays out of the test run. The solid plugin
// compiles the .tsx suites and adds the `development` resolve condition, so the
// tests run against the same solid-js dev build and reactive graph as the app.
// `conditions: ["browser"]` forces Solid's client build so effects flush as
// they do in the app instead of taking the SSR no-op path.
export default defineConfig({
  plugins: [solid()],
  resolve: {
    alias: {
      "~": resolve(root, "./src"),
    },
    conditions: ["browser"],
  },
  test: {
    environment: "happy-dom",
    // Node 22+ defines an inert `localStorage` global that shadows the Storage
    // happy-dom installs, so DOM suites that read it see undefined. The setup
    // file reinstalls a working Storage before any suite runs.
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    globals: false,
  },
});
