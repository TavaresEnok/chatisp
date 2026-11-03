import type { ChannelTemplate } from "@/api/channelpolicy";

export const OFFICIAL_CONNECTOR = "meta_cloud";

export function isOfficialCloud(connectorType: string): boolean {
  return connectorType === OFFICIAL_CONNECTOR;
}

/** O painel não recebe o fim da janela. Não calcula 24 horas no navegador. */
export function windowIndicator(connectorType: string): "hidden" | "unavailable" {
  return isOfficialCloud(connectorType) ? "unavailable" : "hidden";
}

export function templateCanQueue(template: Pick<ChannelTemplate, "state" | "source">): boolean {
  return template.state === "APPROVED" && template.source === "driver";
}
