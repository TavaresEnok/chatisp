import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { deletePrivacy, exportDemands, exportPrivacy, getPlanUsage, getRetentionPolicy, saveRetentionPolicy, type DemandExport, type PrivacyDeletion, type PrivacyExport, type RetentionPolicy } from "@/api/retention";
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
  const [dossier, setDossier] = useState<PrivacyExport | null>(null);
  const [erasure, setErasure] = useState<PrivacyDeletion | null>(null);
  const [confirm, setConfirm] = useState("");
  const [notice, setNotice] = useState("");
  const [erasing, setErasing] = useState(false);
  const contactVersion = useRef(0);
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
    if (!policy.data || policy.isError) return;
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
    const version = contactVersion.current;
    setNotice("");
    setFile(null);
    try {
      const result = await exportDemands(contact.trim());
      if (version === contactVersion.current) setFile(result);
    } catch {
      if (version === contactVersion.current) setNotice("Não foi possível exportar as demandas deste contato.");
    }
  }

  async function loadPrivacy() {
    const version = contactVersion.current;
    setNotice("");
    setDossier(null);
    try {
      const result = await exportPrivacy(contact.trim());
      if (version === contactVersion.current) setDossier(result);
    } catch {
      if (version === contactVersion.current) setNotice("Não foi possível exportar os dados deste titular.");
    }
  }

  async function erase() {
    if (erasing) return;
    const version = ++contactVersion.current;
    setErasing(true);
    setConfirm("");
    setNotice("");
    setFile(null);
    setDossier(null);
    setErasure(null);
    try {
      const result = await deletePrivacy(contact.trim());
      if (version !== contactVersion.current) return;
      setErasure(result);
      setDossier(null);
      setNotice(result.already_erased
        ? result.objects_removed > 0
          ? "Este titular já estava anonimizado. A remoção dos anexos pendentes foi retomada e concluída."
          : "Este titular já estava excluído. Não havia anexos pendentes para remover."
        : "Dados do titular excluídos. O expurgo automático continua desligado.");
    } catch {
      if (version === contactVersion.current) setNotice("Não foi possível concluir a exclusão. A anonimização pode ter sido aplicada; tente novamente para concluir a remoção dos anexos.");
    } finally {
      setErasing(false);
    }
  }

  function changeContact(value: string) {
    contactVersion.current += 1;
    setContact(value);
    setConfirm("");
    setFile(null);
    setDossier(null);
    setErasure(null);
    setNotice("");
  }

  return (
    <section aria-labelledby="retencao-demandas" className="flex flex-col gap-3">
      <h2 id="retencao-demandas" className="text-base font-medium">Retenção e demandas</h2>
      <p>Histórico estruturado de demandas do atendimento. Não é o histórico completo do provedor nem do ERP.</p>
      <p>Expurgo automático desligado. Mensagens, mídia, envelopes, logs e jobs permanecem.</p>
      {policy.isError ? <p role="alert">Não foi possível ler a política de retenção.</p> : null}
      <fieldset disabled={!policy.data || policy.isError}>
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
      </fieldset>
      {usage.data ? (
        <p>
          Uso atual: {usage.data.agents} atendentes, {usage.data.numbers} números, {usage.data.storage_bytes} bytes. {usage.data.note}
          <br />Limites configurados: {usage.data.max_agents ?? "não definido"} atendentes, {usage.data.max_numbers ?? "não definido"} números, {usage.data.max_storage_bytes ?? "não definido"} bytes.
        </p>
      ) : null}
      <label>
        Contato para exportar
        <input aria-label="Contato para exportar" value={contact} onChange={(event) => changeContact(event.target.value)} />
      </label>
      <button type="button" disabled={erasing || contact.trim() === ""} onClick={() => void download()}>Exportar demandas</button>
      <button type="button" disabled={erasing || contact.trim() === ""} onClick={() => void loadPrivacy()}>Exportar dados do titular</button>
      {dossier ? (
        <div>
          <p>{dossier.label}</p>
          <p>{dossier.profile.name} · {dossier.messages.length} mensagens · documento {dossier.profile.document_stored ? dossier.profile.document_kind : "ausente"}</p>
        </div>
      ) : null}
      <label>
        Digite “excluir titular” para confirmar a exclusão
        <input aria-label="Confirmação da exclusão" value={confirm} onChange={(event) => setConfirm(event.target.value)} />
      </label>
      <button type="button" disabled={erasing || contact.trim() === "" || confirm.trim() !== "excluir titular"} onClick={() => void erase()}>Excluir dados do titular</button>
      {erasure ? <p>{erasure.note} Protocolos preservados: {erasure.protocols.join(", ") || "nenhum"}.</p> : null}
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
