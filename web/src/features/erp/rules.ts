import type { ErpResult } from "@/api/erp";

export function referenceProblem(raw: string, empty: string): string | null {
  const ref = raw.trim();
  if (!ref) return empty;
  if (ref.length > 128 || /[\s]/.test(ref)) return "Referência inválida.";
  if (ref.startsWith("+") || /^[0-9]+$/.test(ref)) return "Telefone não escolhe o vínculo.";
  return null;
}

export function protocolProblem(raw: string): string | null {
  const value = raw.trim();
  if (!value || value.length > 128 || /[\r\n]/.test(value)) return "Protocolo inválido.";
  return null;
}

export function unavailableReason(outcome: string | undefined, reason: string | undefined): string | null {
  if (outcome !== "unavailable") return null;
  return reason && reason.trim() ? reason : "ERP indisponível para esta consulta.";
}

export function invoiceAutoScope(customerId: string, contractId: string, verification: string): string {
  return `${customerId}\u0000${contractId}\u0000${verification}`;
}

export function secondCopyQueued(result: ErpResult): boolean {
  return result.outcome === "ok" && Boolean(result.message_id) && Boolean(result.attempt_id);
}

export function secondCopyNotice(result: ErpResult): string {
  if (secondCopyQueued(result)) {
    const lead = result.replayed ? "Envio já registrado." : "Segunda via enfileirada.";
    const pix = result.pix_code ? ` Pix: ${result.pix_code}` : "";
    const document = result.document ? " Documento enfileirado." : "";
    return `${lead}${pix}${document}`;
  }
  if (result.outcome === "uncertain") {
    return result.replayed
      ? "Consulta repetida: o ERP segue sem confirmar o envio. Nenhuma mensagem foi enfileirada."
      : "O ERP não confirmou o envio. Nenhuma mensagem foi enfileirada.";
  }
  return "A segunda via não foi enfileirada.";
}
