import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "m3b*.spec.ts",
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  outputDir: "../.paseo-build-tmp/m3b-browser",
  reporter: "list",
  use: {
    baseURL: "https://localhost:5174",
    ignoreHTTPSErrors: true,
    browserName: "chromium",
    // Tracing retains a full network log per context. With the disposable TLS
    // proxy and three live panels that exhausts the browser and stalls navigation.
    trace: "off",
    screenshot: "only-on-failure",
    // One renderer and no GPU process: the runner cgroup caps threads at 256,
    // and --single-process / --no-zygote crash a second panel context. SwiftShader
    // stays available; disabling the software rasterizer crashes this host.
    launchOptions: {
      ...(process.env.M3B_CHROMIUM_EXECUTABLE ? { executablePath: process.env.M3B_CHROMIUM_EXECUTABLE } : {}),
      args: ["--disable-gpu", "--disable-dev-shm-usage", "--mute-audio", "--renderer-process-limit=1", "--num-raster-threads=1", "--disable-features=AudioServiceOutOfProcess,IsolateOrigins,site-per-process"],
    },
  },
});
