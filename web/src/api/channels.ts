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

export type NumberHealth = components["schemas"]["NumberHealth"];
export type QualityReport = components["schemas"]["QualityReport"];

export type MetaDispatch = "disabled" | "simulation" | "official";

export async function getNumberHealth(): Promise<{ dispatch: MetaDispatch; numbers: NumberHealth[] }> {
  const result = await api.GET("/api/panel/channels/meta/health");
  if (!result.data) throw new Error("Não foi possível carregar a saúde dos números.");
  return { dispatch: result.data.dispatch, numbers: result.data.numbers ?? [] };
}

export class QualityReadError extends Error {
  readonly status: number;
  constructor(status: number) {
    super("Não foi possível ler a nota de qualidade.");
    this.status = status;
  }
}

export async function readNumberQuality(connectorId: string) {
  const result = await api.POST("/api/panel/channels/meta/{connector_id}/quality", { params: { path: { connector_id: connectorId } } });
  if (!result.data) throw new QualityReadError(result.response.status);
  return result.data;
}
