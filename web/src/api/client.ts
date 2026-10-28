import createClient from "openapi-fetch";
import type { paths } from "@/api/schema";

function sameOriginBase(): string {
  if (typeof location === "undefined" || location.origin === "null") {
    return "";
  }
  return location.origin;
}

// OpenAPI server URL is "/": same origin, paths /healthz and /readyz.
// An absolute base keeps Request valid outside the browser as well.
export const api = createClient<paths>({
  baseUrl: sameOriginBase(),
  credentials: "include",
});
