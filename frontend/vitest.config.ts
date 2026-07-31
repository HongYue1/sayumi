/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import solid from "vite-plugin-solid";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Standalone test config, kept separate from vite.config.ts so the build's
// frameScriptPlugin/esbuild pipeline stays out of the test run. The solid plugin
// is required even for non-JSX store tests: babel-preset-solid rewrites the
// signal/store surface, and a plain esbuild transform would leave the tests
// running against a different reactive graph than the app. `conditions:
// ["browser"]` forces Solid's client build so effects flush as they do in the
// app instead of taking the SSR no-op path.
export default defineConfig({
  plugins: [solid()],
  resolve: {
    alias: {
      "~": resolve(root, "./src"),
      // @solidjs/testing-library still imports the pre-2.0 "solid-js/web"
      // specifier, which no longer exists; map it onto the real package so
      // component tests resolve the same renderer the app uses.
      "solid-js/web": "@solidjs/web",
    },
    conditions: ["browser"],
  },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    globals: false,
  },
});
