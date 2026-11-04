import { currentCsrfToken } from "@/api/client";
import { PanelRequestError } from "@/api/problems";

export type AuditEvent = {
  created_at: string;
  action: string;
  target_type: string;
  result: string;
};

export type AuditPage = {
  from: string;
  to: string;
  events: AuditEvent[];
  next_cursor?: string;
};

export type AuditQuery = {
  from: string;
  to: string;
  cursor?: string;
};

export async function listAudit(query: AuditQuery, signal?: AbortSignal): Promise<AuditPage> {
  const params = new URLSearchParams({ from: query.from, to: query.to });
  if (query.cursor) params.set("cursor", query.cursor);
  const headers = new Headers();
  const csrf = currentCsrfToken();
  if (csrf) headers.set("X-CSRF-Token", csrf);
  const response = await fetch(`/api/panel/audit?${params}`, {
    headers,
    signal,
    credentials: "same-origin",
    cache: "no-store",
  });
  if (!response.ok) {
    throw new PanelRequestError(
      response.status === 403 ? "Acesso negado." : "Não foi possível carregar a auditoria.",
      response.status,
    );
  }
  const page = (await response.json()) as AuditPage;
  return {
    from: page.from,
    to: page.to,
    next_cursor: page.next_cursor,
    events: (page.events ?? []).map((event) => ({
      created_at: event.created_at,
      action: event.action,
      target_type: event.target_type,
      result: event.result,
    })),
  };
}
