import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type Schema = components["schemas"];
export class AttendanceError extends Error {
  status: number;
  constructor(status: number, detail: string) { super(detail); this.status = status; }
}
function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.data !== undefined) return result.data;
  const error = result.error as { detail?: string } | undefined;
  throw new AttendanceError(result.response.status, error?.detail || "Não foi possível concluir a operação.");
}
const path = (inbox_id: string, conversation_id: string) => ({ inbox_id, conversation_id });
export async function listInboxes() { return unwrap(await api.GET("/api/panel/inboxes")); }
export async function snapshot(inbox_id: string, conversation_id?: string) { return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations", { params: { path: { inbox_id }, query: { conversation_id } } })); }
export async function history(inbox_id: string, conversation_id: string) { return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/history", { params: { path: path(inbox_id, conversation_id) } })); }
export async function events(inbox_id: string, after: number) { return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/events", { params: { path: { inbox_id }, query: { after } } })); }
export async function assume(inbox_id: string, conversation_id: string, body: Schema["VersionBody"]) { return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/assume", { params: { path: path(inbox_id, conversation_id) }, body })); }
export async function send(inbox_id: string, conversation_id: string, body: Schema["SendBody"]) { return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/messages", { params: { path: path(inbox_id, conversation_id) }, body })); }
export async function close(inbox_id: string, conversation_id: string, body: Schema["CloseBody"]) { return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/close", { params: { path: path(inbox_id, conversation_id) }, body })); }
export async function reopen(inbox_id: string, conversation_id: string, body: Schema["ReopenBody"]) { return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/reopen", { params: { path: path(inbox_id, conversation_id) }, body })); }
