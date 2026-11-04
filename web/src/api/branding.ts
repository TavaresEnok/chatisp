import { currentCsrfToken } from "@/api/client";
import { PanelRequestError } from "@/api/problems";

export const providerFallbackName = "Chat ISP";

export type ProviderIdentity = {
  display_name: string;
  fallback_name: string;
  has_logo: boolean;
  content_type?: string;
  width?: number;
  height?: number;
  updated_at?: string;
};

const logoTypes = new Set(["image/png", "image/jpeg"]);

async function panel(path: string, init: RequestInit | undefined, csrfToken: string | null): Promise<Response> {
  const headers = new Headers(init?.headers);
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  if (init?.body && typeof init.body === "string" && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, { ...init, headers, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) {
    throw new PanelRequestError("Não foi possível concluir a operação.", response.status);
  }
  return response;
}

function asIdentity(body: Partial<ProviderIdentity> | null): ProviderIdentity {
  return {
    display_name: typeof body?.display_name === "string" ? body.display_name : "",
    fallback_name: typeof body?.fallback_name === "string" && body.fallback_name.trim() !== "" ? body.fallback_name : providerFallbackName,
    has_logo: body?.has_logo === true,
    content_type: typeof body?.content_type === "string" ? body.content_type : undefined,
    width: typeof body?.width === "number" ? body.width : undefined,
    height: typeof body?.height === "number" ? body.height : undefined,
    updated_at: typeof body?.updated_at === "string" ? body.updated_at : undefined,
  };
}

export async function getBranding(signal?: AbortSignal): Promise<ProviderIdentity> {
  const response = await panel("/api/panel/branding", { signal }, currentCsrfToken());
  return asIdentity((await response.json()) as Partial<ProviderIdentity>);
}

export async function saveBrandingName(displayName: string, csrfToken: string | null, signal?: AbortSignal): Promise<ProviderIdentity> {
  const response = await panel("/api/panel/branding", {
    method: "PUT",
    body: JSON.stringify({ display_name: displayName }),
    signal,
  }, csrfToken);
  return asIdentity((await response.json()) as Partial<ProviderIdentity>);
}

export async function saveBrandingLogo(file: Blob, contentType: "image/png" | "image/jpeg", csrfToken: string | null, signal?: AbortSignal): Promise<ProviderIdentity> {
  const response = await panel("/api/panel/branding/logo", {
    method: "PUT",
    body: file,
    headers: { "Content-Type": contentType },
    signal,
  }, csrfToken);
  return asIdentity((await response.json()) as Partial<ProviderIdentity>);
}

export async function removeBrandingLogo(csrfToken: string | null, signal?: AbortSignal): Promise<ProviderIdentity> {
  const response = await panel("/api/panel/branding/logo", { method: "DELETE", signal }, csrfToken);
  return asIdentity((await response.json()) as Partial<ProviderIdentity>);
}

export async function getBrandingLogo(signal?: AbortSignal): Promise<Blob> {
  const response = await panel("/api/panel/branding/logo", { signal }, currentCsrfToken());
  const type = (response.headers.get("content-type") ?? "").split(";")[0]?.trim() ?? "";
  if (!logoTypes.has(type)) {
    throw new PanelRequestError("Não foi possível concluir a operação.", response.status);
  }
  return response.blob();
}
