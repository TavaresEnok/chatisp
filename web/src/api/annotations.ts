import { api } from "@/api/client";
import { AttendanceError, type Schema } from "@/api/attendance";

function unwrap<T>(result: { data?: T; error?: { detail?: string }; response: Response }): T {
  if (result.data !== undefined) return result.data;
  throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível acessar as anotações.");
}
const path = (inbox_id: string, conversation_id: string) => ({ inbox_id, conversation_id });
export async function annotations(inbox: string, conversation: string) {
  return unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/annotations", { params: { path: path(inbox, conversation) } }));
}
export async function changeAnnotation(inbox: string, conversation: string, body: Schema["AnnotationChangeBody"]) {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/annotations", { params: { path: path(inbox, conversation) }, body }));
}
export async function markRead(inbox: string, conversation: string, body: Schema["AnnotationReadBody"]) {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/annotations/read", { params: { path: path(inbox, conversation) }, body }));
}
export async function tags() {
  const result = unwrap(await api.GET("/api/panel/annotation-tags"));
  if (result !== null && !Array.isArray(result)) throw new Error("Catálogo de tags inválido.");
  return result ?? [];
}
