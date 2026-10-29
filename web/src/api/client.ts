import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "@/api/schema";

const CSRF_COOKIE = "__Host-csrf";
const PANEL_PREFIXES = ["/api/panel", "/api/platform"];

function sameOriginBase(): string {
  if (typeof location === "undefined" || location.origin === "null") {
    return "";
  }
  return location.origin;
}

let memoryCsrf: string | null = null;
let onSessionLoss: (() => void) | null = null;

export function setCsrfToken(token: string | null) {
  memoryCsrf = token && token.length > 0 ? token : null;
}

export function readCsrfCookie(): string | null {
  if (typeof document === "undefined") {
    return null;
  }
  for (const part of document.cookie.split("; ")) {
    const separator = part.indexOf("=");
    if (separator === -1) {
      continue;
    }
    if (part.slice(0, separator) === CSRF_COOKIE) {
      return decodeURIComponent(part.slice(separator + 1));
    }
  }
  return null;
}

export function currentCsrfToken(): string | null {
  return memoryCsrf ?? readCsrfCookie();
}

export function setSessionLossHandler(handler: (() => void) | null) {
  onSessionLoss = handler;
}

function panelPath(schemaPath: string): boolean {
  return PANEL_PREFIXES.some((prefix) => schemaPath.startsWith(prefix));
}

function anonymousAuth(method: string, schemaPath: string): boolean {
  if (method !== "POST") {
    return false;
  }
  return (
    schemaPath === "/api/panel/session" ||
    schemaPath === "/api/panel/invitations/accept" ||
    schemaPath === "/api/panel/password-resets" ||
    schemaPath === "/api/panel/password-resets/confirm"
  );
}

const sessionMiddleware: Middleware = {
  onRequest({ request, schemaPath }) {
    if (!panelPath(schemaPath)) {
      return request;
    }
    const token = currentCsrfToken();
    if (token && !request.headers.has("X-CSRF-Token")) {
      request.headers.set("X-CSRF-Token", token);
    }
    return request;
  },
  onResponse({ request, response, schemaPath }) {
    if (response.status !== 401 || anonymousAuth(request.method.toUpperCase(), schemaPath)) {
      return undefined;
    }
    onSessionLoss?.();
    return undefined;
  },
};

// OpenAPI server URL is "/": same origin, paths under /api and /healthz.
// An absolute base keeps Request valid outside the browser as well.
export const api = createClient<paths>({
  baseUrl: sameOriginBase(),
  credentials: "include",
  // Resolve fetch on each call so tests can replace globalThis.fetch.
  fetch: (input: Request) => globalThis.fetch(input),
});

api.use(sessionMiddleware);
