import { getOpsAlerts, saveOpsLimits, type OpsAlertView } from "@/api/ops-alerts";
import { PanelRequestError } from "@/api/problems";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

const signalLabel: Record<OpsAlertView["alerts"][number]["signal"], string> = {
  job_age: "Job antigo",
  connector_disconnected: "Conector desconectado",
  webhook_incomplete: "Webhook sem conclusão",
};

function secondsField(edited: string | null, stored: number | null | undefined): string {
  if (edited !== null) return edited;
  if (stored == null) return "";
  return String(stored);
}

export function OpsAlerts({ sessionId }: { sessionId: string }) {
  const client = useQueryClient();
  const query = useQuery({
    queryKey: ["ops-alerts", sessionId],
    queryFn: ({ signal }) => getOpsAlerts(signal),
    retry: false,
  });
  const [jobAge, setJobAge] = useState<string | null>(null);
  const [webhookAge, setWebhookAge] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const save = useMutation({
    mutationFn: saveOpsLimits,
    onSuccess: (view) => {
      client.setQueryData(["ops-alerts", sessionId], view);
      setJobAge(null);
      setWebhookAge(null);
      setNotice("Limites gravados.");
    },
    onError: (error: unknown) => {
      setNotice(error instanceof PanelRequestError ? error.message : "Não foi possível gravar os limites.");
    },
  });

  const shownJob = secondsField(jobAge, query.data?.job_age_seconds);
  const shownWebhook = secondsField(webhookAge, query.data?.webhook_age_seconds);

  function submit() {
    setNotice("");
    const job = Number(shownJob);
    const webhook = Number(shownWebhook);
    if (!Number.isInteger(job) || !Number.isInteger(webhook)) {
      setNotice("Informe limites entre 60 e 604800 segundos.");
      return;
    }
    save.mutate({
      job_age_seconds: job,
      webhook_age_seconds: webhook,
      expected_version: query.data?.settings_version ?? 0,
    });
  }

  return (
    <section aria-label="Alertas operacionais">
      <h2>Alertas operacionais</h2>
      <p>O registro fica neste painel. Não enviamos e-mail nem chamamos um serviço externo.</p>
      <p>Teste de carga e restauração ficam de fora.</p>
      <p>
        Incerteza, reservas, webhook, storage e conectores saem em GET /metrics no listener privado.
        Este painel não mostra esses totais.
      </p>
      {query.isPending ? <p>Carregando alertas.</p> : null}
      {query.isError ? <p role="alert">Não foi possível carregar os alertas.</p> : null}
      {query.data && query.data.alerts.length === 0 ? <p>Não há alerta.</p> : null}
      {query.data && query.data.alerts.length > 0 ? (
        <ul aria-label="Alertas abertos">
          {query.data.alerts.map((alert) => (
            <li key={alert.subject_key}>
              <p>{signalLabel[alert.signal]}</p>
              <p>{alert.label}</p>
              {alert.age_seconds ? <p>Idade medida: {alert.age_seconds} segundos.</p> : null}
            </li>
          ))}
        </ul>
      ) : null}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <label>
          Idade do job em segundos
          <input inputMode="numeric" value={shownJob} onChange={(event) => setJobAge(event.target.value)} />
        </label>
        <label>
          Idade do webhook pendente em segundos
          <input inputMode="numeric" value={shownWebhook} onChange={(event) => setWebhookAge(event.target.value)} />
        </label>
        <button type="submit" disabled={save.isPending || query.isPending}>Salvar limites</button>
      </form>
      {notice ? <p role="status">{notice}</p> : null}
    </section>
  );
}
