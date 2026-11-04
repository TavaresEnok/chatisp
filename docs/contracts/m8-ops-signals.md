# Sinais operacionais do runtime (M8, complemento de B.4.17)

Este bloco publica totais de baixa cardinalidade no mesmo registry privado de `docs/contracts/m8-runtime-telemetry.md`. Não mede capacidade do piloto, não define p95, RPO nem RTO, e não cria alerta novo no painel.

O coletor só sobe no processo que já tem pool de jobs e pool de plataforma. O modo `api`, sem pool de jobs, não publica estas séries. Ausência não significa zero. Dois processos com pool de jobs publicam o mesmo instantâneo do banco; não some os gauges entre processos.

A leitura usa o papel `chatisp_platform`, com `app.access_scope=platform`, em transação somente leitura. A migração `00046_m8_ops_signals.sql` concede só colunas fechadas: estado, idade, fila e `expected_bytes`. Payload, telefone, credencial, origem remota, texto de erro e corpo continuam revogados. A consulta não devolve identificador de provedor.

## Consulta

Somente o listener privado já documentado, `GET /metrics` em `CHATISP_METRICS_ADDR` (padrão `127.0.0.1:9090`):

```
curl -sS http://127.0.0.1:9090/metrics
```

## Séries

| Série | Tipo | Leitura |
| --- | --- | --- |
| `chatisp_ops_collect_ok` | gauge | 1 se a última leitura concluiu. 0 se falhou. Ausente até a primeira leitura concluída. |
| `chatisp_ops_collect_errors_total` | counter | Leituras que falharam. Não é quantidade de envios. |
| `chatisp_outbound_uncertain` | gauge | Tentativas `outbound_attempts.state = uncertain` em todo o banco. |
| `chatisp_outbound_uncertain_oldest_age_seconds` | gauge | Idade da tentativa uncertain mais antiga. 0 quando a contagem é 0. |
| `chatisp_send_reservations_held` | gauge | Reservas de envio em `held`. |
| `chatisp_job_reservations` | gauge | Reservas de trabalho com `expires_at` no futuro. Rótulo `queue`: `ingress`, `inbound`, `outbound` ou `other`. |
| `chatisp_webhook_items` | gauge | Itens por estado `pending`, `processed`, `quarantined` ou `other`. Sem payload. |
| `chatisp_webhook_pending_oldest_age_seconds` | gauge | Idade do item `pending` mais antigo. 0 quando não há pending. |
| `chatisp_storage_expected_bytes` | gauge | Soma de `expected_bytes` dos anexos `validated`. Não é o uso do disco nem o limite do plano. |
| `chatisp_connectors` | gauge | Contagem por `type` (`meta_cloud`, `evolution`, `other`) e `status` (`pending`, `connected`, `degraded`, `disconnected`, `other`). Sem origem nem credencial. |

A duração do ensaio de restore não entra neste registry. O processo do ensaio grava `metrics.prom` no diretório temporário do dump, com `chatisp_restore_drill_duration_seconds`. Esse número é a duração daquele exercício e não é meta de piloto. Ver `docs/contracts/m8-restore-drill.md`.

O tempo da API continua em `chatisp_http_request_duration_seconds`. O resultado dos workers continua em `chatisp_river_job_results_total`. O destino dos alertas já gravados (idade de job, conector desconectado, webhook incompleto) continua o painel do admin em `/configuracoes`, rota `GET /api/panel/ops-alerts`. Estas séries novas não abrem linha nesse painel.

## O que continua de fora

- k6, p95 de aceite e qualquer número de capacidade. O ensaio local está em `load/perfil-sintetico-local.md`.
- RPO e RTO do piloto. A D2-07 segue aberta.
- Rótulo por tenant, texto, CPF, telefone e segredo.
- Expurgo. Permanece desligado.
