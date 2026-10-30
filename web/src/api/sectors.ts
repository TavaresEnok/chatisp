import { api } from "@/api/client";
import type { components } from "@/api/schema";
function unwrap<T>(r: { data?: T; response: Response }): T { if (!r.response.ok || r.data === undefined) throw new Error("Operação indisponível."); return r.data; }
export async function list() { return unwrap(await api.GET("/api/panel/sectors")); }
export async function details(sector_id: string) { return unwrap(await api.GET("/api/panel/sectors/{sector_id}", { params: { path: { sector_id } } })); }
export async function create(name: string) { return unwrap(await api.POST("/api/panel/sectors", { body: { name } })); }
export async function update(sector_id: string, body: components["schemas"]["UpdateInputBody"]) { const r = await api.PATCH("/api/panel/sectors/{sector_id}", { params: { path: { sector_id } }, body }); if (!r.response.ok) throw new Error("Operação indisponível."); }
export async function link(sector_id: string, target_id: string, kind: "membership" | "inbox", action: "add" | "remove" | "default") {
  const params = { path: { sector_id, target_id } };
  const r = kind === "membership" ? (action === "add" ? await api.PUT("/api/panel/sectors/{sector_id}/memberships/{target_id}", { params }) : await api.DELETE("/api/panel/sectors/{sector_id}/memberships/{target_id}", { params })) : action === "default" ? await api.PUT("/api/panel/sectors/{sector_id}/inboxes/{target_id}/default", { params }) : action === "add" ? await api.PUT("/api/panel/sectors/{sector_id}/inboxes/{target_id}", { params }) : await api.DELETE("/api/panel/sectors/{sector_id}/inboxes/{target_id}", { params });
  if (!r.response.ok) throw new Error("Operação indisponível.");
}
