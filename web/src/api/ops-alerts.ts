import { currentCsrfToken } from "@/api/client";
import { PanelRequestError } from "@/api/problems";

export type OpsAlert = {
  signal: "job_age" | "connector_disconnected" | "webhook_incomplete";
  subject_key: string;
  label: string;
  age_seconds?: number;
};

export type OpsAlertView = {
  job_age_seconds: number | null;
  webhook_age_seconds: number | null;
  settings_version: number;
  configured: boolean;
  alerts: OpsAlert[];
};

export type OpsAlertLimits = {
  job_age_seconds: number;
  webhook_age_seconds: number;
  expected_version: number;
};

async function readView(response: Response): Promise<OpsAlertView> {
  if (!response.ok) {
    const message = response.status === 403
      ? "Acesso negado."
      : response.status === 409
        ? "Outra gravação alterou os limites."
        : response.status === 422
          ? "Informe limites entre 60 e 604800 segundos."
          : "Não foi possível carregar os alertas.";
    throw new PanelRequestError(message, response.status);
  }
  return response.json() as Promise<OpsAlertView>;
}

function headers(): Headers {
  const result = new Headers({ "content-type": "application/json" });
  const csrf = currentCsrfToken();
  if (csrf) result.set("X-CSRF-Token", csrf);
  return result;
}

export async function getOpsAlerts(signal?: AbortSignal): Promise<OpsAlertView> {
  const response = await fetch("/api/panel/ops-alerts", {
    headers: headers(),
    signal,
    credentials: "same-origin",
    cache: "no-store",
  });
  return readView(response);
}

export async function saveOpsLimits(body: OpsAlertLimits): Promise<OpsAlertView> {
  const response = await fetch("/api/panel/ops-alerts/settings", {
    method: "PUT",
    headers: headers(),
    credentials: "same-origin",
    body: JSON.stringify(body),
  });
  return readView(response);
}
