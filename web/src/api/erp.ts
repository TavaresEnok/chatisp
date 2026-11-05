import { currentCsrfToken } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";

export type ErpCustomer = {
  external_id: string;
  contract_id?: string;
  display_name?: string;
};

export type ErpInvoice = {
  external_id: string;
  label: string;
};

export type ErpLink = {
  id: string;
  contact_id: string;
  conversation_id?: string;
  external_customer_id: string;
  external_contract_id?: string;
  external_protocol?: string;
  selected: boolean;
  customer_url?: string;
};

export type ErpResult = {
  driver?: string;
  simulated?: boolean;
  capability?: string;
  outcome?: string;
  reason?: string;
  consulted_at?: string;
  customer?: ErpCustomer;
  invoices?: ErpInvoice[];
  document?: string;
  pix_code?: string;
  external_ref?: string;
  local_protocol?: string;
  replayed?: boolean;
  message_id?: string;
  attempt_id?: string;
  links?: ErpLink[];
};

export type ErpLinkInput = {
  external_customer_id: string;
  external_contract_id?: string;
  selected: boolean;
};

export type ErpIdentifyInput = {
  reference: string;
  local_key?: string;
};

export type ErpSecondCopyInput = {
  invoice_id: string;
  verification: string;
  local_key: string;
  client_request_id: string;
  artifact: "pix" | "document";
};

async function panelJSON<T>(
  method: "GET" | "POST" | "PUT" | "DELETE",
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (method !== "GET") {
    const csrf = currentCsrfToken();
    if (csrf) headers.set("X-CSRF-Token", csrf);
  }
  const request = new Request(new URL(path, window.location.origin), {
    method,
    credentials: "include",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const response = await fetch(request);
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) throw new PanelRequestError(problemMessage(payload, response.status), response.status);
  if (payload === null || typeof payload !== "object") {
    throw new PanelRequestError("Não foi possível concluir. Tente de novo.", response.status);
  }
  return payload as T;
}

function conversationPath(conversationId: string, suffix: string): string {
  return `/api/panel/conversations/${encodeURIComponent(conversationId)}/erp/${suffix}`;
}

export function listErpLinks(conversationId: string, signal?: AbortSignal): Promise<ErpResult> {
  return panelJSON("GET", conversationPath(conversationId, "links"), undefined, signal);
}

export function createErpLink(conversationId: string, input: ErpLinkInput): Promise<ErpResult> {
  return panelJSON("POST", conversationPath(conversationId, "links"), input);
}

export function deleteErpLink(conversationId: string, linkId: string): Promise<ErpResult> {
  return panelJSON("DELETE", conversationPath(conversationId, `links/${encodeURIComponent(linkId)}`));
}

export function identifyErp(conversationId: string, input: ErpIdentifyInput): Promise<ErpResult> {
  return panelJSON("POST", conversationPath(conversationId, "identify"), input);
}

export function erpSummary(conversationId: string, signal?: AbortSignal): Promise<ErpResult> {
  return panelJSON("GET", conversationPath(conversationId, "summary"), undefined, signal);
}

export function erpInvoices(
  conversationId: string,
  verification: string,
  localKey: string,
  signal?: AbortSignal,
): Promise<ErpResult> {
  const query = new URLSearchParams({ verification });
  if (localKey) query.set("local_key", localKey);
  return panelJSON("GET", `${conversationPath(conversationId, "invoices")}?${query.toString()}`, undefined, signal);
}

export function erpSecondCopy(conversationId: string, input: ErpSecondCopyInput): Promise<ErpResult> {
  return panelJSON("POST", conversationPath(conversationId, "second-copy"), input);
}

export function erpProtocol(conversationId: string, externalProtocol: string): Promise<ErpResult> {
  return panelJSON("POST", conversationPath(conversationId, "protocol"), { external_protocol: externalProtocol });
}

export type ErpCustomerLink = {
  url_template: string;
  version: number;
  placeholder: string;
};

export function getErpCustomerLink(signal?: AbortSignal): Promise<ErpCustomerLink> {
  return panelJSON("GET", "/api/panel/erp/customer-link", undefined, signal);
}

export function saveErpCustomerLink(urlTemplate: string, version: number): Promise<ErpCustomerLink> {
  return panelJSON("PUT", "/api/panel/erp/customer-link", { url_template: urlTemplate, version });
}
