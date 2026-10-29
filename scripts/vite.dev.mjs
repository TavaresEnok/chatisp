import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../web");
const require = createRequire(path.join(webDir, "package.json"));
const { defineConfig } = require("vite");

function plugin(mod) {
  if (typeof mod === "function") {
    return mod;
  }
  if (mod && typeof mod.default === "function") {
    return mod.default;
  }
  throw new Error("plugin do Vite sem função padrão");
}

const react = plugin(require("@vitejs/plugin-react"));
const tailwind = plugin(require("@tailwindcss/vite"));

export default defineConfig({
  root: webDir,
  plugins: [react(), tailwind()],
  resolve: {
    alias: {
      "@": path.join(webDir, "src"),
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
      "/readyz": "http://127.0.0.1:8080",
    },
  },
});
