import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../web");
const require = createRequire(path.join(webDir, "package.json"));

function plugin(mod) {
  if (typeof mod === "function") {
    return mod;
  }
  if (mod && typeof mod.default === "function") {
    return mod.default;
  }
  throw new Error("plugin do Vite sem função padrão");
}

function loopbackHost(value, fallback) {
  const host = value === undefined || value === "" ? fallback : String(value);
  if (host === "::1") {
    return host;
  }
  const match = /^127\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!match) {
    throw new Error("host do painel precisa ser loopback");
  }
  const parts = match.slice(1).map(Number);
  if (parts.some((part) => part > 255)) {
    throw new Error("host do painel precisa ser loopback");
  }
  return host;
}

function loopbackPort(value, fallback) {
  const raw = value === undefined || value === "" ? String(fallback) : String(value);
  if (!/^[1-9]\d{0,4}$/.test(raw)) {
    throw new Error("porta inválida");
  }
  const port = Number(raw);
  if (port > 65535) {
    throw new Error("porta inválida");
  }
  return port;
}

function loopbackHTTPOrigin(value, fallback) {
  const raw = value === undefined || value === "" ? fallback : String(value);
  let url;
  try {
    url = new URL(raw);
  } catch {
    throw new Error("origem da API inválida");
  }
  if (url.protocol !== "http:" || url.username || url.password || url.pathname !== "/" || url.search || url.hash) {
    throw new Error("origem da API inválida");
  }
  const hostname = url.hostname.startsWith("[") && url.hostname.endsWith("]")
    ? url.hostname.slice(1, -1)
    : url.hostname;
  const host = loopbackHost(hostname, hostname);
  if (!url.port) {
    throw new Error("origem da API inválida");
  }
  const port = loopbackPort(url.port, url.port);
  const shown = host.includes(":") ? `[${host}]` : host;
  return `http://${shown}:${port}`;
}

export function devServerSettings(env = process.env) {
  const host = loopbackHost(env.CHATISP_DEV_VITE_HOST, "127.0.0.1");
  const port = loopbackPort(env.CHATISP_DEV_VITE_PORT, 5173);
  const apiOrigin = loopbackHTTPOrigin(env.CHATISP_DEV_API_ORIGIN, "http://127.0.0.1:8080");
  const settings = {
    host,
    port,
    strictPort: true,
    proxy: {
      "/api": apiOrigin,
      "/healthz": apiOrigin,
      "/readyz": apiOrigin,
    },
  };
  if (env.CHATISP_DEV_TLS === "1") {
    if (!env.CHATISP_DEV_TLS_KEY || !env.CHATISP_DEV_TLS_CERT) {
      throw new Error("CHATISP_DEV_TLS=1 exige chave e certificado locais");
    }
    settings.https = {
      key: readFileSync(env.CHATISP_DEV_TLS_KEY),
      cert: readFileSync(env.CHATISP_DEV_TLS_CERT),
    };
  }
  return settings;
}

export default function chatispDevConfig() {
  const react = plugin(require("@vitejs/plugin-react"));
  const tailwind = plugin(require("@tailwindcss/vite"));
  return {
    root: webDir,
    plugins: [react(), tailwind()],
    resolve: {
      alias: {
        "@": path.join(webDir, "src"),
      },
    },
    server: devServerSettings(),
  };
}
