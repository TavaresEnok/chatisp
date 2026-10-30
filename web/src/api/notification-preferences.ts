import { api } from "@/api/client";
import type { components } from "@/api/schema";
function unwrap<T>(r: { data?: T; response: Response }): T { if (!r.response.ok || r.data === undefined) throw new Error("Operação indisponível."); return r.data; }
export async function getPreferences(signal?: AbortSignal) { return unwrap(await api.GET("/api/panel/preferences/notifications", { signal })); }
export async function savePreferences(body: components["schemas"]["Patch"], signal?: AbortSignal) { return unwrap(await api.PATCH("/api/panel/preferences/notifications", { body, signal })); }
