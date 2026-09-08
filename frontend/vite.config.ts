import { defineConfig, normalizePath, type Plugin } from "vite";
import solid from "@solidjs/vite-plugin";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Keep the build-only Bun API out of the browser application's global types.
// The installed runtime is selected explicitly by the package scripts.
declare const Bun: {
  build(options: {
    entrypoints: string[];
    format: "iife";
    minify: boolean;
    target: "browser";
    root: string;
    metafile: true;
    throw: false;
  }): Promise<{
    success: boolean;
    logs: Array<{ message: string }>;
    outputs: Array<{ kind: string; type: string; text(): Promise<string> }>;
    metafile?: {
      inputs: Record<string, unknown>;
      outputs: Record<string, { imports: unknown[] }>;
    };
  }>;
};

// A srcdoc classic script must be one self-contained IIFE. Vite owns the shell
// and raw frame.css; Bun owns only frame.ts and its transitive runtime imports.
function frameScriptPlugin(): Plugin {
  const virtualId = "virtual:frame-script";
  const resolvedId = "\0" + virtualId;
  let frameRoot = root;
  return {
    name: "frame-script",
    configResolved(config) {
      if (typeof Bun === "undefined")
        throw new Error(
          "The iframe build requires Bun; use bun run dev, build or preview.",
        );
      frameRoot = config.root;
    },
    resolveId(id: string) {
      if (id === virtualId) return resolvedId;
    },
    async load(id: string) {
      if (id !== resolvedId) return;
      const result = await Bun.build({
        entrypoints: [resolve(frameRoot, "src/iframe/frame.ts")],
        format: "iife",
        minify: true,
        target: "browser",
        root: frameRoot,
        metafile: true,
        // Otherwise Bun rejects with AggregateError and Vite loses its details.
        throw: false,
      });
      if (!result.success)
        throw new Error(
          "Bun.build failed for frame.ts: " +
            result.logs.map((l) => l.message).join("\n"),
        );
      const [output] = result.outputs;
      if (
        result.outputs.length !== 1 ||
        output.kind !== "entry-point" ||
        !output.type.startsWith("text/javascript") ||
        !result.metafile ||
        Object.values(result.metafile.outputs).some((o) => o.imports.length > 0)
      )
        throw new Error(
          "frame.ts must produce one self-contained JavaScript IIFE, with no external imports or sidecar assets.",
        );

      // Bun's metafile paths are relative to the build process's cwd, not root.
      // addWatchFile connects these real inputs to the virtual module in both
      // Vite dev and production watch mode. Let Vite propagate the whole graph:
      // filtering HMR to just the IIFE would lose a shared module's shell update.
      // https://bun.sh/docs/bundler#metafile
      for (const input of Object.keys(result.metafile.inputs)) {
        // Bun may spell cross-drive inputs as ../../C:/... or /C:/....
        // Recover the drive path; keep ordinary cwd-relative inputs unchanged.
        const nativeInput =
          process.platform === "win32"
            ? input.replace(/^(?:\/|(?:\.\.\/)+)([a-z]:\/)/i, "$1")
            : input;
        this.addWatchFile(normalizePath(resolve(nativeInput)));
      }

      return `export default ${JSON.stringify(await output.text())};`;
    },
  };
}

export default defineConfig({
  root,
  plugins: [solid(), frameScriptPlugin()],
  resolve: {
    alias: { "~": resolve(root, "./src") },
  },
  build: {
    // Output straight into the Go binary's embed directory.
    outDir: resolve(root, "../cmd/sayumi/dist"),
    emptyOutDir: true,
    target: "es2022",
    reportCompressedSize: false,
    rolldownOptions: {
      // /fonts/* are served by Go at runtime, not bundled by Vite/Rolldown.
      external: [/^\/fonts\//],
    },
  },
  server: {
    port: 3000,
    // Both the API and embedded fonts must have the same owner as production.
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/fonts": "http://127.0.0.1:8080",
    },
  },
});
