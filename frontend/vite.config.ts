import {
  defineConfig,
  normalizePath,
  type HmrContext,
  type ModuleNode,
  type Plugin,
} from "vite";
import solid from "vite-plugin-solid";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// The Bun global comes from the bun runtime that executes this config via
// `bun run build/dev`. vite.config.ts sits outside tsconfig's src/** include,
// so tsc never checks this file; this minimal declaration keeps editors and
// the type-aware linter honest without pulling in @types/bun.
declare const Bun: {
  build(options: {
    entrypoints: string[];
    format?: "esm" | "cjs" | "iife";
    minify?: boolean;
    target?: "browser" | "bun" | "node";
    root?: string;
  }): Promise<{
    success: boolean;
    logs: Array<{ message: string }>;
    outputs: Array<{ text(): Promise<string> }>;
  }>;
};

// Compiles src/iframe/frame.ts into a minified JS string exposed as the virtual
// module `virtual:frame-script`. buildFrameHtml.ts inlines that string into the
// reader iframe's <script> so the engine runs inside the srcdoc sandbox.
//
// Bun.build replaced esbuild: the toolchain is bun-pinned end to end, so the
// frame bundle uses it too and the esbuild devDep is gone. Differences that
// matter: no metafile (HMR watches the static frame-graph list below), no
// es-version target (Bun emits modern JS, fine inside the es2022 iframe), and
// tsconfig paths (~/) are respected automatically.
function frameScriptPlugin(): Plugin {
  const virtualId = "virtual:frame-script";
  const resolvedId = "\0" + virtualId;
  // Static frame-graph watch list (Bun.build has no metafile). Covers
  // frame.ts's runtime imports: everything in src/iframe plus the two lib
  // modules it pulls in (frameMessages, cfi).
  const iframeDir = normalizePath(resolve(root, "src/iframe")) + "/";
  const frameGraphLibs = ["src/lib/frameMessages.ts", "src/lib/cfi.ts"].map(
    (p) => normalizePath(resolve(root, p)),
  );
  const isFrameInput = (file: string): boolean => {
    const f = normalizePath(file);
    return f.startsWith(iframeDir) || frameGraphLibs.includes(f);
  };

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
      const result = await Bun.build({
        entrypoints: [framePath],
        format: "iife",
        minify: true,
        target: "browser",
        root,
      });
      if (!result.success)
        throw new Error(
          "Bun.build failed for frame.ts: " +
            result.logs.map((l) => l.message).join("\n"),
        );

      return `export default ${JSON.stringify(await result.outputs[0].text())};`;
    },
    handleHotUpdate(ctx: HmrContext) {
      // frame.css changes must invalidate buildFrameHtml.ts, which embeds the
      // raw stylesheet. Checked first: frame.css lives under src/iframe, so it
      // would otherwise be swallowed by the isFrameInput branch below.
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

      // Frame engine sources: invalidate the virtual module so the iframe
      // reloads with a rebuilt bundle.
      if (isFrameInput(ctx.file)) {
        const mod = ctx.server.moduleGraph.getModuleById(resolvedId);
        if (mod) {
          ctx.server.moduleGraph.invalidateModule(mod);
          return [mod];
        }
      }
    },
  };
}

export default defineConfig({
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
      // /fonts/* are served by the Go binary at runtime, not bundled by Vite.
      // Vite 8 bundles with Rolldown, so this block is `rolldownOptions`;
      // the old `rollupOptions` name is silently ignored, which would
      // inline the font URLs back into the bundle.
      external: [/^\/fonts\//],
    },
  },
  server: {
    port: 3000,
    // /fonts is proxied too so the embedded interface/reading fonts served by
    // the Go binary resolve in dev instead of falling back to system fonts.
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/fonts": "http://127.0.0.1:8080",
    },
  },
});
