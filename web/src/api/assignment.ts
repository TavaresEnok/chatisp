import { api } from "@/api/client";
import { AttendanceError, type Schema } from "@/api/attendance";

function unwrap<T>(result: { data?: T; error?: { detail?: string }; response: Response }): T {
  if (result.data !== undefined) return result.data;
  throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível concluir a atribuição.");
}
const path = (inbox_id: string, conversation_id: string) => ({ inbox_id, conversation_id });
export async function transferSectors(inbox: string, conversation: string, signal?: AbortSignal) {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/transfer-sectors", { params: { path: path(inbox, conversation) }, signal }));
}
export async function candidates(inbox: string, conversation: string) {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/assignment-candidates", { params: { path: path(inbox, conversation) } }));
}
export async function assign(inbox: string, conversation: string, action: "member" | "sector" | "unassign", body: Schema["AssignmentBody"]) {
  const params = { path: path(inbox, conversation) };
  if (action === "member") return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/transfer-member", { params, body }));
  if (action === "sector") return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/transfer-sector", { params, body }));
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/unassign", { params, body }));
}
export async function capacity(membership_id: string, capacity: number) {
  return unwrap(await api.POST("/api/panel/memberships/{membership_id}/assignment-capacity", { params: { path: { membership_id } }, body: { capacity } }));
}
