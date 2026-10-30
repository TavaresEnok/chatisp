import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Navigate } from "react-router";
import { configureChannel, getChannels, type ManualChannel } from "@/api/channels";
import { useSessionStore } from "@/stores/session-store";

const empty: ManualChannel = { inbox_name: "", display_number: "", app_id: "", app_secret: "", waba_id: "", phone_number_id: "", access_token: "" };
const fields: { key: keyof ManualChannel; label: string; secret?: boolean }[] = [
  { key: "inbox_name", label: "Nome da caixa" }, { key: "display_number", label: "Número exibido" },
  { key: "app_id", label: "App ID" }, { key: "app_secret", label: "App Secret", secret: true },
  { key: "waba_id", label: "WABA ID" }, { key: "phone_number_id", label: "Phone Number ID" },
  { key: "access_token", label: "Token de acesso", secret: true },
];

export function ChannelSettingsPage() {
  const principal = useSessionStore((state) => state.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <Navigate to="/entrar" replace />;
  if (principal.role !== "admin") return <p>Acesso restrito a administradores.</p>;
  return <ChannelSettings key={principal.sessionId} scope={[principal.tenantId, principal.userId, principal.sessionId]} />;
}

function ChannelSettings({ scope }: { scope: string[] }) {
  const key = [...scope, "channels"];
  const client = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: getChannels });
  const [form, setForm] = useState<ManualChannel>(empty);
  const mutation = useMutation({ mutationFn: configureChannel, onSuccess: () => { setForm(empty); void client.invalidateQueries({ queryKey: key }); } });
  function submit(event: FormEvent) { event.preventDefault(); mutation.mutate(form); }
  return <section className="space-y-4"><h1>Caixas Meta</h1>
    {query.isPending ? <p>Carregando caixas…</p> : query.isError ? <p role="alert">Não foi possível carregar as caixas.</p> : <>
      {query.data.boxes?.length ? <ul>{query.data.boxes.map((box) => <li key={box.inbox_id}>{box.name} — {box.display_number} — {box.status} {box.enabled ? "" : "(desativada)"}</li>)}</ul> : <p>Nenhuma caixa configurada.</p>}
      {query.data.alerts?.map((alert) => <p role="alert" key={alert.id}>Canal degradado: {alert.action} — {alert.result}</p>)}
    </>}
    <h2>Configuração manual</h2><form onSubmit={submit} className="grid max-w-lg gap-3">
      {fields.map(({ key: field, label, secret }) => <label key={field}>{label}<input className="block w-full rounded border p-2" required type={secret ? "password" : "text"} autoComplete="off" value={form[field]} onChange={(event) => setForm({ ...form, [field]: event.target.value })} /></label>)}
      {mutation.isError && <p role="alert">Não foi possível configurar a caixa.</p>}
      {mutation.isSuccess && <p role="status">Caixa configurada.</p>}
      <button type="submit" disabled={mutation.isPending}>Salvar caixa</button>
    </form></section>;
}
