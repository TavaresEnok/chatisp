import { api } from "@/api/client";
import { PanelRequestError } from "@/api/problems";
import type { components } from "@/api/schema";
export type DocumentView = components["schemas"]["ContactDocumentView"];
export type DocumentMatch = components["schemas"]["ContactDocumentMatch"];
// Never echo a document or provider error body into the UI or an exception.
function unwrap<T>(r: { data?: T; response: Response }): T {
  if (r.response.ok && r.data !== undefined) return r.data;
  throw new PanelRequestError(r.response.status === 403 ? "Acesso negado ao documento." : "Não foi possível consultar ou alterar o documento.", r.response.status);
}
export async function masked(contact_id: string, signal?: AbortSignal) { return unwrap(await api.GET("/api/panel/contacts/{contact_id}/document", { params: { path: { contact_id } }, signal, cache: "no-store" })); }
export async function setDocument(contact_id: string, expected_version: number, document: string, signal?: AbortSignal) { return unwrap(await api.PUT("/api/panel/contacts/{contact_id}/document", { params: { path: { contact_id } }, body: { expected_version, request_id: crypto.randomUUID(), document }, signal })); }
export async function clearDocument(contact_id: string, expected_version: number, signal?: AbortSignal) { return unwrap(await api.DELETE("/api/panel/contacts/{contact_id}/document", { params: { path: { contact_id } }, body: { expected_version, request_id: crypto.randomUUID() }, signal })); }
export async function reveal(contact_id: string, signal?: AbortSignal) { return unwrap(await api.POST("/api/panel/contacts/{contact_id}/document/reveal", { params: { path: { contact_id } }, signal, cache: "no-store" })); }
export async function findDocument(document: string, signal?: AbortSignal) { return unwrap(await api.POST("/api/panel/contacts/document-search", { body: { document }, signal, cache: "no-store" })); }
