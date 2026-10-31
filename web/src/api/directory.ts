import { api } from "@/api/client";
import { AttendanceError } from "@/api/attendance";
import type { components } from "@/api/schema";

export type DirectoryEntry = components["schemas"]["RealtimeStoreDirectoryEntry"];

/** Reads the operational directory of one inbox through explicit cursor pages. */
export async function directory(inbox_id: string, signal?: AbortSignal): Promise<DirectoryEntry[]> {
  const out: DirectoryEntry[] = [];
  const cursors = new Set<string>();
  let after: string | undefined;
  for (;;) {
    signal?.throwIfAborted();
    const result = await api.GET("/api/panel/inboxes/{inbox_id}/directory", { params: { path: { inbox_id }, query: { after, limit: 100 } }, signal });
    signal?.throwIfAborted();
    if (!result.data) throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível carregar o diretório de atendentes.");
    out.push(...(result.data.items ?? []));
    if (!result.data.next_cursor) return out;
    if (cursors.has(result.data.next_cursor)) throw new Error("O diretório retornou uma continuação repetida.");
    cursors.add(result.data.next_cursor);
    after = result.data.next_cursor;
  }
}
