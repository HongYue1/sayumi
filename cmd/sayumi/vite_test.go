package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise actual Bun/Vite builds and filesystem-driven HMR in disposable
// projects. These test the build graph and messages, not browser layout/CSP.
// Keep graph coverage behavioral: config-source lists cannot prove that a shared
// edit reaches both shell and frame, or that production watch reloads the IIFE.
func TestViteFramePipeline(t *testing.T) {
	frontend, err := filepath.Abs(filepath.Join("..", "..", "frontend"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"build", "dev", "preview", "watch", "syntax", "sidecar"} {
		t.Run(mode, func(t *testing.T) {
			// Vite IDs must not mix a Windows short TEMP alias and its real path.
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for path, source := range map[string]string{
				"package.json":                 `{"type":"module"}`,
				"tsconfig.json":                `{"compilerOptions":{"paths":{"~/*":["./src/*"]}}}`,
				"index.html":                   `<html><body><script type="module" src="/src/main.ts"></script></body></html>`,
				"src/main.ts":                  `import "./frame-client"; import "./shell-client";`,
				"src/frame-client.ts":          `import { payload } from "./iframe/buildFrameHtml"; globalThis.framePayload = payload; if (import.meta.hot) import.meta.hot.accept();`,
				"src/shell-client.ts":          `import { value } from "./lib/shared"; globalThis.shellValue = value; if (import.meta.hot) import.meta.hot.accept();`,
				"src/iframe/buildFrameHtml.ts": `import frame from "virtual:frame-script"; import css from "./frame.css?raw"; export const payload = frame + css;`,
				"src/iframe/frame.css":         `body { --pipeline: css-before; }`,
				"src/iframe/frame.ts":          `import { value } from "~/lib/shared"; import { leaf } from "~/lib/transitive"; globalThis.frameResult = value + ":" + leaf;`,
				"src/lib/shared.ts":            `export const value = "shared-before";`,
				"src/lib/transitive.ts":        `export { leaf } from "../nested/new-dependency";`,
				"src/nested/new-dependency.ts": `export const leaf = "leaf-before";`,
				"src/iframe/image.svg":         `<svg xmlns="http://www.w3.org/2000/svg"/>`,
			} {
				writeProvisionFile(t, dir, path, []byte(source+"\n"))
			}
			driver := fmt.Sprintf("import config from %q;\nimport { build, createServer, preview } from %q;\nconst root = %q;\nconst mode = %q;\n",
				filepath.ToSlash(filepath.Join(frontend, "vite.config.ts")),
				filepath.ToSlash(filepath.Join(frontend, "node_modules", "vite", "dist", "node", "index.js")),
				filepath.ToSlash(dir), mode) + vitePipelineDriver
			writeProvisionFile(t, dir, "pipeline.mjs", []byte(driver))
			got := runProvisionCommand(t, frontend, map[string]string{"CI": "", "GITHUB_ACTIONS": ""}, "bun", filepath.Join(dir, "pipeline.mjs"))
			if got.code != 0 || !strings.Contains(got.output, "pipeline "+mode+" passed") {
				t.Fatalf("Vite %s contract failed: exit=%d\n%s", mode, got.code, got.output)
			}
		})
	}
}

func TestViteRejectsNode(t *testing.T) {
	got := runProvisionCommand(t, filepath.Join("..", "..", "frontend"), nil, "node", frontendToolBin(t, "vite", "vite.js"), "build")
	if got.code == 0 || !strings.Contains(got.output, "iframe build requires Bun") {
		t.Fatalf("Node must fail before starting the build: exit=%d\n%s", got.code, got.output)
	}
}

const vitePipelineDriver = `
import assert from "node:assert/strict";
import { readFile, readdir, writeFile, unlink } from "node:fs/promises";
import { join } from "node:path";
import { Script } from "node:vm";
import { createServer as createHTTPServer } from "node:http";

const outDir = join(root, "out");
const options = {
  ...config,
  configFile: false,
  root,
  cacheDir: join(root, "cache"),
  logLevel: "silent",
  resolve: { ...config.resolve, alias: { "~": join(root, "src") } },
  build: { ...config.build, outDir },
  server: { ...config.server, host: "127.0.0.1", port: 0, strictPort: true, proxy: {} },
};
const framePath = join(root, "src/iframe/frame.ts");
const leafPath = join(root, "src/nested/new-dependency.ts");
const frameURL = "/@id/__x00__virtual:frame-script";
const deadline = (promise, label) => {
  let timer;
  return Promise.race([
    promise,
    new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("timed out: " + label)), 8000); }),
  ]).finally(() => clearTimeout(timer));
};
async function emittedJS() {
  const files = await readdir(join(outDir, "assets"));
  return (await Promise.all(files.filter(f => f.endsWith(".js")).map(f => readFile(join(outDir, "assets", f), "utf8")))).join("\n");
}
function requireMarkers(text, ...markers) {
  for (const marker of markers) assert.ok(text.includes(marker), "missing marker: " + marker);
}

if (mode === "syntax" || mode === "sidecar") {
  await writeFile(framePath, mode === "syntax" ? "export const = ;" : 'import image from "./image.svg"; globalThis.image = image;');
  await assert.rejects(() => build(options), mode === "syntax" ? /frame\.ts/ : /one self-contained JavaScript IIFE/);
} else if (mode === "build") {
  await build(options);
  const html = await readFile(join(outDir, "index.html"), "utf8");
  requireMarkers(html, "/assets/");
  const js = await emittedJS();
  requireMarkers(js, "shared-before", "leaf-before", "css-before");
  assert.ok(!js.includes("virtual:frame-script"), "unresolved virtual module shipped");
} else if (mode === "preview") {
  await build(options);
  // Redirect only the configured proxy routes to an isolated fake Go backend.
  // Exercise dev and preview without touching a running user's port 8080 server.
  const upstream = createHTTPServer((request, response) => response.end("backend " + request.url));
  await new Promise(resolve => upstream.listen(0, "127.0.0.1", resolve));
  const target = "http://127.0.0.1:" + upstream.address().port;
  const proxy = Object.fromEntries(Object.keys(config.server.proxy).map(route => [route, target]));
  try {
    for (const start of [createServer, preview]) {
      const server = await start({
        ...options,
        server: { ...options.server, proxy },
        preview: { host: "127.0.0.1", port: 0, strictPort: true },
      });
      try {
        if (start === createServer) await server.listen();
        const origin = "http://127.0.0.1:" + server.httpServer.address().port;
        const get = async path => {
          const response = await fetch(origin + path, { signal: AbortSignal.timeout(8000) });
          assert.equal(response.status, 200, path);
          return response.text();
        };
        for (const path of ["/api/pipeline", "/fonts/pipeline.woff2"]) assert.equal(await get(path), "backend " + path);
        const html = await get("/");
        if (start === preview) {
          assert.ok(!html.includes("/@vite/client"));
          const asset = html.match(/src="([^"]+\.js)"/)[1];
          requireMarkers(await get(asset), "leaf-before", "shared-before");
        }
      } finally {
        await server.close();
      }
    }
  } finally {
    await new Promise(resolve => upstream.close(resolve));
  }
} else if (mode === "watch") {
  const watcher = await build({ ...options, build: { ...options.build, watch: {} } });
  let pending;
  const events = [];
  watcher.on("event", event => {
    if (event.code === "ERROR") { if (pending) pending.reject(event.error); else events.push(event); }
    if (event.code === "END") { if (pending) pending.resolve(); else events.push(event); }
  });
  const nextBuild = () => {
    const prior = events.shift();
    if (prior) return prior.code === "ERROR" ? Promise.reject(prior.error) : Promise.resolve();
    pending = Promise.withResolvers();
    return deadline(pending.promise, "watch build").finally(() => { pending = undefined; });
  };
  try {
    await nextBuild();
    requireMarkers(await emittedJS(), "leaf-before");
    const rebuilt = nextBuild();
    await writeFile(leafPath, 'export const leaf = "leaf-watch-after";');
    await rebuilt;
    const js = await emittedJS();
    requireMarkers(js, "leaf-watch-after");
    assert.ok(!js.includes("leaf-before"), "watch kept a stale iframe bundle");
  } finally {
    await watcher.close();
  }
} else if (mode === "dev") {
  const server = await createServer(options);
  let pending;
  const send = server.ws.send.bind(server.ws);
  server.ws.send = (payload, ...args) => {
    if (pending && payload && typeof payload === "object" && ["update", "full-reload", "error"].includes(payload.type)) pending.resolve(payload);
    return send(payload, ...args);
  };
  try {
    await server.listen();
    const origin = "http://127.0.0.1:" + server.httpServer.address().port;
    const get = async path => {
      const response = await fetch(origin + path, { signal: AbortSignal.timeout(8000) });
      const text = await response.text();
      if (!response.ok) throw new Error(text);
      return text;
    };
    const frame = async () => {
      const code = await get(frameURL);
      const module = await import("data:text/javascript;base64," + Buffer.from(code).toString("base64"));
      const context = {};
      new Script(module.default).runInNewContext(context);
      return context.frameResult;
    };
    const change = async action => {
      assert.equal(pending, undefined);
      pending = Promise.withResolvers();
      const message = deadline(pending.promise, "filesystem HMR");
      try { await action(); return await message; }
      finally { pending = undefined; }
    };
    const boundary = (message, path) => {
      assert.equal(message.type, "update", JSON.stringify(message));
      assert.ok(message.updates.some(update => update.path === path), "missing HMR boundary " + path + ": " + JSON.stringify(message));
    };
    // Prime the same modules a browser loads, including separate accepting
    // boundaries for the shell and frame so one cannot mask the other's update.
    for (const path of ["/src/main.ts", "/src/frame-client.ts", "/src/shell-client.ts", "/src/iframe/buildFrameHtml.ts", "/src/lib/shared.ts", "/src/iframe/frame.css?raw"]) await get(path);
    assert.equal(await frame(), "shared-before:leaf-before");
    const leafMessage = await change(() => writeFile(leafPath, 'export const leaf = "leaf-after";'));
    boundary(leafMessage, "/src/frame-client.ts");
    assert.equal(await frame(), "shared-before:leaf-after");
    const sharedMessage = await change(() => writeFile(join(root, "src/lib/shared.ts"), 'export const value = "shared-after";'));
    boundary(sharedMessage, "/src/frame-client.ts");
    boundary(sharedMessage, "/src/shell-client.ts");
    assert.equal(await frame(), "shared-after:leaf-after");
    requireMarkers(await get("/src/lib/shared.ts"), "shared-after");
    const cssMessage = await change(() => writeFile(join(root, "src/iframe/frame.css"), "body { --pipeline: css-after; }"));
    boundary(cssMessage, "/src/frame-client.ts");
    requireMarkers(await get("/src/iframe/frame.css?raw"), "css-after");
    const wrapperPath = join(root, "src/iframe/buildFrameHtml.ts");
    const wrapper = await readFile(wrapperPath, "utf8");
    const wrapperMessage = await change(() => writeFile(wrapperPath, wrapper.replace("frame + css", 'frame + css + "wrapper-after"')));
    boundary(wrapperMessage, "/src/frame-client.ts");
    requireMarkers(await get("/src/iframe/buildFrameHtml.ts"), "wrapper-after");
    await change(() => unlink(leafPath));
    await assert.rejects(frame, /new-dependency|Bun\.build/);
    await change(() => writeFile(leafPath, 'export const leaf = "leaf-restored";'));
    assert.equal(await frame(), "shared-after:leaf-restored");
  } finally {
    await server.close();
  }
}
console.log("pipeline " + mode + " passed");
`
