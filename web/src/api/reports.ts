import { currentCsrfToken } from "@/api/client";
import { PanelRequestError } from "@/api/problems";

export type ReportStats = {
  sample: number;
  not_applicable: number;
  average_seconds: number | null;
  median_seconds: number | null;
};

export type AttendanceReport = {
  timezone: string;
  from: string;
  to: string;
  group: string;
  definitions: string[];
  conversations: number;
  cycles: number;
  messages: number;
  first_response: ReportStats;
  close_total: ReportStats;
  close_operational: ReportStats;
  queue_wait: ReportStats;
  results: { result: string; author_kind: string; label: string; count: number }[];
  sectors: { sector_id: string; name: string; cycles: number; queue_waits: number; queue_wait_median_seconds: number | null; responsibility_seconds: number }[];
  attendants: { membership_id: string; label: string; responsibility_seconds: number; messages: number; closures: number }[];
  inboxes: { inbox_id: string; name: string; cycles: number; messages: number }[];
  reasons: { name: string; count: number; overlap: boolean }[];
  tags: { name: string; count: number; overlap: boolean }[];
  heatmap: { weekday: number; hour: number; cycles: number }[];
  buckets: { key: string; cycles: number; messages: number; closed: number; first_response_sample: number; first_response_average_seconds: number | null; operational_sample: number; operational_average_seconds: number | null }[];
  live: {
    in_queue: number;
    in_attendance: number;
    waiting_customer: number;
    snoozed: number;
    unassigned: number;
    online_attendants: number;
    longest_queue_wait_seconds: number;
    wait_alerts: number;
    wait_alert_seconds: number;
  };
  csv: string;
};

export type ReportQuery = {
  from: string;
  to: string;
  group: "day" | "week" | "month";
  sectorId?: string;
  inboxId?: string;
};

export type ReportPrincipal = {
  tenantId: string;
  sessionId: string;
  userId: string;
  membershipId: string;
  role: string;
};

export type ReportOption = { id: string; name: string };

type DirectoryRow = { id?: string; name?: string; active?: boolean };

export function reportQueryKey(principal: ReportPrincipal, mode: "live" | "full", query: ReportQuery, generation = 0) {
  const key = [
    "reports",
    principal.tenantId,
    principal.sessionId,
    principal.userId,
    principal.membershipId,
    principal.role,
    mode,
    query.from,
    query.to,
    query.group,
    query.sectorId ?? "",
    query.inboxId ?? "",
  ] as const;
  // Geração 0 é o recorte ainda não recusado. Depois de um 403 da lista, a
  // geração nova impede reexibir o cache amplo ou filtrado anterior.
  if (generation === 0) return key;
  return [...key, generation] as const;
}

export function reportDirectoryKey(kind: "sectors" | "inboxes", principal: ReportPrincipal) {
  return [
    "reports",
    "directory",
    kind,
    principal.tenantId,
    principal.sessionId,
    principal.userId,
    principal.membershipId,
    principal.role,
  ] as const;
}

async function panelGet<T>(path: string, signal?: AbortSignal): Promise<T> {
  const headers = new Headers();
  const csrf = currentCsrfToken();
  if (csrf) headers.set("X-CSRF-Token", csrf);
  const response = await fetch(path, { headers, signal, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) {
    throw new PanelRequestError(response.status === 403 ? "Acesso negado." : "Não foi possível carregar o relatório.", response.status);
  }
  return response.json() as Promise<T>;
}

function directoryOptions(rows: DirectoryRow[] | null | undefined, activeOnly: boolean): ReportOption[] {
  if (!rows) return [];
  const options: ReportOption[] = [];
  for (const row of rows) {
    if (typeof row.id !== "string" || row.id.length === 0 || typeof row.name !== "string" || row.name.trim().length === 0) continue;
    if (activeOnly && row.active === false) continue;
    options.push({ id: row.id, name: row.name });
  }
  return options;
}

export async function listReportSectors(signal?: AbortSignal): Promise<ReportOption[]> {
  const body = await panelGet<{ sectors?: DirectoryRow[] | null }>("/api/panel/sectors", signal);
  return directoryOptions(body.sectors, true);
}

export async function listReportInboxes(signal?: AbortSignal): Promise<ReportOption[]> {
  const body = await panelGet<{ inboxes?: DirectoryRow[] | null }>("/api/panel/inboxes", signal);
  return directoryOptions(body.inboxes, false);
}

export async function getReport(query: ReportQuery, signal?: AbortSignal): Promise<AttendanceReport> {
  const params = new URLSearchParams({ from: query.from, to: query.to, group: query.group });
  if (query.sectorId) params.set("sector_id", query.sectorId);
  if (query.inboxId) params.set("inbox_id", query.inboxId);
  return panelGet<AttendanceReport>(`/api/panel/reports?${params.toString()}`, signal);
}
