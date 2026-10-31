import { api } from "@/api/client";
import { AttendanceError } from "@/api/attendance";

const path = (inbox_id: string, conversation_id: string) => ({ inbox_id, conversation_id });
export async function indicators(inbox: string, conversation: string) {
  const result = await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision", { params: { path: path(inbox, conversation) } });
  if (result.data) return result.data;
  throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível consultar presença.");
}
export async function heartbeat(inbox: string, conversation: string, tab_id: string, typing: boolean) {
  const result = await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision", { params: { path: path(inbox, conversation) }, body: { tab_id, typing } });
  if (!result.response.ok) throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível atualizar presença.");
}
export async function release(inbox: string, conversation: string, tab_id: string) {
  const result = await api.DELETE("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/collision/{tab_id}", { params: { path: { ...path(inbox, conversation), tab_id } } });
  if (!result.response.ok) throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível liberar presença.");
}
