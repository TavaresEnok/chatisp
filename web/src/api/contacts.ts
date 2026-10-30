import { api } from "@/api/client";
import type { components } from "@/api/schema";

function unwrap<T>(r: { data?: T; response: Response }): T { if (!r.response.ok || r.data === undefined) throw new Error("Operação indisponível."); return r.data; }
export type ContactInput = components["schemas"]["UpdateSchema"];
export async function list(q: string, offset: number) { return unwrap(await api.GET("/api/panel/contacts", { params: { query: { q, offset, limit: 50 } } })); }
export async function get(contact_id: string) { return unwrap(await api.GET("/api/panel/contacts/{contact_id}", { params: { path: { contact_id } } })); }
export async function history(contact_id: string, offset: number) { return unwrap(await api.GET("/api/panel/contacts/{contact_id}/history", { params: { path: { contact_id }, query: { offset, limit: 50 } } })); }
export async function create(body: components["schemas"]["CreateSchema"]) { return unwrap(await api.POST("/api/panel/contacts", { body })); }
export async function update(contact_id: string, body: ContactInput) { return unwrap(await api.PUT("/api/panel/contacts/{contact_id}", { params: { path: { contact_id } }, body })); }
export async function block(contact_id: string, blocked: boolean, reason: string) { return unwrap(await api.PUT("/api/panel/contacts/{contact_id}/block", { params: { path: { contact_id } }, body: { blocked, reason } })); }
