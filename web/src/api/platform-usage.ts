import { PanelRequestError, problemMessage } from "@/api/problems";
import { currentCsrfToken } from "@/api/client";

export type ProviderUsage = {
  tenant_id: string;
  timezone: string;
  status: string;
  active_agents: number;
  inboxes: number;
  open_conversations: number;
  messages: number;
  attachment_bytes: number | null;
};

export type PlatformUsage = {
  from: string;
  to: string;
  providers_active: number;
  providers_suspended: number;
  providers: ProviderUsage[];
};

export function usageBounds(startDay: string, endDay: string): { from: string; to: string } {
  const end = nextCalendarDay(endDay);
  return {
    from: `${startDay}T00:00:00-03:00`,
    to: `${end}T00:00:00-03:00`,
  };
}

function nextCalendarDay(day: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!match) {
    return day;
  }
  const year = Number(match[1]);
  const month = Number(match[2]);
  const date = Number(match[3]);
  const cursor = new Date(Date.UTC(year, month - 1, date));
  cursor.setUTCDate(cursor.getUTCDate() + 1);
  const y = cursor.getUTCFullYear();
  const m = String(cursor.getUTCMonth() + 1).padStart(2, "0");
  const d = String(cursor.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

export async function loadPlatformUsage(startDay: string, endDay: string): Promise<PlatformUsage> {
  const bounds = usageBounds(startDay, endDay);
  const params = new URLSearchParams({ from: bounds.from, to: bounds.to });
  const headers = new Headers({ Accept: "application/json" });
  const csrf = currentCsrfToken();
  if (csrf) {
    headers.set("X-CSRF-Token", csrf);
  }
  const response = await fetch(`/api/platform/usage?${params.toString()}`, {
    method: "GET",
    credentials: "same-origin",
    headers,
  });
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    throw new PanelRequestError(problemMessage(payload, response.status), response.status);
  }
  return payload as PlatformUsage;
}
