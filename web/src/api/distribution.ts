import { api } from "@/api/client";
import { PanelRequestError } from "@/api/problems";
function unwrap<T>(r: { data?: T; response: Response }): T { if (r.response.ok && r.data !== undefined) return r.data; throw new PanelRequestError("Não foi possível consultar ou configurar a distribuição.", r.response.status); }
export async function distribution(sector_id: string, inbox_id: string, signal?: AbortSignal) { return unwrap(await api.GET("/api/panel/sectors/{sector_id}/distribution", { params: { path: { sector_id }, query: { inbox_id } }, signal })); }
export async function setAuto(sector_id: string, expected_version: number, enabled: boolean, signal?: AbortSignal) { return unwrap(await api.PUT("/api/panel/sectors/{sector_id}/distribution", { params: { path: { sector_id } }, body: { expected_version, enabled }, signal })); }
export async function sectorPresence(sector_id: string, signal?: AbortSignal) { return unwrap(await api.GET("/api/panel/sectors/{sector_id}/presence", { params: { path: { sector_id } }, signal })); }
