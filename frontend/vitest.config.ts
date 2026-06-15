/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Standalone test config, kept separate from vite.config.ts so the build's
// frameScriptPlugin/esbuild pipeline stays out of the test run. The svelte
// plugin is needed so the `.svelte.ts` rune stores (toast/library) compile their
// $state/$derived; tests themselves are plain `.test.ts` that read the compiled
// stores. `conditions: ["browser"]` forces Svelte's client build so runes behave
// as they do in the app (not the SSR build).
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: { "~": resolve(root, "./src") },
    conditions: ["browser"],
  },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.{test,spec}.ts"],
    globals: false,
  },
});
