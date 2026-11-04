import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:net";
import { access, mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { constants } from "node:fs";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";
import path from "node:path";
import { chromium } from "@playwright/test";

const root = fileURLToPath(new URL("../../", import.meta.url));
// Same thread budget as the reviewed M3B runner: Vite, PostgreSQL, the fixture
// and three Chromium contexts share one cgroup of 256 threads.
const env = { ...process.env, GOMAXPROCS: "1", RAYON_NUM_THREADS: "1", UV_THREADPOOL_SIZE: "1", TMPDIR: "/tmp", GOTMPDIR: path.join(root, ".paseo-build-tmp") };
const children = [];
let fixtureDir;
function start(command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, env, stdio: ["ignore", "pipe", "inherit"], ...options });
  children.push(child);
  return child;
}
function finish(child) {
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => code === 0 ? resolve() : reject(new Error(`processo falhou: ${code ?? signal}`)));
  });
}
function childGone(child) {
  return new Promise(resolve => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolve();
      return;
    }
    child.once("exit", resolve);
  });
}
async function stop() {
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const ended = childGone(child);
    child.kill("SIGTERM");
    const timer = setTimeout(() => child.kill("SIGKILL"), 8_000);
    await Promise.race([ended, new Promise(resolve => setTimeout(resolve, 12_000))]);
    clearTimeout(timer);
  }
  if (fixtureDir) await rm(fixtureDir, { recursive: true, force: true });
}
for (const signal of ["SIGINT", "SIGTERM"]) process.once(signal, () => { void stop().then(() => process.exit(1)); });
try {
  if (Object.keys(env).some(key => /^CHATISP_.*DATABASE_URL$/.test(key))) throw new Error("M4B recusa DSNs externos");
  if (!env.CHATISP_E2E_API_PORT) {
    env.CHATISP_E2E_API_PORT = String(await new Promise((resolve, reject) => {
      const probe = createServer();
      probe.once("error", reject);
      probe.listen(0, "127.0.0.1", () => {
        const address = probe.address();
        const port = address && typeof address === "object" ? address.port : 0;
        probe.close(() => resolve(port));
      });
    }));
  }
  process.env.CHATISP_E2E_API_PORT = env.CHATISP_E2E_API_PORT;
  console.log(`M4B API loopback: 127.0.0.1:${env.CHATISP_E2E_API_PORT}`);
  const pinned = spawnSync("taskset", ["-pc", "0,1", String(process.pid)], { encoding: "utf8" });
  if (pinned.status !== 0) throw new Error(`afinidade de CPU indisponível: ${pinned.stderr || pinned.error?.message || pinned.status}`);
  await mkdir(env.GOTMPDIR, { recursive: true });
  const libDir = path.join(root, ".paseo-build-tmp/browser/flat-lib");
  try {
    await access(libDir, constants.R_OK);
    const prefix = `${libDir}:${path.join(libDir, "x86_64-linux-gnu")}`;
    process.env.LD_LIBRARY_PATH = process.env.LD_LIBRARY_PATH ? `${prefix}:${process.env.LD_LIBRARY_PATH}` : prefix;
    env.LD_LIBRARY_PATH = process.env.LD_LIBRARY_PATH;
    const fontDir = path.join(root, ".paseo-build-tmp/browser/lib/usr/share/fonts");
    const fontConfig = path.join(root, ".paseo-build-tmp/browser/fonts.conf");
    try {
      await access(fontDir, constants.R_OK);
      await mkdir(path.join(root, ".paseo-build-tmp/browser/font-cache"), { recursive: true });
      await writeFile(fontConfig, `<?xml version="1.0"?>\n<fontconfig><dir>${fontDir}</dir><cachedir>${path.join(root, ".paseo-build-tmp/browser/font-cache")}</cachedir></fontconfig>\n`);
      process.env.FONTCONFIG_FILE = fontConfig;
      env.FONTCONFIG_FILE = fontConfig;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  const staged = path.join(root, ".paseo-build-tmp/browser/chromium-launcher");
  const homeCache = path.join(homedir(), ".cache/ms-playwright");
  // An explicit executable is mandatory. Otherwise reuse a browser that is
  // already on disk: the staged launcher, the Playwright cache, then the
  // account cache for revision 1193. A PLAYWRIGHT_BROWSERS_PATH that points
  // at an empty directory must not hide that cache. This runner never downloads.
  const candidates = env.M3B_CHROMIUM_EXECUTABLE
    ? [env.M3B_CHROMIUM_EXECUTABLE]
    : [
        staged,
        chromium.executablePath(),
        path.join(homeCache, "chromium-1193/chrome-linux/chrome"),
        path.join(homeCache, "chromium_headless_shell-1193/chrome-linux/headless_shell"),
      ];
  const missing = [];
  let executablePath;
  for (const candidate of candidates) {
    const resolved = path.resolve(root, candidate);
    try {
      await access(resolved, constants.X_OK);
      executablePath = resolved;
      break;
    } catch (error) {
      missing.push(`${resolved} (${error.code})`);
    }
  }
  if (!executablePath) {
    throw new Error(`Chromium ausente ou não executável: ${missing.join("; ")}; provisione o browser e bibliotecas ou defina M3B_CHROMIUM_EXECUTABLE. O runner não baixa browser.`);
  }
  env.M3B_CHROMIUM_EXECUTABLE = executablePath;
  console.log(`M4B Chromium: ${executablePath}`);
  const browser = await chromium.launch({ executablePath });
  console.log(`M4B browser real: ${browser.version()}`);
  await browser.close();
  fixtureDir = await mkdtemp(path.join(env.GOTMPDIR, "m4b-"));
  const binary = path.join(fixtureDir, "demo");
  const build = start("go", ["-C", "backend", "build", "-mod=vendor", "-o", binary, "./cmd/m2demo"]);
  build.stdout.pipe(process.stdout); await finish(build);
  const fixture = start(binary, ["m4b"], { cwd: path.join(root, "backend") });
  const ready = new Promise((resolve, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error("fixture M4B não ficou pronta em 90s")), 90_000);
    fixture.once("error", reject);
    fixture.once("exit", code => { clearTimeout(timer); reject(new Error(`fixture encerrou antes do aceite: ${code}`)); });
    fixture.stdout.on("data", chunk => {
      buffer += chunk;
      for (;;) {
        const end = buffer.indexOf("\n"); if (end < 0) break;
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        if (line.startsWith('{"Email":')) { clearTimeout(timer); resolve(JSON.parse(line)); }
      }
    });
  });
  const info = await ready;
  if (!info.M4A || !info.M3B || !info.M4B) throw new Error("fixture M4B sem atores, ERP fake ou controle de simulação");
  const filename = path.join(fixtureDir, "fixture.json");
  await writeFile(filename, JSON.stringify(info), { mode: 0o600 });
  env.M4B_FIXTURE_FILE = filename;
  const vite = start(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", "5173", "--strictPort"], { cwd: path.join(root, "web") });
  vite.stdout.pipe(process.stdout);
  for (let i = 0; ; i++) {
    if (vite.exitCode !== null) throw new Error(`Vite indisponível: ${vite.exitCode}`);
    try { if ((await fetch("http://127.0.0.1:5173")).ok) break; } catch { /* espera limitada */ }
    if (i === 100) throw new Error("Vite não ficou pronto");
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  // npm ci drops the optimizer cache. Navigating while it rewrites modules
  // closes the single renderer. Wait until the entry and the dep metadata settle.
  const entry = "http://127.0.0.1:5173/src/main.tsx";
  const meta = path.join(root, "web/node_modules/.vite/deps/_metadata.json");
  for (let i = 0; ; i++) {
    if (vite.exitCode !== null) throw new Error(`Vite indisponível: ${vite.exitCode}`);
    let ready = false;
    try {
      const response = await fetch(entry);
      const text = await response.text();
      await access(meta, constants.R_OK);
      ready = response.ok && text.includes("import");
    } catch { /* optimizer still writing */ }
    if (ready) break;
    if (i === 150) throw new Error("Vite não concluiu o bundle do painel");
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  // tsconfig.e2e.json permanece só com M3B; este runner não o amplia.
  // O cenário M4A é importado pelo Node 24, que remove os tipos na carga.
  const check = start(process.execPath, ["node_modules/typescript/bin/tsc", "-p", "tsconfig.e2e.json"], { cwd: path.join(root, "web") });
  check.stdout.pipe(process.stdout); await finish(check);
  process.env.M3B_CHROMIUM_EXECUTABLE = executablePath;
  process.env.M4B_FIXTURE_FILE = filename;
  const { chromium: runner, expect } = await import("@playwright/test");
  const config = (await import("../playwright.config.ts")).default;
  expect.configure({ timeout: config.expect?.timeout ?? 15_000 });
  const launch = config.use?.launchOptions ?? {};
  const scenarioBrowser = await runner.launch({ ...launch, executablePath });
  try {
    const { runM4BM6 } = await import("../e2e/m4b-m6.spec.ts");
    await runM4BM6(scenarioBrowser, {
      attach: async (name, { body }) => {
        const dir = path.join(root, ".paseo-build-tmp/m4b-m6-browser");
        await mkdir(dir, { recursive: true });
        await writeFile(path.join(dir, `${name}.json`), body, { mode: 0o600 });
      },
    });
  } finally { await scenarioBrowser.close(); }
  console.log("M4B-M6 E2E passou (simulação local, sem skip)");
} catch (error) {
  console.error("M4B-M6 E2E FALHOU (sem skip):", error.message);
  process.exitCode = 1;
} finally { await stop(); }
