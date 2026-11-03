import { spawn, spawnSync } from "node:child_process";
import { access, mkdir, mkdtemp, writeFile, rm } from "node:fs/promises";
import { constants } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "@playwright/test";

const root = fileURLToPath(new URL("../../", import.meta.url));
// Keep the disposable Go services and esbuild within the runner's PID budget
// while Chromium, PostgreSQL and three browser contexts are alive together.
// The cgroup allows 256 threads. Vite stays single-threaded and the scenario
// runs in this process so Chromium can open the three panel contexts.
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
async function stop() {
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    const ended = new Promise(resolve => child.once("exit", resolve));
    child.kill("SIGTERM");
    const timer = setTimeout(() => child.kill("SIGKILL"), 20_000);
    await ended; clearTimeout(timer);
  }
  if (fixtureDir) await rm(fixtureDir, { recursive: true, force: true });
}
for (const signal of ["SIGINT", "SIGTERM"]) process.once(signal, () => { void stop().then(() => process.exit(1)); });
try {
  if (Object.keys(env).some(key => /^CHATISP_.*DATABASE_URL$/.test(key))) throw new Error("M3B recusa DSNs externos");
  // Vite's rolldown pool and Chromium's foreground pool follow the CPU affinity.
  // Two CPUs keep the three panels inside the runner's 256-thread cgroup.
  const pinned = spawnSync("taskset", ["-pc", "0,1", String(process.pid)], { encoding: "utf8" });
  if (pinned.status !== 0) throw new Error(`afinidade de CPU indisponível: ${pinned.stderr || pinned.error?.message || pinned.status}`);
  await mkdir(env.GOTMPDIR, { recursive: true });
  // Launch, rather than merely checking the cache directory: this detects missing libraries too.
  const staged = path.join(root, ".paseo-build-tmp/browser/chromium-launcher");
  let executablePath = env.M3B_CHROMIUM_EXECUTABLE;
  if (!executablePath) {
    try { await access(staged, constants.F_OK); executablePath = staged; }
    catch (error) { if (error.code !== "ENOENT") throw error; }
  }
  executablePath = path.resolve(root, executablePath || chromium.executablePath());
  await access(executablePath, constants.X_OK).catch(error => {
    throw new Error(`Chromium ausente ou não executável: ${executablePath}; provisione o browser e bibliotecas ou defina M3B_CHROMIUM_EXECUTABLE (${error.code})`);
  });
  // One resolved executable for both preflight and the Playwright subprocess.
  env.M3B_CHROMIUM_EXECUTABLE = executablePath;
  console.log(`M3B Chromium: ${executablePath}`);
  const browser = await chromium.launch({ executablePath });
  console.log(`M3B browser real: ${browser.version()}`);
  await browser.close();
  fixtureDir = await mkdtemp(path.join(env.GOTMPDIR, "m3b-"));
  const binary = path.join(fixtureDir, "demo");
  const build = start("go", ["-C", "backend", "build", "-mod=vendor", "-o", binary, "./cmd/m2demo"]);
  build.stdout.pipe(process.stdout); await finish(build);
  const fixture = start(binary, ["m3b"], { cwd: path.join(root, "backend") });
  const ready = new Promise((resolve, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error("fixture M3B não ficou pronta em 90s")), 90_000);
    fixture.once("error", reject);
    fixture.once("exit", code => { clearTimeout(timer); reject(new Error(`fixture encerrou antes do aceite: ${code}`)); });
    fixture.stdout.on("data", chunk => {
      buffer += chunk;
      for (;;) {
        const end = buffer.indexOf("\n"); if (end < 0) break;
        const line = buffer.slice(0,end); buffer = buffer.slice(end+1);
        if (line.startsWith('{"Email":')) { clearTimeout(timer); resolve(JSON.parse(line)); }
        // Never echo ephemeral session credentials.
      }
    });
  });
  const info = await ready;
  const filename = path.join(fixtureDir, "fixture.json");
  await writeFile(filename, JSON.stringify(info), { mode: 0o600 });
  env.M3B_FIXTURE_FILE = filename;
  const vite = start(process.execPath, ["node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", "5173", "--strictPort"], { cwd: path.join(root, "web") });
  // Resolve Vite from its own working directory.
  vite.stdout.pipe(process.stdout);
  for (let i = 0; ; i++) {
    if (vite.exitCode !== null) throw new Error(`Vite indisponível: ${vite.exitCode}`);
    try { if ((await fetch("http://127.0.0.1:5173")).ok) break; } catch { /* bounded readiness */ }
    if (i === 100) throw new Error("Vite não ficou pronto");
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  const check = start(process.execPath, ["node_modules/typescript/bin/tsc", "-p", "tsconfig.e2e.json"], { cwd: path.join(root,"web") });
  check.stdout.pipe(process.stdout); await finish(check);
  process.env.M3B_CHROMIUM_EXECUTABLE = executablePath;
  process.env.M3B_FIXTURE_FILE = filename;
  const { chromium: runner, expect } = await import("@playwright/test");
  const config = (await import("../playwright.config.ts")).default;
  expect.configure({ timeout: config.expect?.timeout ?? 15_000 });
  const launch = config.use?.launchOptions ?? {};
  const scenarioBrowser = await runner.launch({ ...launch, executablePath });
  try {
    const { runM3B } = await import("../e2e/m3b.spec.ts");
    await runM3B(scenarioBrowser, {
      attach: async (name, { body }) => {
        const dir = path.join(root, ".paseo-build-tmp/m3b-browser");
        await mkdir(dir, { recursive: true });
        await writeFile(path.join(dir, `${name}.json`), body, { mode: 0o600 });
      },
    });
  } finally { await scenarioBrowser.close(); }
} catch (error) {
  console.error("M3B E2E FALHOU (sem skip):", error.message);
  process.exitCode = 1;
} finally { await stop(); }
