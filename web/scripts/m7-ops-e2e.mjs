import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:net";
import { access, mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { constants } from "node:fs";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";
import path from "node:path";
import { chromium } from "@playwright/test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const env = { ...process.env, GOMAXPROCS: "1", RAYON_NUM_THREADS: "1", UV_THREADPOOL_SIZE: "1", TMPDIR: "/tmp", GOTMPDIR: path.join(root, ".paseo-build-tmp") };
const children = [];
const browsers = new Set();
let fixtureDir;
let aborted = false;
const startedAt = Date.now();
const HARD_BUDGET_MS = 350_000;
const WORK_BUDGET_MS = 330_000;
function workRemaining() {
  return WORK_BUDGET_MS - (Date.now() - startedAt);
}
function budgetError() {
  return new Error("orçamento do launcher M7 esgotado antes de 360s; preparação e jornada encerradas");
}
function ensureBudget() {
  if (aborted || workRemaining() <= 0) throw budgetError();
}
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
let stopping;
function stop() {
  if (!stopping) stopping = stopOwned();
  return stopping;
}
async function stopOwned() {
  await Promise.all(children.map(async (child) => {
    if (child.exitCode !== null || child.signalCode !== null) return;
    const ended = childGone(child);
    child.kill("SIGTERM");
    const timer = setTimeout(() => child.kill("SIGKILL"), 2_000);
    await Promise.race([ended, new Promise(resolve => setTimeout(resolve, 4_000))]);
    clearTimeout(timer);
  }));
  if (fixtureDir) await rm(fixtureDir, { recursive: true, force: true });
}
async function closeBrowsers() {
  const open = [...browsers];
  browsers.clear();
  await Promise.all(open.map(async (browser) => {
    await Promise.race([
      browser.close().catch(() => {}),
      new Promise(resolve => setTimeout(resolve, 4_000)),
    ]);
  }));
}
async function launchChromium(launcher, options) {
  ensureBudget();
  const browser = await launcher.launch({ ...options, timeout: Math.min(30_000, Math.max(1_000, workRemaining())) });
  browsers.add(browser);
  return browser;
}
function freePort() {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address();
      const port = address && typeof address === "object" ? address.port : 0;
      probe.close(() => resolve(port));
    });
  });
}
for (const signal of ["SIGINT", "SIGTERM"]) process.once(signal, () => { aborted = true; void closeBrowsers().then(() => stop()).then(() => process.exit(1)); });
const hardTimer = setTimeout(() => {
  aborted = true;
  console.error("M7-OPS E2E FALHOU (sem skip): orçamento total de 350s esgotado");
  process.exit(1);
}, HARD_BUDGET_MS);
async function runAll() {
  if (Object.keys(env).some(key => /^CHATISP_.*DATABASE_URL$/.test(key))) throw new Error("M7 recusa DSNs externos");
  delete env.CHATISP_E2E_API_PORT;
  delete process.env.CHATISP_E2E_API_PORT;
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
  const candidates = env.M3B_CHROMIUM_EXECUTABLE
    ? [env.M3B_CHROMIUM_EXECUTABLE]
    : [staged, chromium.executablePath(), path.join(homeCache, "chromium-1193/chrome-linux/chrome"), path.join(homeCache, "chromium_headless_shell-1193/chrome-linux/headless_shell")];
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
  const probe = await launchChromium(chromium, { executablePath });
  const browserVersion = probe.version();
  await probe.close().catch(() => {});
  browsers.delete(probe);
  console.log(`M7 Chromium: ${browserVersion} ${executablePath}`);
  const vitePort = await freePort();
  env.CHATISP_E2E_VITE_PORT = String(vitePort);
  console.log(`M7 Vite loopback: 127.0.0.1:${vitePort}`);
  fixtureDir = await mkdtemp(path.join(env.GOTMPDIR, "m7-"));
  const binary = path.join(fixtureDir, "demo");
  const build = start("go", ["-C", "backend", "build", "-mod=vendor", "-o", binary, "./cmd/m2demo"]);
  build.stdout.on("data", (chunk) => process.stdout.write(chunk));
  const vite = start(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", String(vitePort), "--strictPort"], { cwd: path.join(root, "web") });
  vite.stdout.on("data", (chunk) => process.stdout.write(chunk));
  await finish(build);
  const fixture = start(binary, ["m7"], { cwd: path.join(root, "backend") });
  const ready = new Promise((resolve, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error("fixture M7 não ficou pronta em 80s")), 80_000);
    fixture.once("error", reject);
    fixture.once("exit", code => { clearTimeout(timer); reject(new Error(`fixture encerrou antes do aceite: ${code}`)); });
    fixture.stdout.on("data", chunk => {
      buffer += chunk;
      for (;;) {
        const end = buffer.indexOf("\n");
        if (end < 0) break;
        const line = buffer.slice(0, end);
        buffer = buffer.slice(end + 1);
        if (line.startsWith('{"Email":')) {
          clearTimeout(timer);
          resolve(JSON.parse(line));
        }
      }
    });
  });
  const info = await ready;
  if (!info.M7?.Admin || !info.M7.Restricted || !info.M7.Alfa || !info.M7.Beta || !info.M7.MetricsURL || !info.M7.API || !info.M7.PanelOrigin) {
    throw new Error("fixture M7 sem atores, recortes ou portas");
  }
  console.log(`M7 API ${info.M7.API} métricas ${info.M7.MetricsURL} painel ${info.M7.PanelOrigin}`);
  for (let i = 0; ; i++) {
    if (vite.exitCode !== null) throw new Error(`Vite indisponível: ${vite.exitCode}`);
    try { if ((await fetch(`http://127.0.0.1:${vitePort}`)).ok) break; } catch { /* espera limitada */ }
    if (i === 80) throw new Error("Vite não ficou pronto");
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  for (let i = 0; ; i++) {
    try {
      const response = await fetch(`${info.M7.API}/readyz`);
      if (response.ok) break;
    } catch { /* worker ainda sobe */ }
    if (i === 80) throw new Error("API M7 não ficou pronta");
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  const filename = path.join(fixtureDir, "fixture.json");
  await writeFile(filename, JSON.stringify(info), { mode: 0o600 });
  const revision = spawnSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" });
  process.env.M7_FIXTURE_FILE = filename;
  process.env.M7_CODE_REV = revision.status === 0 ? revision.stdout.trim() : "";
  process.env.M7_BROWSER_VERSION = browserVersion;
  process.env.M3B_CHROMIUM_EXECUTABLE = executablePath;
  const { chromium: runner, expect } = await import("@playwright/test");
  const config = (await import("../playwright.config.ts")).default;
  expect.configure({ timeout: config.expect?.timeout ?? 15_000 });
  const launch = config.use?.launchOptions ?? {};
  const scenarioBrowser = await launchChromium(runner, { ...launch, executablePath });
  const { runM7Ops } = await import("../e2e/m7-ops.spec.ts");
  await runM7Ops(scenarioBrowser, {
    attach: async (name, { body }) => {
      const dir = path.join(root, ".paseo-build-tmp/m7-ops-browser");
      await mkdir(dir, { recursive: true });
      await writeFile(path.join(dir, `${name}.json`), body, { mode: 0o600 });
    },
  });
  ensureBudget();
  console.log("M7-OPS E2E passou (simulação local, sem skip)");
}
const work = runAll().then(() => "ok", (error) => ({ error }));
let workTimer;
const workTimeout = new Promise((resolve) => {
  workTimer = setTimeout(() => {
    aborted = true;
    resolve("timeout");
  }, Math.max(0, workRemaining()));
});
try {
  const winner = await Promise.race([work, workTimeout]);
  if (winner === "timeout" || aborted) throw budgetError();
  if (winner && winner.error) throw winner.error;
} catch (error) {
  aborted = true;
  console.error("M7-OPS E2E FALHOU (sem skip):", error.message);
  process.exitCode = 1;
} finally {
  clearTimeout(workTimer);
  await closeBrowsers();
  await stop();
  clearTimeout(hardTimer);
}
