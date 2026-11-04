# Telemetria operacional do runtime (M8, bloco B.4.17)

Este bloco publica séries de baixa cardinalidade no registry privado do processo. Não mede capacidade do piloto, não define meta de latência e não envia alerta.

O mesmo `metrics.Metrics` nasce em `setupRuntimeServices`, antes dos pools e do cliente River. `runtime.Run` recebe essa instância e a serve em `GET /metrics` no listener de `CHATISP_METRICS_ADDR` (padrão `127.0.0.1:9090`). A API HTTP não expõe esse endpoint. Os modos `api`, `worker` e `all` usam o registry do próprio processo. Testes que deixam a métrica `nil` continuam sem séries novas.

Dois processos não compartilham registry. Uma instância `api` sem pool de jobs não publica backlog River: ausência da série não significa fila vazia no banco.

## Consulta

Somente o listener privado. Exemplo local:

```
curl -sS http://127.0.0.1:9090/metrics
```

Não há rótulo de tenant, usuário, mensagem, URL, UUID, argumento ou texto de erro. Fila, estado, papel, worker e resultado fora dos conjuntos abaixo viram `other`.

## Séries

| Série | Tipo | Unidade | Rótulos | Leitura |
| --- | --- | --- | --- | --- |
| `chatisp_websocket_connections` | gauge | conexões | nenhum | Sockets do painel com upgrade aceito e ainda abertos neste processo. Recusa, origem inválida e falha de upgrade não entram. O valor desce ao terminar, inclusive erro e cancelamento. |
| `chatisp_db_pool_connections` | gauge | conexões | `role`, `state` | Instantâneo de `pgxpool.Stat` dos pools já abertos. `state` é `acquired`, `idle` ou `constructing`. |
| `chatisp_db_pool_max_connections` | gauge | conexões | `role` | `MaxConns` configurado no pool. |
| `chatisp_db_pool_acquires_total` | counter | aquisições | `role` | Aquisições bem-sucedidas desde a abertura do pool. |
| `chatisp_db_pool_acquire_seconds_total` | counter | segundos | `role` | Tempo acumulado para adquirir conexão. |
| `chatisp_db_pool_canceled_acquires_total` | counter | aquisições | `role` | Aquisições canceladas pelo contexto. |
| `chatisp_db_pool_new_connections_total` | counter | conexões | `role` | Conexões novas abertas pelo pool. |
| `chatisp_db_pool_empty_acquires_total` | counter | aquisições | `role` | Aquisições que esperaram com o pool vazio. |
| `chatisp_river_collect_ok` | gauge | 0 ou 1 | nenhum | 1 se a última leitura do backlog concluiu. 0 se falhou. Ausente até a primeira leitura concluída. |
| `chatisp_river_collect_errors_total` | counter | leituras | nenhum | Leituras que falharam. Não é quantidade de jobs. Cancelamento do processo durante a leitura não incrementa. |
| `chatisp_river_jobs` | gauge | jobs | `queue`, `state` | Contagem em `river.river_job` na última leitura bem-sucedida. Uma falha posterior não zera este valor. |
| `chatisp_river_oldest_job_age_seconds` | gauge | segundos | `queue`, `state` | Idade desde `created_at` do job mais antigo daquele par. 0 quando a contagem é 0. Mesma regra de snapshot de `chatisp_river_jobs`. |
| `chatisp_river_job_results_total` | counter | execuções | `worker`, `result` | Uma observação por execução real de worker. `snooze` não incrementa `error`. Panic do worker conta como `error` uma vez; o valor e o stack não entram no rótulo. O River segue recuperando, retentando e descartando como antes. |

`role` fechado: `app`, `platform`, `ingress`, `jobs`. O pool dono de migração não entra. A leitura usa `Stat()` e não abre conexão nem grava DSN.

`queue` fechada: `ingress`, `inbound`, `outbound`, `inbound_media`, `webhooks`, `scheduled`. `state` fechado: `available`, `cancelled`, `completed`, `discarded`, `pending`, `retryable`, `running`, `scheduled`.

`worker` fechado: `ingress`, `inbound`, `outbound`, `media`, `webhooks`, `automation`. São os seis workers deste cliente. `result` fechado: `success`, `error`, `cancelled`, `snooze`. Cancelamento é `JobCancel` ou `context.Canceled`. Estouro de prazo continua `error`. Snooze de lease e snooze de webhook ficam em `snooze`.

A coleta do backlog roda só no processo que tem o pool de jobs já autorizado. A consulta é `SELECT` agregado, em transação somente leitura, com tempo de comando de 1,5s. A consulta e o `ROLLBACK` compartilham o contexto de 2s, que também termina quando o processo cancela o coletor. Se o `ROLLBACK` falha ou estoura esse prazo com o processo ainda ativo, a leitura não entra no snapshot e `chatisp_river_collect_ok` fica 0. O cancelamento do processo faz o coletor retornar sem publicar a leitura incompleta e sem incrementar o contador de erro. O intervalo entre leituras é 10s. A primeira leitura ocorre ao subir o coletor. Ela não insere, não atualiza e não apaga job, e não precisa de `chatisp_owner` nem de `BYPASSRLS`. O coletor para antes de `Cleanup` fechar os pools. O encerramento do HTTP e do River permanece o de `76c7176`.

O counter de resultado só é registrado no cliente que executa workers. O cliente que apenas enfileira, usado pela API, não publica esse counter.

As séries HTTP, de build, de processo Go e de conector ERP já existentes continuam no mesmo registry.

## O que este bloco não entrega

- Uso de storage, reservas, envios incertos e idade de webhook. Isso ficou para `docs/contracts/m8-ops-signals.md`, no mesmo registry, só no processo que também lê o River.
- Duração de restauração. O ensaio grava `chatisp_restore_drill_duration_seconds` em `metrics.prom`, no diretório temporário do dump.
- Destino de alerta. O registro local em `docs/contracts/m8-ops-alerts.md` não consome estas séries. O destino das séries é o `GET /metrics` privado.
- Meta de carga, p95 ou capacidade de piloto. Nenhum número deste contrato é benchmark.

M7, M8 e a homologação do piloto não ficam concluídos por estas séries existirem.
