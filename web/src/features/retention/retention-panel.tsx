import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { exportDemands, getPlanUsage, getRetentionPolicy, saveRetentionPolicy, type DemandExport, type RetentionPolicy } from "@/api/retention";
import { invalidateBranding } from "@/features/branding/cache";

const empty: RetentionPolicy = {
  display_name: "",
  content_days: null,
  media_days: null,
  envelope_days: null,
  log_days: null,
  job_days: null,
  automatic_purge: false,
};

const classes: { key: "content_days" | "media_days" | "envelope_days" | "log_days" | "job_days"; label: string }[] = [
  { key: "content_days", label: "Conteúdo" },
  { key: "media_days", label: "Mídia" },
  { key: "envelope_days", label: "Envelope" },
  { key: "log_days", label: "Registro" },
  { key: "job_days", label: "Tarefa" },
];

export function RetentionPanel() {
  const client = useQueryClient();
  const policy = useQuery({ queryKey: ["retention", "policy"], queryFn: ({ signal }) => getRetentionPolicy(signal), retry: false });
  const usage = useQuery({ queryKey: ["retention", "plan"], queryFn: ({ signal }) => getPlanUsage(signal), retry: false });
  const [draft, setDraft] = useState<RetentionPolicy | null>(null);
  const [contact, setContact] = useState("");
  const [file, setFile] = useState<DemandExport | null>(null);
  const [notice, setNotice] = useState("");
  const current = draft ?? policy.data ?? empty;

  function setDays(key: (typeof classes)[number]["key"], value: string) {
    const parsed = value.trim() === "" ? null : Number(value);
    setDraft({
      ...current,
      [key]: parsed !== null && Number.isInteger(parsed) ? parsed : null,
      automatic_purge: false,
    });
  }

  async function save() {
    setNotice("");
    try {
      const saved = await saveRetentionPolicy({ ...current, automatic_purge: false });
      setDraft(null);
      client.setQueryData(["retention", "policy"], saved);
      await invalidateBranding(client);
      setNotice("Política guardada. O expurgo automático continua desligado.");
    } catch {
      setNotice("Não foi possível guardar a política.");
    }
  }

  async function download() {
    setNotice("");
    setFile(null);
    try {
      setFile(await exportDemands(contact.trim()));
    } catch {
      setNotice("Não foi possível exportar as demandas deste contato.");
    }
  }

  return (
    <section aria-labelledby="retencao-demandas" className="flex flex-col gap-3">
      <h2 id="retencao-demandas" className="text-base font-medium">Retenção e demandas</h2>
      <p>Histórico estruturado de demandas do atendimento. Não é o histórico completo do provedor nem do ERP.</p>
      <p>Expurgo automático desligado. Mensagens, mídia, envelopes, logs e jobs permanecem.</p>
      {policy.isError ? <p role="alert">Não foi possível ler a política de retenção.</p> : null}
      <label>
        Nome do provedor
        <input aria-label="Nome do provedor" value={current.display_name} onChange={(event) => setDraft({ ...current, display_name: event.target.value, automatic_purge: false })} />
      </label>
      {classes.map((item) => (
        <label key={item.key}>
          {item.label} (dias, vazio se ainda não definido)
          <input
            aria-label={item.label}
            inputMode="numeric"
            value={typeof current[item.key] === "number" ? String(current[item.key]) : ""}
            onChange={(event) => setDays(item.key, event.target.value)}
          />
        </label>
      ))}
      <button type="button" onClick={() => void save()}>Guardar política</button>
      {usage.data ? (
        <p>
          Plano: {usage.data.agents} atendentes, {usage.data.numbers} números, {usage.data.storage_bytes} bytes. {usage.data.note}
        </p>
      ) : null}
      <label>
        Contato para exportar
        <input aria-label="Contato para exportar" value={contact} onChange={(event) => setContact(event.target.value)} />
      </label>
      <button type="button" disabled={contact.trim() === ""} onClick={() => void download()}>Exportar demandas</button>
      {file ? (
        <div>
          <p>{file.label}</p>
          <ul>
            {file.demands.map((demand) => (
              <li key={`${demand.protocol}-${demand.cycle}`}>
                {demand.protocol} · {demand.reason} · {demand.synthesis} · {demand.forwarding} · {demand.author}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {notice ? <p role="status">{notice}</p> : null}
    </section>
  );
}
