import {
  defineConfig,
  normalizePath,
  type HmrContext,
  type ModuleNode,
  type Plugin,
} from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { build as esbuild } from "esbuild";
import { resolve, dirname, isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Compiles src/iframe/frame.ts into a minified JS string exposed as the virtual
// module `virtual:frame-script`. buildFrameHtml.ts inlines that string into the
// reader iframe's <script> so the engine runs inside the srcdoc sandbox.
function frameScriptPlugin(): Plugin {
  const virtualId = "virtual:frame-script";
  const resolvedId = "\0" + virtualId;
  const watchedInputs = new Set<string>();

  return {
    name: "frame-script",
    resolveId(id: string) {
      if (id === virtualId) return resolvedId;
    },
    async load(id: string) {
      if (id !== resolvedId) return;
      const framePath = resolve(root, "src/iframe/frame.ts");
      // Bundle frame.ts into a self-contained IIFE because it runs inside the
      // reader srcdoc as a classic script, not a JavaScript module.
      const result = await esbuild({
        entryPoints: [framePath],
        bundle: true,
        format: "iife",
        minify: true,
        target: "es2022",
        platform: "browser",
        legalComments: "none",
        write: false,
        metafile: true,
        absWorkingDir: root,
        alias: { "~": resolve(root, "src") },
      });

      const metafile = result.metafile;
      if (!metafile)
        throw new Error("esbuild did not return dependency metadata");

      watchedInputs.clear();
      for (const input of Object.keys(metafile.inputs)) {
        if (input.startsWith("<")) continue;
        const inputPath = isAbsolute(input) ? input : resolve(root, input);
        this.addWatchFile(inputPath);
        watchedInputs.add(normalizePath(inputPath));
      }

      return `export default ${JSON.stringify(result.outputFiles[0].text)};`;
    },
    handleHotUpdate(ctx: HmrContext) {
      if (watchedInputs.has(normalizePath(ctx.file))) {
        const mod = ctx.server.moduleGraph.getModuleById(resolvedId);
        if (mod) {
          ctx.server.moduleGraph.invalidateModule(mod);
          return [mod];
        }
      }

      // frame.css changes must invalidate buildFrameHtml.ts, which embeds the
      // raw stylesheet. Keep the virtual module invalidated as a full iframe
      // refresh signal for the consuming component.
      if (ctx.file.endsWith("frame.css")) {
        const modules: ModuleNode[] = [];
        const virtualMod = ctx.server.moduleGraph.getModuleById(resolvedId);
        if (virtualMod) {
          ctx.server.moduleGraph.invalidateModule(virtualMod);
          modules.push(virtualMod);
        }
        const buildPath = normalizePath(
          resolve(root, "src/iframe/buildFrameHtml.ts"),
        );
        const buildMods = ctx.server.moduleGraph.getModulesByFile(buildPath);
        if (buildMods) modules.push(...buildMods);
        return modules.length > 0 ? modules : undefined;
      }
    },
  };
}

export default defineConfig({
  plugins: [svelte(), frameScriptPlugin()],
  resolve: {
    alias: { "~": resolve(root, "./src") },
  },
  build: {
    // Output straight into the Go binary's embed directory.
    outDir: resolve(root, "../cmd/sayumi/dist"),
    emptyOutDir: true,
    target: "es2022",
    reportCompressedSize: false,
    rollupOptions: {
      // /fonts/* are served by the Go binary at runtime, not bundled by Vite.
      external: [/^\/fonts\//],
    },
  },
  server: {
    port: 3000,
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
});
