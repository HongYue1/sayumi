import {
  defineConfig,
  transformWithOxc,
  type HmrContext,
  type ModuleNode,
} from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";

const root = dirname(fileURLToPath(import.meta.url));

// Compiles src/iframe/frame.ts into a minified JS string exposed as the virtual
// module `virtual:frame-script`. buildFrameHtml.ts inlines that string into the
// reader iframe's <script> so the engine runs inside the srcdoc sandbox.
function frameScriptPlugin() {
  const virtualId = "virtual:frame-script";
  const resolvedId = "\0" + virtualId;

  return {
    name: "frame-script",
    resolveId(id: string) {
      if (id === virtualId) return resolvedId;
    },
    async load(id: string) {
      if (id !== resolvedId) return;
      const framePath = resolve(root, "src/iframe/frame.ts");
      const source = readFileSync(framePath, "utf-8");
      // OXC infers TypeScript from the `frame.ts` filename (Vite 8 dropped the
      // bundled esbuild that transformWithEsbuild needed).
      const result = await transformWithOxc(source, "frame.ts", {
        minify: true,
        target: "es2022",
      });
      return `export default ${JSON.stringify(result.code)};`;
    },
    handleHotUpdate(ctx: HmrContext) {
      if (ctx.file.endsWith("frame.ts")) {
        const mod = ctx.server.moduleGraph.getModuleById(resolvedId);
        if (mod) return [mod];
      }
      // frame.css changes must invalidate both the virtual module and
      // buildFrameHtml.ts, since both embed the raw CSS string.
      if (ctx.file.endsWith("frame.css")) {
        const modules: ModuleNode[] = [];
        const virtualMod = ctx.server.moduleGraph.getModuleById(resolvedId);
        if (virtualMod) modules.push(virtualMod);
        const buildPath = resolve(root, "src/iframe/buildFrameHtml.ts");
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
      // Marking them external silences the "didn't resolve at build time" notes.
      external: [/^\/fonts\//],
    },
  },
  server: {
    port: 3000,
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
});
