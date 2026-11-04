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

export type ReportQuery = { from: string; to: string; group: "day" | "week" | "month" };

export async function getReport(query: ReportQuery, signal?: AbortSignal): Promise<AttendanceReport> {
  const params = new URLSearchParams({ from: query.from, to: query.to, group: query.group });
  const headers = new Headers();
  const csrf = currentCsrfToken();
  if (csrf) headers.set("X-CSRF-Token", csrf);
  const response = await fetch(`/api/panel/reports?${params}`, { headers, signal, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) {
    throw new PanelRequestError(response.status === 403 ? "Acesso negado." : "Não foi possível carregar o relatório.", response.status);
  }
  return response.json() as Promise<AttendanceReport>;
}
