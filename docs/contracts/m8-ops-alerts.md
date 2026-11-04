# Alertas operacionais locais (M8)

Recorte do requisito `14-metricas-alertas` no que já está persistido. O admin do provedor vê um registro local. Não há e-mail, serviço pago nem webhook de saída.

## Sinais que viram alerta

A leitura acontece em `GET /api/panel/ops-alerts`. Ela grava ou encerra linhas em `ops_alerts` e devolve só o que continua aberto. O rótulo sai de campos fechados: fila, tipo e estado do job; tipo do conector; classe do item de webhook. Fila e tipo só aparecem quando já são os do runtime (`ingress`, `inbound`, `outbound`, `scheduled` e os kinds `chatisp.*.v1`). Qualquer outro texto, inclusive CPF, telefone ou argumento, vira «desconhecida» ou «desconhecido». Não entra argumento do River, `errors`, payload, corpo, telefone, credencial, CPF ou origem remota.

| Sinal | Quando alerta | Quando não alerta |
| --- | --- | --- |
| `job_age` | Job em `river.river_job` nos estados `available`, `running`, `retryable`, `scheduled` ou `pending`, com `args.tenant_id` deste provedor e `created_at` estritamente anterior ao limite gravado | Sem linha em `ops_alert_settings`; tabela River ausente; job sem `tenant_id` (ingresso e automação); idade igual ao limite; estado `completed`, `cancelled` ou `discarded` |
| `connector_disconnected` | `channel_connectors.status = disconnected` | `pending`, `connected` e `degraded` |
| `webhook_incomplete` | `webhook_items` existe, o item está `pending` e `created_at` é estritamente anterior ao limite gravado | Tabela ausente; sem limite gravado; idade igual ao limite; `processed` ou `quarantined` |

O limite é o que o admin grava. Não há número inicial disparando alerta. A faixa aceita é de 60 a 604800 segundos, a mesma do `CHECK` da migração `00037_m8_ops_alerts.sql`. Isso é teto do limiar, não capacidade de atendimento. Cada sinal devolve no máximo 100 linhas por leitura do painel.

`PUT /api/panel/ops-alerts/settings` grava `job_age_seconds` e `webhook_age_seconds` com versão otimista. `expected_version` 0 cria a primeira linha. Papel exigido nas duas rotas: `admin` do provedor. Supervisor, atendente e superadmin recebem 403. A gravação exige `Origin` confiável e CSRF.

Se não houver alerta aberto, a resposta traz `alerts: []`. A tela diz «Não há alerta.» e não mostra exemplo.

## O que ficou de fora

- Teste de carga k6 e qualquer número de capacidade, p95, CPU, RAM ou I/O. A D2-07 continua obrigatória antes de publicar capacidade. O ensaio local posterior está em `load/perfil-sintetico-local.md` e não fecha este item.
- Restore de banco, anexos e chaves. A duração medida desse exercício fica em `metrics.prom` do ensaio, não neste painel. Ver `docs/contracts/m8-restore-drill.md`.
- Incerteza, WebSocket, pool, storage e atraso de webhook como séries numéricas neste painel. Elas não abrem alerta. WebSocket, pool e River estão em `docs/contracts/m8-runtime-telemetry.md`. Incerteza, reservas, webhook, storage e conectores agregados estão em `docs/contracts/m8-ops-signals.md`. O destino desses números é `GET /metrics` no listener privado. O painel do admin cita esse destino e não mostra os totais, porque a leitura é do banco inteiro e não de um provedor. O destino destes três alertas continua sendo este painel.
- Conector `degraded` e item de webhook `quarantined`.
- E-mail, webhook externo e qualquer destino pago.
- Corpo de mensagem, CPF, segredo, telefone e argumento completo do job.

As rotas ficam no roteador HTTP e não alteram `backend/api/openapi.json` nem `web/src/api/schema.ts`.
