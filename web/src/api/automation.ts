import { api } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";

export type TriageMenu = components["schemas"]["TriageMenu"];
export type TriageOption = components["schemas"]["TriageOption"];
export type InactivitySettings = components["schemas"]["LifecycleInactivitySettings"];
export type PresenceSettings = components["schemas"]["PresenceSettings"];
export type Snooze = components["schemas"]["LifecycleSnooze"];

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.response.ok && result.data !== undefined) return result.data;
  throw new PanelRequestError(problemMessage(result.error, result.response.status), result.response.status);
}

export async function getReopenWindow(inbox_id: string, signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/reopen-window", { params: { path: { inbox_id } }, signal, cache: "no-store" }));
}
export async function setReopenWindow(inbox_id: string, hours: number, signal?: AbortSignal) {
  return unwrap(await api.PUT("/api/panel/inboxes/{inbox_id}/reopen-window", { params: { path: { inbox_id } }, body: { hours }, signal }));
}

export async function getTriageMenu(inbox_id: string, signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/triage-menu", { params: { path: { inbox_id } }, signal }));
}
export async function putTriageMenu(inbox_id: string, body: { expected_version: number; max_attempts: number; options: TriageOption[] }, signal?: AbortSignal) {
  return unwrap(await api.PUT("/api/panel/inboxes/{inbox_id}/triage-menu", { params: { path: { inbox_id } }, body: { ...body, request_id: crypto.randomUUID() }, signal }));
}

export async function getInactivity(signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/lifecycle/inactivity", { signal }));
}
export async function putInactivity(body: { expected_version: number; warning_after_seconds: number; close_after_seconds: number }, signal?: AbortSignal) {
  return unwrap(await api.PUT("/api/panel/lifecycle/inactivity", { body: { ...body, request_id: crypto.randomUUID() }, signal }));
}

export async function getPresenceSettings(signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/presence/settings", { signal }));
}
export async function putPresenceSettings(body: { expected_version: number; offline_tolerance_seconds: number }, signal?: AbortSignal) {
  return unwrap(await api.PUT("/api/panel/presence/settings", { body: { ...body, request_id: crypto.randomUUID() }, signal }));
}

/** Active snooze of a conversation, or null when there is none (404). */
export async function getSnooze(inbox_id: string, conversation_id: string, signal?: AbortSignal): Promise<Snooze | null> {
  const result = await api.GET("/api/panel/conversations/{conversation_id}/snooze", { params: { path: { conversation_id }, query: { inbox_id } }, signal, cache: "no-store" });
  if (result.response.status === 404) return null;
  const value = unwrap(result);
  // Only a complete active snooze counts; anything else means none.
  return value.state === "active" && value.snoozed_until ? value : null;
}
export async function snooze(conversation_id: string, body: { request_id: string; inbox_id: string; expected_version: number; until: string; reason: string }, signal?: AbortSignal) {
  return unwrap(await api.PUT("/api/panel/conversations/{conversation_id}/snooze", { params: { path: { conversation_id } }, body, signal }));
}
