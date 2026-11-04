import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getEmbedSettings, saveEmbedSettings } from "@/api/embed";
import { errorMessage } from "@/api/problems";
import type { PanelPrincipal } from "@/stores/session-store";

export function EmbedSettings({ principal }: { principal: PanelPrincipal }) {
  const [open, setOpen] = useState(false);
  if (!open) return <button type="button" onClick={() => setOpen(true)}>Mostrar painel externo</button>;
  return <EmbedForm key={principal.membershipId} principal={principal} />;
}

function EmbedForm({ principal }: { principal: PanelPrincipal }) {
  const admin = principal.role === "admin";
  const client = useQueryClient();
  const key = ["m6-embed-settings", principal.tenantId, principal.membershipId, principal.sessionId];
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => getEmbedSettings(signal), retry: false });
  const [origin, setOrigin] = useState<string | null>(null);
  const [panel, setPanel] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (values: { origin: string; panel: string }) => saveEmbedSettings(values.origin, values.panel),
    onSuccess: async (data) => {
      client.setQueryData(key, data);
      setOrigin(null);
      setPanel(null);
    },
  });
  if (query.isPending) return <p role="status">Carregando painel externo…</p>;
  if (query.isError) return <p role="alert">{errorMessage(query.error)}</p>;
  const currentOrigin = origin ?? query.data.allowed_origin;
  const currentPanel = panel ?? query.data.panel_url;
  return (
    <section aria-label="Painel externo" className="flex max-w-xl flex-col gap-3">
      <h2 className="text-base font-medium">Painel externo</h2>
      <p className="text-sm text-muted-foreground">Supervisor consulta. Somente o administrador altera a origem e a URL.</p>
      {query.data.configured && !query.data.embeddable ? <p role="alert">A URL guardada não pode ser embutida.</p> : null}
      <label className="flex flex-col gap-1 text-sm">Origem permitida
        <input aria-label="Origem permitida" value={currentOrigin} disabled={!admin} onChange={(event) => setOrigin(event.target.value)} />
      </label>
      <label className="flex flex-col gap-1 text-sm">URL do painel
        <input aria-label="URL do painel" value={currentPanel} disabled={!admin} onChange={(event) => setPanel(event.target.value)} />
      </label>
      {save.isError ? <p role="alert">{errorMessage(save.error)}</p> : null}
      {save.isSuccess ? <p role="status">{save.data.configured ? "Painel externo salvo." : "Painel externo removido."}</p> : null}
      {admin ? (
        <div className="flex gap-2">
          <button type="button" onClick={() => save.mutate({ origin: currentOrigin.trim(), panel: currentPanel.trim() })}>Salvar painel externo</button>
          <button type="button" onClick={() => save.mutate({ origin: "", panel: "" })}>Remover painel externo</button>
        </div>
      ) : null}
    </section>
  );
}
