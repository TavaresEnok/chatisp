import { useState } from "react";
import { loadPlatformUsage, type PlatformUsage } from "@/api/platform-usage";
import { errorMessage } from "@/api/problems";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const tenantStatus: Record<string, string> = {
  active: "Ativo",
  suspended: "Suspenso",
};

export function PlatformUsagePanel() {
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  const [usage, setUsage] = useState<PlatformUsage | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function consult() {
    setError("");
    if (!start || !end) {
      setUsage(null);
      setError("Informe o início e o fim do período.");
      return;
    }
    setPending(true);
    try {
      setUsage(await loadPlatformUsage(start, end));
    } catch (caught) {
      setUsage(null);
      setError(errorMessage(caught));
    } finally {
      setPending(false);
    }
  }

  return (
    <section aria-labelledby="uso-da-plataforma" className="flex flex-col gap-4">
      <h2 id="uso-da-plataforma" className="text-base font-medium">
        Uso da plataforma
      </h2>
      <p className="text-sm text-muted-foreground">
        Contagens por provedor. O período usa o início do dia em America/Sao_Paulo e inclui o dia final.
        Conversas, mensagens, contatos e anexos não aparecem aqui.
      </p>
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          void consult();
        }}
      >
        <div className="flex flex-col gap-2">
          <Label htmlFor="inicio-do-periodo">Início do período</Label>
          <Input
            id="inicio-do-periodo"
            type="date"
            value={start}
            onChange={(event) => {
              setStart(event.target.value);
            }}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="fim-do-periodo">Fim do período</Label>
          <Input
            id="fim-do-periodo"
            type="date"
            value={end}
            onChange={(event) => {
              setEnd(event.target.value);
            }}
          />
        </div>
        <Button type="submit" disabled={pending} className="h-11 w-fit px-3">
          Consultar uso
        </Button>
      </form>
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      {usage ? <UsageTotals usage={usage} /> : null}
    </section>
  );
}

function UsageTotals({ usage }: { usage: PlatformUsage }) {
  return (
    <div className="flex flex-col gap-3">
      <dl className="grid gap-2 text-sm sm:grid-cols-2">
        <div>
          <dt>Provedores ativos</dt>
          <dd>{usage.providers_active}</dd>
        </div>
        <div>
          <dt>Provedores suspensos</dt>
          <dd>{usage.providers_suspended}</dd>
        </div>
      </dl>
      <ul aria-label="Uso por provedor" className="flex flex-col gap-3">
        {usage.providers.map((provider) => (
          <li key={provider.tenant_id} className="rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10">
            <p>{provider.timezone}</p>
            <p>{tenantStatus[provider.status] ?? "Situação desconhecida"}</p>
            <dl className="mt-2 grid gap-1">
              <div className="flex justify-between gap-4">
                <dt>Atendentes ativos</dt>
                <dd>{provider.active_agents}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Caixas</dt>
                <dd>{provider.inboxes}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Conversas abertas</dt>
                <dd>{provider.open_conversations}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Mensagens no período</dt>
                <dd>{provider.messages}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Bytes de anexo</dt>
                <dd>{provider.attachment_bytes === null ? "Indisponível" : provider.attachment_bytes}</dd>
              </div>
            </dl>
          </li>
        ))}
      </ul>
    </div>
  );
}
