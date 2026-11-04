import { api } from "@/api/client";
import { PanelRequestError } from "@/api/problems";
import type { components } from "@/api/schema";
export type PresenceSnapshot = components["schemas"]["PresenceSnapshot"];
function unwrap<T>(r: { data?: T; response: Response }): T { if (r.response.ok && r.data !== undefined) return r.data; throw new PanelRequestError("Não foi possível atualizar a presença.", r.response.status); }
export async function heartbeat(tab_id: string, signal?: AbortSignal) { return unwrap(await api.POST("/api/panel/presence/heartbeat", { body: { tab_id }, signal })); }
export async function closeTab(tab_id: string) { return unwrap(await api.DELETE("/api/panel/presence/tabs/{tab_id}", { params: { path: { tab_id } }, keepalive: true })); }
export async function manual(state: "available" | "busy", signal?: AbortSignal) { return unwrap(await api.PUT("/api/panel/presence/manual", { body: { request_id: crypto.randomUUID(), state }, signal })); }
export async function occupancy(sector_id?: string, signal?: AbortSignal) { return unwrap(await api.GET("/api/panel/presence/occupancy", { params: { query: { sector_id } }, signal, cache: "no-store" })); }
