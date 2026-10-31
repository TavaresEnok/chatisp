import { api } from "@/api/client";
import { AttendanceError } from "@/api/attendance";
import type { operations } from "@/api/schema";

export type QueueQuery = NonNullable<operations["attendance-queue"]["parameters"]["query"]>;

export async function fetchQueue(query: QueueQuery, signal?: AbortSignal) {
  const result = await api.GET("/api/panel/queue", { params: { query }, signal });
  if (result.data) return result.data;
  throw new AttendanceError(result.response.status, result.error?.detail || "Não foi possível carregar a fila.");
}
