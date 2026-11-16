import { z } from "zod";
import { Button } from "@/components/ui/button";

const profiles = [
  ["Administrador do SaaS", "saas.dev@chatisp.local"],
  ["Dono da empresa", "admin@chatisp.local"],
  ["Atendimento", "ana.suporte.demo@chatisp.local"],
  ["Supervisão", "sofia.supervisao.demo@chatisp.local"],
] as const;
const accessSchema = z.array(z.object({ email: z.enum(["saas.dev@chatisp.local", "admin@chatisp.local", "ana.suporte.demo@chatisp.local", "sofia.supervisao.demo@chatisp.local"]), senha: z.string().min(10).max(128) })).length(4)
  .refine((items) => new Set(items.map((item) => item.email)).size === 4);

export function DevelopmentAccess({ disabled, onSelect }: { disabled: boolean; onSelect: (email: string, password: string) => void }) {
  if (import.meta.env.VITE_DEVELOPMENT_LOGIN_SHORTCUTS !== "true") return null;
  let accesses: z.infer<typeof accessSchema> = [];
  try {
    const parsed = accessSchema.safeParse(JSON.parse(import.meta.env.VITE_DEVELOPMENT_LOGIN_CREDENTIALS ?? "[]"));
    if (parsed.success) accesses = parsed.data;
  } catch { /* Invalid configuration leaves every shortcut disabled. */ }
  return <fieldset className="rounded-lg border p-3" disabled={disabled || accesses.length !== 4}>
    <legend className="px-1 text-sm font-medium">Acessos de desenvolvimento</legend>
    <p className="mb-3 text-sm text-muted-foreground">Demonstração pública. Clique no perfil para preencher e-mail e senha.</p>
    {accesses.length !== 4 && <p role="status">Acessos de demonstração não configurados.</p>}
    <div className="flex flex-wrap gap-2">
      {profiles.map(([label, email]) => <Button key={email} type="button" variant="outline" onClick={() => {
        const access = accesses.find((item) => item.email === email);
        if (access) onSelect(access.email, access.senha);
      }}>{label}</Button>)}
    </div>
  </fieldset>;
}
