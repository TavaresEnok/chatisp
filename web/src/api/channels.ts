import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type ManualChannel = components["schemas"]["ManualBody"];

export async function getChannels() {
  const result = await api.GET("/api/panel/channels/meta");
  if (!result.data) throw new Error("Não foi possível carregar as caixas.");
  return result.data;
}

export async function configureChannel(body: ManualChannel) {
  const result = await api.POST("/api/panel/channels/meta", { body });
  if (!result.data) throw new Error("Não foi possível configurar a caixa.");
  return result.data;
}
