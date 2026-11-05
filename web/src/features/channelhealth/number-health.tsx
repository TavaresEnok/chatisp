import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getNumberHealth, QualityReadError, readNumberQuality, type MetaDispatch, type NumberHealth } from "@/api/channels";

const dateFormat = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

function when(value?: string) {
  if (!value) return "nunca";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "data inválida" : dateFormat.format(date);
}

function qualityFailure(error: unknown) {
  if (error instanceof QualityReadError) {
    if (error.status === 503) return "Leitura indisponível: esta instalação não tem a integração com a Meta autorizada nem a versão da Graph API definida (D2-03).";
    if (error.status === 409) return "A Meta recusou a leitura ou o token do canal é inválido. Confira os alertas do canal.";
  }
  return "Não foi possível ler a nota de qualidade.";
}

function DispatchNotice({ dispatch }: { dispatch: MetaDispatch }) {
  if (dispatch === "official") return null;
  if (dispatch === "simulation") {
    return <p role="status">Envio em modo de simulação: as mensagens vão para um servidor local de teste, não para a Meta.</p>;
  }
  return (
    <p role="alert">
      Envio pela Meta não autorizado nesta instalação. Mensagens para o WhatsApp falham com o código driver_indisponivel, sem chamada externa. O recebimento e as demais funções continuam ativos.
    </p>
  );
}

function NumberCard({ number, onRead, reading, error }: { number: NumberHealth; onRead: () => void; reading: boolean; error: unknown }) {
  const headingId = `saude-${number.connector_id}`;
  const failures = number.failures_by_code ?? [];
  return (
    <article aria-labelledby={headingId} className="space-y-2 rounded border p-3">
      <h3 id={headingId} className="font-medium">{number.name} {number.display_number ? `— ${number.display_number}` : ""}</h3>
      {number.open_credential_alert && <p role="alert">Credencial inválida: o canal está degradado até salvar um token novo.</p>}
      <dl className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-[max-content_1fr]">
        <dt>Estado do canal</dt>
        <dd>{number.status}{number.enabled ? "" : " (desativado)"}</dd>
        <dt>Limite informado pela Meta</dt>
        <dd>
          {number.limit
            ? <>
                {number.limit.current_limit || "sem nível"}
                {number.limit.old_limit ? ` (antes ${number.limit.old_limit})` : ""}
                {number.limit.max_daily_conversations != null ? `, até ${number.limit.max_daily_conversations} conversas por dia` : ""}
                {` — evento ${number.limit.event}, recebido em ${when(number.limit.received_at)}`}
              </>
            : "nenhum aviso de limite recebido"}
        </dd>
        <dt>Nota de qualidade</dt>
        <dd>
          {number.quality
            ? `${number.quality.rating}, lida em ${when(number.quality.read_at)}${number.quality.simulation ? " (servidor simulado, não é a Meta)" : ""}`
            : "não lida"}
        </dd>
        <dt>Envios com falha ({number.window_days} dias)</dt>
        <dd>
          {number.failed_sends}
          {failures.length > 0 && ` — ${failures.map((item) => `${item.code}: ${item.count}`).join(", ")}`}
        </dd>
        <dt>Envios com resultado incerto</dt>
        <dd>{number.uncertain_sends}</dd>
        <dt>Itens em quarentena ({number.window_days} dias)</dt>
        <dd>{number.quarantined_items}</dd>
        <dt>Última mensagem recebida</dt>
        <dd>{when(number.last_inbound_at)}</dd>
      </dl>
      <p className="text-sm">O limite é o nível que a Meta enviou, sem conversão local. O limite de envio do Chat ISP continua sendo o do próprio sistema.</p>
      <button type="button" onClick={onRead} disabled={reading}>{reading ? "Lendo nota…" : "Ler nota de qualidade"}</button>
      {error ? <p role="alert">{qualityFailure(error)}</p> : null}
    </article>
  );
}

export function NumberHealthPanel({ scope }: { scope: string[] }) {
  const key = [...scope, "channels", "health"];
  const client = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: getNumberHealth });
  const mutation = useMutation({
    mutationFn: readNumberQuality,
    onSettled: () => void client.invalidateQueries({ queryKey: key }),
  });
  return (
    <section aria-labelledby="saude-numeros" className="space-y-3">
      <h2 id="saude-numeros">Saúde e limites dos números</h2>
      {query.data ? <DispatchNotice dispatch={query.data.dispatch} /> : null}
      {query.isPending ? <p>Carregando saúde dos números…</p>
        : query.isError ? <p role="alert">Não foi possível carregar a saúde dos números.</p>
          : query.data.numbers.length === 0 ? <p>Nenhum número Meta configurado.</p>
            : query.data.numbers.map((number) => (
              <NumberCard
                key={number.connector_id}
                number={number}
                reading={mutation.isPending && mutation.variables === number.connector_id}
                error={mutation.isError && mutation.variables === number.connector_id ? mutation.error : null}
                onRead={() => mutation.mutate(number.connector_id)}
              />
            ))}
    </section>
  );
}
