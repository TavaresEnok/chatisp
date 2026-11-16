import { useState } from "react";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const emails = ["saas.dev@chatisp.local", "admin@chatisp.local", "ana.suporte.demo@chatisp.local", "sofia.supervisao.demo@chatisp.local"] as const;
const accessFile = z.array(z.object({ email: z.enum(emails), senha: z.string().min(1).max(1024) })).min(1).max(4)
  .refine((items) => new Set(items.map((item) => item.email)).size === items.length);

// Deliberately tab memory only: no storage, cookies, network lookup or public secrets.
let passwords: ReadonlyMap<string, string> = new Map();
export function clearDevelopmentPasswords() { passwords = new Map(); }

export function DevelopmentAccess({ disabled, onSelect }: { disabled: boolean; onSelect: (email: string, password: string) => void }) {
  const [loaded, setLoaded] = useState(passwords.size);
  const [error, setError] = useState(false);
  const [reading, setReading] = useState(false);
  return <fieldset className="rounded-lg border p-3" disabled={disabled || reading}>
    <legend className="px-1 text-sm font-medium">Acessos de desenvolvimento</legend>
    <p className="mb-3 text-sm text-muted-foreground">Importe o arquivo privado uma vez nesta aba. Os atalhos preencherão e-mail e senha, sem entrar automaticamente.</p>
    <Label htmlFor="development-access-file">Importar acessos privados (JSON)</Label>
    <Input id="development-access-file" type="file" accept=".json,application/json" className="my-2" onChange={async (event) => {
      const input = event.currentTarget;
      const file = input.files?.[0];
      if (!file) return;
      setReading(true); setError(false);
      try {
        if (file.size > 16_384) throw new Error("Arquivo grande");
        const parsed = accessFile.parse(JSON.parse(await file.text()));
        passwords = new Map(parsed.map((item) => [item.email, item.senha]));
        setLoaded(passwords.size);
      } catch { clearDevelopmentPasswords(); setLoaded(0); setError(true); onSelect("", ""); }
      finally { input.value = ""; setReading(false); }
    }} />
    {error && <p role="alert" className="text-sm text-destructive">Arquivo inválido. Use o JSON privado dos acessos de desenvolvimento.</p>}
    <p role="status" className="mb-3 text-sm text-muted-foreground">{loaded ? `${loaded} acesso(s) carregado(s) somente nesta aba. Recarregar ou fechar remove as senhas.` : "Sem senhas carregadas: os atalhos preenchem apenas o e-mail."}</p>
    <div className="flex flex-wrap gap-2">
      {(["Administrador do SaaS", "Dono da empresa", "Atendimento", "Supervisão"] as const).map((label, index) => {
        const email = emails[index]!;
        return <Button key={email} type="button" variant="outline" onClick={() => onSelect(email, passwords.get(email) ?? "")}>{label}</Button>;
      })}
      {loaded > 0 && <Button type="button" variant="outline" onClick={() => { clearDevelopmentPasswords(); setLoaded(0); onSelect("", ""); }}>Limpar senhas carregadas</Button>}
    </div>
  </fieldset>;
}
