/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Standalone test config, kept separate from vite.config.ts so the build's
// frameScriptPlugin/esbuild pipeline stays out of the test run. Mirrors the
// "~" -> src alias and runs in happy-dom so the cfi/DOM helpers have a document.
export default defineConfig({
  resolve: { alias: { "~": resolve(root, "./src") } },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.{test,spec}.ts"],
    globals: false,
  },
});
