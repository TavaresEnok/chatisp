import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  createErpLink,
  deleteErpLink,
  erpInvoices,
  erpProtocol,
  erpSecondCopy,
  erpSummary,
  identifyErp,
  listErpLinks,
  type ErpInvoice,
  type ErpLink,
  type ErpResult,
  type ErpSecondCopyInput,
} from "@/api/erp";
import { PanelRequestError } from "@/api/problems";
import { invoiceAutoScope, protocolProblem, referenceProblem, secondCopyNotice, secondCopyQueued, unavailableReason } from "@/features/erp/rules";
import type { PanelPrincipal } from "@/stores/session-store";

type Props = {
  principal: PanelPrincipal;
  conversationId: string;
  localProtocol: string;
};

type Draft = {
  invoiceId: string;
  label: string;
  artifact: "pix" | "document";
  verification: string;
};

function scopeKey(principal: PanelPrincipal, conversationId: string) {
  return ["erp", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId, conversationId] as const;
}

function denied(error: unknown): error is PanelRequestError {
  return error instanceof PanelRequestError && error.status === 403;
}

function connectorMessage(error: unknown): string | null {
  if (!(error instanceof PanelRequestError) || error.status !== 503) return null;
  return error.message;
}

function errorText(error: unknown): string {
  if (error instanceof PanelRequestError) return error.message;
  return "Não foi possível concluir. Tente de novo.";
}

function customerLine(result: ErpResult | undefined): string | null {
  const customer = result?.customer;
  if (!customer) return null;
  const name = customer.display_name?.trim() || customer.external_id;
  const contract = customer.contract_id ? ` · ${customer.contract_id}` : "";
  return `${name}${contract}`;
}

export function ErpPanel({ principal, conversationId, localProtocol }: Props) {
  const client = useQueryClient();
  const key = scopeKey(principal, conversationId);
  const links = useQuery({
    queryKey: [...key, "links"],
    queryFn: ({ signal }) => listErpLinks(conversationId, signal),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const selected = (links.data?.links ?? []).find((link) => link.selected) ?? null;
  const summary = useQuery({
    queryKey: [...key, "summary", selected?.id ?? ""],
    queryFn: ({ signal }) => erpSummary(conversationId, signal),
    enabled: selected !== null,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const [customerId, setCustomerId] = useState("");
  const [contractId, setContractId] = useState("");
  const [choose, setChoose] = useState(true);
  const [reference, setReference] = useState("");
  const [identifyKey, setIdentifyKey] = useState("");
  const [verification, setVerification] = useState("");
  const [invoiceKeyInput, setInvoiceKeyInput] = useState("");
  const [protocol, setProtocol] = useState("");
  const [formError, setFormError] = useState("");
  const [invoiceRequest, setInvoiceRequest] = useState<{ verification: string; localKey: string } | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const invoiceKeys = useRef(new Map<string, string>());
  const sendKeys = useRef(new Map<string, { localKey: string; requestId: string }>());
  const selectedId = selected?.id ?? "";
  const [invoiceSelection, setInvoiceSelection] = useState(selectedId);
  if (invoiceSelection !== selectedId) {
    setInvoiceSelection(selectedId);
    setInvoiceRequest(null);
    setDraft(null);
  }
  const invoicesReady = selected !== null && invoiceRequest !== null;

  const invoices = useQuery({
    queryKey: [...key, "invoices", selectedId, invoiceRequest?.verification ?? "", invoiceRequest?.localKey ?? ""],
    queryFn: ({ signal }) => {
      if (!invoiceRequest) throw new Error("consulta de faturas sem verificação");
      return erpInvoices(conversationId, invoiceRequest.verification, invoiceRequest.localKey, signal);
    },
    enabled: invoicesReady,
    retry: false,
    refetchOnWindowFocus: false,
  });

  function refreshLinks() {
    return client.invalidateQueries({ queryKey: [...key, "links"] });
  }

  const link = useMutation({
    mutationFn: () => {
      const input = {
        external_customer_id: customerId.trim(),
        selected: choose,
        ...(contractId.trim() ? { external_contract_id: contractId.trim() } : {}),
      };
      return createErpLink(conversationId, input);
    },
    onSuccess: async () => {
      setCustomerId("");
      setContractId("");
      setFormError("");
      await refreshLinks();
    },
  });
  const unlink = useMutation({
    mutationFn: (linkId: string) => deleteErpLink(conversationId, linkId),
    onSuccess: () => refreshLinks(),
  });
  const selectLink = useMutation({
    mutationFn: async (item: ErpLink) => {
      await createErpLink(conversationId, {
        external_customer_id: item.external_customer_id,
        selected: true,
        ...(item.external_contract_id ? { external_contract_id: item.external_contract_id } : {}),
      });
      try {
        await deleteErpLink(conversationId, item.id);
      } catch (error) {
        const status = error instanceof PanelRequestError ? error.status : 500;
        throw new PanelRequestError(
          "A seleção foi gravada, mas o vínculo anterior continua na lista. Remova a cópia antiga. O protocolo, se houver, permanece nela.",
          status,
        );
      }
      if (!item.external_protocol) return "";
      try {
        await erpProtocol(conversationId, item.external_protocol);
        return "";
      } catch {
        return "O protocolo deste vínculo não foi transferido para a nova seleção.";
      }
    },
    onSuccess: async (notice) => {
      setFormError(notice);
      await refreshLinks();
    },
    onError: () => refreshLinks(),
  });
  const identify = useMutation({
    mutationFn: () =>
      identifyErp(conversationId, {
        reference: reference.trim(),
        ...(identifyKey.trim() ? { local_key: identifyKey.trim() } : {}),
      }),
  });
  const protocolSave = useMutation({
    mutationFn: () => erpProtocol(conversationId, protocol.trim()),
    onSuccess: async () => {
      setProtocol("");
      setFormError("");
      await refreshLinks();
    },
  });
  const secondCopy = useMutation({
    mutationFn: (input: ErpSecondCopyInput) => erpSecondCopy(conversationId, input),
    onSuccess: (result) => {
      if (secondCopyQueued(result)) setDraft(null);
    },
    onError: (error, input) => {
      if (error instanceof PanelRequestError && error.status === 409) {
        sendKeys.current.delete(`${input.artifact}\u0000${input.invoice_id}\u0000${input.verification}`);
      }
      if (denied(error)) setDraft(null);
    },
  });

  const conversationDenial = [links.error, summary.error, link.error, unlink.error, selectLink.error, identify.error, protocolSave.error].find(denied);
  const accessDenied = conversationDenial !== undefined;
  const grantDenied = !accessDenied && (denied(invoices.error) || denied(secondCopy.error));
  const busy = link.isPending || unlink.isPending || selectLink.isPending || identify.isPending || protocolSave.isPending || secondCopy.isPending;
  const simulated = Boolean(links.data?.simulated || summary.data?.simulated || invoices.data?.simulated || identify.data?.simulated || secondCopy.data?.simulated);
  const breaker =
    connectorMessage(links.error) ??
    connectorMessage(summary.error) ??
    connectorMessage(invoices.error) ??
    connectorMessage(link.error) ??
    connectorMessage(identify.error) ??
    connectorMessage(protocolSave.error) ??
    connectorMessage(secondCopy.error) ??
    unavailableReason(summary.data?.outcome, summary.data?.reason) ??
    unavailableReason(invoices.data?.outcome, invoices.data?.reason) ??
    unavailableReason(identify.data?.outcome, identify.data?.reason);
  const listed = invoicesReady && invoices.data?.outcome === "ok" ? (invoices.data.invoices ?? []) : [];
  const { reset: resetSecondCopy } = secondCopy;
  useEffect(() => {
    resetSecondCopy();
  }, [selectedId, resetSecondCopy]);
  const protocolOnLink = (links.data?.links ?? []).find((item) => item.selected)?.external_protocol;
  const protocolFromSave = protocolSave.data?.links?.find((item) => item.id === selected?.id)?.external_protocol;
  const storedProtocol = selected ? (protocolOnLink || protocolFromSave || "") : "";
  const confirmedLocal = protocolSave.data?.local_protocol || localProtocol;

  function submitLink(event: FormEvent) {
    event.preventDefault();
    const customerError = referenceProblem(customerId, "Informe o cliente no ERP.");
    if (customerError) {
      setFormError(customerError);
      return;
    }
    if (contractId.trim()) {
      const contractError = referenceProblem(contractId, "Informe o contrato.");
      if (contractError) {
        setFormError(contractError);
        return;
      }
    }
    setFormError("");
    link.mutate();
  }

  function submitIdentify(event: FormEvent) {
    event.preventDefault();
    const problem = referenceProblem(reference, "Informe a referência autorizada.");
    if (problem) {
      setFormError(problem);
      return;
    }
    if (identifyKey.trim().length > 200) {
      setFormError("Chave local inválida.");
      return;
    }
    setFormError("");
    identify.mutate();
  }

  function consultInvoices(event: FormEvent) {
    event.preventDefault();
    const value = verification.trim();
    if (!value || value.length > 200) {
      setFormError("Informe a verificação.");
      return;
    }
    const typedKey = invoiceKeyInput.trim();
    if (typedKey.length > 200) {
      setFormError("Chave local inválida.");
      return;
    }
    const scope = invoiceAutoScope(selected?.external_customer_id ?? "", selected?.external_contract_id ?? "", value);
    const localKey = typedKey || invoiceKeys.current.get(scope) || crypto.randomUUID();
    if (!typedKey) invoiceKeys.current.set(scope, localKey);
    setFormError("");
    const next = { verification: value, localKey };
    if (invoiceRequest?.verification === next.verification && invoiceRequest.localKey === next.localKey) {
      void invoices.refetch();
      return;
    }
    setInvoiceRequest(next);
  }

  function submitProtocol(event: FormEvent) {
    event.preventDefault();
    const problem = protocolProblem(protocol);
    if (problem) {
      setFormError(problem);
      return;
    }
    setFormError("");
    protocolSave.mutate();
  }

  function clearInvoiceView() {
    setInvoiceRequest(null);
    setDraft(null);
    secondCopy.reset();
  }

  function openDraft(invoice: ErpInvoice, artifact: "pix" | "document") {
    if (!invoiceRequest) return;
    setDraft({
      invoiceId: invoice.external_id,
      label: invoice.label,
      artifact,
      verification: invoiceRequest.verification,
    });
  }

  function confirmSend() {
    if (!draft || secondCopy.isPending) return;
    const mapKey = `${draft.artifact}\u0000${draft.invoiceId}\u0000${draft.verification}`;
    const existing = sendKeys.current.get(mapKey) ?? { localKey: crypto.randomUUID(), requestId: crypto.randomUUID() };
    sendKeys.current.set(mapKey, existing);
    secondCopy.mutate({
      invoice_id: draft.invoiceId,
      verification: draft.verification,
      local_key: existing.localKey,
      client_request_id: existing.requestId,
      artifact: draft.artifact,
    });
  }

  const actionError = [link.error, unlink.error, selectLink.error, identify.error, protocolSave.error, summary.error, invoices.error]
    .find((error) => error && !denied(error) && !connectorMessage(error));

  return (
    <section aria-label="ERP" className="flex min-w-0 flex-col gap-3 rounded-xl bg-card p-3 ring-1 ring-foreground/10">
      <h2 className="text-sm font-medium">ERP</h2>
      {simulated ? <p className="text-sm text-muted-foreground">Simulação. O cadastro comercial não fica neste chat.</p> : null}
      {breaker ? <p role="alert">{breaker}</p> : null}
      {links.isPending ? <p>Carregando ERP…</p> : null}
      {accessDenied ? (
        <>
          <p>Sem permissão para o ERP desta conversa.</p>
          <p role="status">{errorText(conversationDenial)}</p>
        </>
      ) : null}
      {!links.isPending && !accessDenied ? (
        <>
          <ul aria-label="Vínculos" className="flex flex-col gap-2">
            {(links.data?.links ?? []).map((item) => (
              <li key={item.id} className="flex flex-col gap-1 text-sm">
                <span>{item.external_customer_id}{item.external_contract_id ? ` · ${item.external_contract_id}` : ""}</span>
                {item.selected ? <span>Selecionado</span> : (
                  <button type="button" disabled={busy} onClick={() => { clearInvoiceView(); selectLink.mutate(item); }}>Selecionar vínculo {item.external_customer_id}</button>
                )}
                <button type="button" disabled={busy} onClick={() => { if (item.selected) clearInvoiceView(); unlink.mutate(item.id); }}>Remover vínculo {item.external_customer_id}</button>
              </li>
            ))}
          </ul>
          {(links.data?.links ?? []).length === 0 ? <p className="text-sm">Nenhum vínculo nesta conversa.</p> : null}
          <form aria-label="Vincular cliente" className="flex flex-col gap-2" onSubmit={submitLink}>
            <label className="text-sm">Cliente no ERP <input value={customerId} onChange={(event) => setCustomerId(event.target.value)} autoComplete="off" /></label>
            <label className="text-sm">Contrato no ERP <input value={contractId} onChange={(event) => setContractId(event.target.value)} autoComplete="off" /></label>
            <label className="text-sm"><input type="checkbox" checked={choose} onChange={(event) => setChoose(event.target.checked)} /> Selecionar nesta conversa</label>
            <button type="submit" disabled={busy}>Vincular</button>
          </form>
          <form aria-label="Identificar cliente" className="flex flex-col gap-2" onSubmit={submitIdentify}>
            <p className="text-sm text-muted-foreground">A identificação não escolhe o vínculo.</p>
            <label className="text-sm">Referência autorizada <input value={reference} onChange={(event) => setReference(event.target.value)} autoComplete="off" /></label>
            <label className="text-sm">Chave local da identificação <input value={identifyKey} onChange={(event) => setIdentifyKey(event.target.value)} autoComplete="off" /></label>
            <button type="submit" disabled={busy}>Identificar</button>
            {customerLine(identify.data) ? <p>Identificação: {customerLine(identify.data)}</p> : null}
            {identify.data?.outcome === "not_found" ? <p>Cadastro não encontrado.</p> : null}
          </form>
          <section aria-label="Resumo do cliente" className="flex flex-col gap-1">
            <h3 className="text-sm font-medium">Resumo do cliente</h3>
            {!selected ? <p className="text-sm">Escolha um vínculo para ver o resumo.</p> : null}
            {selected && summary.isPending ? <p>Carregando resumo…</p> : null}
            {selected && customerLine(summary.data) ? <p>{customerLine(summary.data)}</p> : null}
            {selected && summary.data?.outcome === "not_found" ? <p>Cadastro não encontrado.</p> : null}
            {selected && summary.isError && !denied(summary.error) && !connectorMessage(summary.error) ? <p role="status">{errorText(summary.error)}</p> : null}
          </section>
          {grantDenied ? <p>Sem permissão para faturas e segunda via.</p> : (
            <form aria-label="Faturas em aberto" className="flex flex-col gap-2" onSubmit={consultInvoices}>
              <label className="text-sm">Verificação <input value={verification} onChange={(event) => setVerification(event.target.value)} autoComplete="off" /></label>
              <label className="text-sm">Chave local das faturas <input value={invoiceKeyInput} onChange={(event) => setInvoiceKeyInput(event.target.value)} autoComplete="off" /></label>
              <button type="submit" disabled={busy || !selected || verification.trim() === ""}>Consultar faturas</button>
            </form>
          )}
          {invoicesReady && invoices.data?.outcome === "hidden" ? <p>Faturas ocultas.</p> : null}
          {invoicesReady && invoices.data?.outcome === "not_found" ? <p>Cadastro não encontrado.</p> : null}
          {listed.length > 0 ? (
            <ul aria-label="Faturas em aberto" className="flex flex-col gap-2">
              {listed.map((invoice) => (
                <li key={invoice.external_id} className="flex flex-col gap-1 text-sm">
                  <span>{invoice.label}</span>
                  {!grantDenied ? (
                    <>
                      <button type="button" disabled={busy} onClick={() => openDraft(invoice, "pix")}>Enviar Pix {invoice.label}</button>
                      <button type="button" disabled={busy} onClick={() => openDraft(invoice, "document")}>Enviar documento {invoice.label}</button>
                    </>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : null}
          {invoicesReady && invoices.data?.outcome === "ok" && listed.length === 0 ? <p>Nenhuma fatura em aberto.</p> : null}
          <form aria-label="Associar protocolo do ERP" className="flex flex-col gap-2" onSubmit={submitProtocol}>
            <p>Protocolo local: {confirmedLocal}</p>
            {storedProtocol ? <p>Protocolo no ERP: {storedProtocol}</p> : null}
            <label className="text-sm">Protocolo no ERP <input value={protocol} onChange={(event) => setProtocol(event.target.value)} autoComplete="off" /></label>
            <button type="submit" disabled={busy || !selected}>Associar protocolo</button>
          </form>
          {formError ? <p role="status">{formError}</p> : null}
          {actionError ? <p role="status">{errorText(actionError)}</p> : null}
          {secondCopy.isError && !denied(secondCopy.error) && !connectorMessage(secondCopy.error) ? <p role="status">{errorText(secondCopy.error)}</p> : null}
          {secondCopy.data ? <p role="status">{secondCopyNotice(secondCopy.data)}</p> : null}
        </>
      ) : null}
      {draft ? (
        <div role="dialog" aria-modal="true" aria-label="Confirmar segunda via" className="flex flex-col gap-2 border p-3">
          <p>{draft.artifact === "pix" ? "Enviar o Pix" : "Enviar o documento"} de {draft.label} nesta conversa?</p>
          <button type="button" disabled={secondCopy.isPending} onClick={confirmSend}>Confirmar envio</button>
          <button type="button" disabled={secondCopy.isPending} onClick={() => setDraft(null)}>Cancelar</button>
        </div>
      ) : null}
    </section>
  );
}
