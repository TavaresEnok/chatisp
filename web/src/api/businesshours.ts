import { api } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";

export type HoursConfiguration = components["schemas"]["BusinessHoursConfiguration"];
export type HoursCommand = components["schemas"]["BusinessHoursCommand"];
export type HoursDecision = components["schemas"]["BusinessHoursDecision"];
export type HoursDelete = components["schemas"]["DeleteInputBody"];

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.response.ok && result.data !== undefined) return result.data;
  throw new PanelRequestError(problemMessage(result.error, result.response.status), result.response.status);
}

// A lista compartilhada de setores descarta o status e a de caixas lança outro tipo.
// Esta seção precisa do 403/404 original para revogar o cache inteiro.
export async function listHoursInboxes(signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/inboxes", { signal }));
}

export async function listHoursSectors(signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/sectors", { signal }));
}

export async function getHours(inboxId: string, signal?: AbortSignal): Promise<HoursConfiguration> {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/business-hours", {
    params: { path: { inbox_id: inboxId } },
    signal,
  }));
}

export async function putHours(inboxId: string, body: HoursCommand): Promise<HoursConfiguration> {
  return unwrap(await api.PUT("/api/panel/inboxes/{inbox_id}/business-hours", {
    params: { path: { inbox_id: inboxId } },
    body,
  }));
}

export async function deleteHours(inboxId: string, body: HoursDelete): Promise<HoursConfiguration> {
  return unwrap(await api.DELETE("/api/panel/inboxes/{inbox_id}/business-hours", {
    params: { path: { inbox_id: inboxId } },
    body,
  }));
}

export async function evaluateHours(inboxId: string, instant: string, signal?: AbortSignal): Promise<HoursDecision> {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/business-hours/evaluation", {
    params: { path: { inbox_id: inboxId }, query: { instant } },
    signal,
  }));
}
