import { useState } from "react";

// UI contract only; callers can pass the generated response type after schema generation.
export type PreferenceValues = { sound: boolean; browser: boolean; silent: boolean };
type Permission = NotificationPermission | "unavailable";
export function notificationPermission(): Permission {
  return typeof Notification === "undefined" ? "unavailable" : Notification.permission;
}
export function NotificationPreferences<T extends PreferenceValues>({ prefs, onSave }: { prefs: T; onSave: (next: T) => Promise<void> }) {
  const [permission, setPermission] = useState<Permission>(notificationPermission);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  async function save(field: keyof PreferenceValues, value: boolean) {
    setError(""); setSaving(true);
    try { await onSave({ ...prefs, [field]: value }); } catch { setError("Não foi possível salvar as preferências."); } finally { setSaving(false); }
  }
  async function requestPermission() {
    if (typeof Notification === "undefined") { setPermission("unavailable"); return; }
    try { setPermission(await Notification.requestPermission()); } catch { setPermission(notificationPermission()); setError("Não foi possível solicitar permissão ao navegador."); }
  }
  return <section aria-label="Preferências de notificações">
    <label><input type="checkbox" checked={prefs.sound} disabled={saving} onChange={e => void save("sound", e.target.checked)} /> Som</label>
    <label><input type="checkbox" checked={prefs.browser} disabled={saving} onChange={e => void save("browser", e.target.checked)} /> Notificações do navegador</label>
    <label><input type="checkbox" checked={prefs.silent} disabled={saving} onChange={e => void save("silent", e.target.checked)} /> Modo silencioso</label>
    <p>{permission === "granted" ? "Permissão concedida." : permission === "denied" ? "Permissão recusada no navegador." : permission === "unavailable" ? "Notificações indisponíveis neste navegador." : "Permissão ainda não solicitada."}</p>
    {permission === "default" && <button type="button" onClick={() => void requestPermission()}>Permitir notificações</button>}
    <p>Notificações exigem o painel aberto.</p>
    {error && <p role="alert">{error}</p>}
  </section>;
}
