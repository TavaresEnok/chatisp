import { currentCsrfToken } from "@/api/client";
import { PanelRequestError } from "@/api/problems";

export type RetentionPolicy = {
  display_name: string;
  content_days: number | null;
  media_days: number | null;
  envelope_days: number | null;
  log_days: number | null;
  job_days: number | null;
  automatic_purge: boolean;
};

export type PlanUsage = {
  max_agents?: number;
  agents: number;
  max_numbers?: number;
  numbers: number;
  max_storage_bytes?: number;
  storage_bytes: number;
  note: string;
};

export type DemandExport = {
  label: string;
  contact_id: string;
  demands: {
    protocol: string;
    cycle: number;
    opened_at: string;
    closed_at?: string;
    reason: string;
    synthesis: string;
    forwarding: string;
    author: string;
  }[];
};

async function panel(path: string, init?: RequestInit): Promise<Response> {
  const headers = new Headers(init?.headers);
  const csrf = currentCsrfToken();
  if (csrf) headers.set("X-CSRF-Token", csrf);
  if (init?.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...init, headers, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) {
    throw new PanelRequestError("Não foi possível concluir a operação.", response.status);
  }
  return response;
}

export function getRetentionPolicy(signal?: AbortSignal): Promise<RetentionPolicy> {
  return panel("/api/panel/retention", { signal }).then((response) => response.json() as Promise<RetentionPolicy>);
}

export function saveRetentionPolicy(policy: RetentionPolicy): Promise<RetentionPolicy> {
  return panel("/api/panel/retention", { method: "PUT", body: JSON.stringify({ ...policy, automatic_purge: false }) }).then(
    (response) => response.json() as Promise<RetentionPolicy>,
  );
}

export function getPlanUsage(signal?: AbortSignal): Promise<PlanUsage> {
  return panel("/api/panel/plan-limits", { signal }).then((response) => response.json() as Promise<PlanUsage>);
}

export function exportDemands(contactId: string, signal?: AbortSignal): Promise<DemandExport> {
  return panel(`/api/panel/contacts/${contactId}/demands`, { signal }).then((response) => response.json() as Promise<DemandExport>);
}
